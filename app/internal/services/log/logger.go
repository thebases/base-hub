package log

// File overview:
// - Responsibility: encapsulates domain/service orchestration for logger, keeping integrations and multi-step business flows out of transport handlers.
// - Key symbols: `Init`, `SetLevel`, `Level`, `sinks`.
// - Pattern: Keeps one focused responsibility per file so adjacent packages can reuse this logic without depending on unrelated transport or persistence details.

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"gopkg.in/natefinch/lumberjack.v2"
)

// ---------- config ----------
type Level int

const (
	// # Trace: 0; Debug: 1; Info: 2;  Warn: 3; Error: 4; Fatal: 5; Panic: 6
	TRACE Level = iota
	DEBUG
	INFO
	WARN
	ERROR
	FATAL
	PANIC
)

var (
	currentLevel = INFO
)

type sinks struct {
	console io.Writer // usually os.Stdout
	file    io.Writer // lumberjack rotating file
}

var out sinks
var loggerDir string // absolute dir of this package (normalized)

// ---------- init & setup ----------

func Init(logPath string) {
	// resolve the absolute dir of this logger package to skip its frames later
	if _, file, _, ok := runtime.Caller(0); ok {
		loggerDir = toSlash(filepath.Dir(file))
	}

	rot := &lumberjack.Logger{
		Filename:   logPath,
		MaxSize:    50,
		MaxBackups: 5,
		MaxAge:     30,
		Compress:   true,
	}

	out = sinks{
		console: os.Stdout,
		file:    rot,
	}

	// Redirect std log to our writer with level "STD"
	log.SetOutput(&stdAdapter{level: "STD"})
	log.SetFlags(0) // we print time/file ourselves
	log.SetPrefix("")
}

// ---------- public helpers (concise) ----------

func SetLevel(l Level) { currentLevel = l }

func Debugf(format string, v ...any) { write("Debug", fmt.Sprintf(format, v...)) }

func Infof(format string, v ...any) { write("Info", fmt.Sprintf(format, v...)) }

func Warnf(format string, v ...any) { write("Warn", fmt.Sprintf(format, v...)) }

func Errorf(format string, v ...any) { write("Error", fmt.Sprintf(format, v...)) }

func Fatalf(format string, v ...any) { write("Fatal", fmt.Sprintf(format, v...)) }

// If you still want Println-style:
func Debug(v ...any) { write("Debug", fmt.Sprint(v...)) }

func Info(v ...any) { write("Info", fmt.Sprint(v...)) }

func Warn(v ...any) { write("Warn", fmt.Sprint(v...)) }

func Error(v ...any) { write("Error", fmt.Sprint(v...)) }

func Trace(v ...any) { write("Trace", fmt.Sprint(v...)) }

func Fatal(v ...any) { write("Fatal", fmt.Sprint(v...)) }

func Panic(v ...any) { write("Panic", fmt.Sprint(v...)) }

// ---------- std log adapter ----------

type stdAdapter struct{ level string } // implements io.Writer

func (a *stdAdapter) Write(p []byte) (int, error) {
	// std log already formatted message in p; we just adopt it
	write(a.level, strings.TrimRight(string(p), "\n"))
	return len(p), nil
}

// ---------- core writer ----------
func write(level string, msg string) {
	// ---- 1. Filter by level ----
	if !shouldLog(level) {
		return
	}

	// ---- 2. Format message ----
	ts := time.Now().Format("2006/01/02 15:04:05.000000")
	file, line := findCaller()

	plain := fmt.Sprintf("%s %s:%d: [%s] %s\n", ts, file, line, level, msg)
	colLevel := colorWrap(level, "["+level+"]")
	colored := fmt.Sprintf("%s %s:%d: %s %s\n", ts, file, line, colLevel, msg)

	// ---- 3. Output ----
	consoleSink, fileSink := resolveSinks()
	_, _ = io.WriteString(consoleSink, colored)
	if fileSink != nil {
		_, _ = io.WriteString(fileSink, plain)
	}

	// ---- 4. Handle fatal/panic ----
	switch level {
	case "Fatal":
		os.Exit(1)
	case "Panic":
		panic(msg)
	}
}

// resolveSinks makes early bootstrap logging safe before Init(...) wires the
// configured rotating-file sink. During that window, logging falls back to
// stdout instead of panicking on a nil writer.
func resolveSinks() (io.Writer, io.Writer) {
	consoleSink := out.console
	if consoleSink == nil {
		consoleSink = os.Stdout
	}
	return consoleSink, out.file
}

func shouldLog(level string) bool {
	levelMap := map[string]Level{
		"Trace": TRACE,
		"Debug": DEBUG,
		"Info":  INFO,
		"Warn":  WARN,
		"Error": ERROR,
		"Fatal": FATAL,
		"Panic": PANIC,
		"STD":   INFO, // treat std log as INFO
	}

	lvl, ok := levelMap[level]
	if !ok {
		return true
	}
	return lvl >= currentLevel
}

// func write(level, msg string) {
// 	ts := time.Now().Format("2006/01/02 15:04:05.000000")
// 	file, line := findCaller()

// 	// plain line (no ANSI) for file sink
// 	plain := fmt.Sprintf("%s %s:%d: [%s] %s\n", ts, file, line, level, msg)

// 	// colored line for console
// 	colLevel := colorWrap(level, "["+level+"]")
// 	colored := fmt.Sprintf("%s %s:%d: %s %s\n", ts, file, line, colLevel, msg)

// 	// write separately to keep file clean
// 	_, _ = io.WriteString(out.console, colored)
// 	_, _ = io.WriteString(out.file, plain)
// }

// ---------- caller resolution ----------

func findCaller() (string, int) {
	// collect PCs; skip this function and write() itself
	const maxDepth = 32
	var pcs [maxDepth]uintptr
	n := runtime.Callers(3, pcs[:]) // skip: runtime.Callers, findCaller, write
	frames := runtime.CallersFrames(pcs[:n])

	for {
		f, more := frames.Next()
		if f.File == "" {
			if !more {
				break
			}
			continue
		}
		fp := toSlash(f.File)

		// skip stdlib runtime and log
		if strings.Contains(fp, "/runtime/") || strings.HasSuffix(fp, "/log.go") {
			if !more {
				break
			}
			continue
		}
		// skip our logger package files (regardless of repo layout)
		if loggerDir != "" && strings.HasPrefix(fp, loggerDir) {
			if !more {
				break
			}
			continue
		}
		// found real caller
		return filepath.Base(fp), f.Line
	}
	return "???", 0
}

func toSlash(p string) string { return filepath.ToSlash(p) }

// ---------- colors (console only) ----------

const (
	colReset = "\x1b[0m"
	colRed   = "\x1b[31m"
	colYel   = "\x1b[33m"
	colBlu   = "\x1b[34m"
	colCyn   = "\x1b[36m"
)

func colorWrap(level, s string) string {
	switch level {
	case "Error":
		return colRed + s + colReset
	case "Warn":
		return colYel + s + colReset
	case "Info":
		return colCyn + s + colReset
	case "Debug":
		return colBlu + s + colReset
	case "STD":
		return s // std logs usually neutral
	default:
		return s
	}
}
