package i18n

// File overview:
// - Responsibility: parse gettext .po and .pot files into message entry slices.
// - Key symbols: Entry, ParsePO, unquotePOString.
// - Why: gettext is the established standard for portable translation catalogs
//   used by the standard gettext workflow. Parsing them in pure Go
//   avoids a cgo or shell dependency at the cost of ~100 lines that cover the
//   subset of the format this project actually uses.

import (
	"bufio"
	"io"
	"strings"
)

// Entry holds one translated message unit from a parsed .po or .pot file.
// The file header (msgid == "") is excluded by ParsePO.
type Entry struct {
	MsgID  string // source string (the original English text)
	MsgStr string // translated string; empty in .pot templates and untranslated .po entries
}

// ParsePO reads gettext .po or .pot content from r and returns all message
// entries. The file header block (the entry whose msgid is "") is skipped so
// callers see only real translatable strings.
//
// Supported format features:
//   - Single-line and multi-line (continuation) msgid / msgstr
//   - All comment line types (#, #., #:, #,, #|)
//   - Common escape sequences: \n, \t, \r, \\, \"
//
// Not supported (will be silently ignored):
//   - msgctxt (message context)
//   - msgid_plural / msgstr[n] (plural forms)
//   - obsolete entries (#~)
func ParsePO(r io.Reader) ([]Entry, error) {
	var entries []Entry

	var msgid, msgstr strings.Builder
	// inMsgStr is true when the current block is accumulating msgstr lines.
	// false means we are accumulating msgid lines (or have not yet seen either keyword).
	inMsgStr := false
	// seenBlock is set when we have started reading at least one keyword in the
	// current block; it guards against flushing the initial empty state.
	seenBlock := false

	// flush commits the current block as an Entry (skipping the header), then
	// resets all per-block state.
	flush := func() {
		if !seenBlock {
			return
		}
		seenBlock = false
		inMsgStr = false
		id := msgid.String()
		str := msgstr.String()
		msgid.Reset()
		msgstr.Reset()
		// id == "" is the file header entry; skip it.
		if id == "" {
			return
		}
		entries = append(entries, Entry{MsgID: id, MsgStr: str})
	}

	scanner := bufio.NewScanner(r)
	// Increase the buffer to handle very long msgid / msgstr values that appear in
	// module descriptions embedded in base.pot.
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()

		// A blank line marks the boundary between two entries.
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}

		// All comment line variants start with '#'.
		if strings.HasPrefix(line, "#") {
			continue
		}

		// A continuation line starts with a quoted string and appends to whichever
		// keyword (msgid or msgstr) is currently active in this block.
		if strings.HasPrefix(line, `"`) {
			value := unquotePOString(line)
			if inMsgStr {
				msgstr.WriteString(value)
			} else {
				msgid.WriteString(value)
			}
			continue
		}

		// "msgid <quoted>" starts a new block (or restarts after a header flush).
		if rest, ok := cutPrefix(line, "msgid "); ok {
			// Flush any existing block before opening the new one.
			flush()
			seenBlock = true
			inMsgStr = false
			msgid.WriteString(unquotePOString(rest))
			continue
		}

		// "msgstr <quoted>" opens the translation side of the current block.
		if rest, ok := cutPrefix(line, "msgstr "); ok {
			inMsgStr = true
			msgstr.WriteString(unquotePOString(rest))
			continue
		}

		// msgctxt, msgid_plural, msgstr[n], and other keywords are silently ignored
		// because we do not need plural forms or contexts for backend field labels.
	}

	// Flush the last block which has no trailing blank line.
	flush()

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return entries, nil
}

// cutPrefix returns (tail, true) when s starts with prefix, otherwise ("", false).
// It mirrors strings.CutPrefix added in Go 1.20 and is kept local so the package
// compiles cleanly on older toolchains that may be in use.
func cutPrefix(s, prefix string) (string, bool) {
	if strings.HasPrefix(s, prefix) {
		return s[len(prefix):], true
	}
	return "", false
}

// unquotePOString removes the surrounding double quotes from a gettext quoted
// string and unescapes the four escape sequences that gettext mandates.
// If the value lacks surrounding quotes (which should not happen in a valid .po
// file but does happen when callers pass raw keyword tails) it is returned as-is
// after trimming space.
func unquotePOString(s string) string {
	s = strings.TrimSpace(s)
	if len(s) < 2 || s[0] != '"' || s[len(s)-1] != '"' {
		return s
	}
	s = s[1 : len(s)-1]
	// Unescape in a single pass to avoid double-processing.
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			i++
			switch s[i] {
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			case 'r':
				b.WriteByte('\r')
			case '"':
				b.WriteByte('"')
			case '\\':
				b.WriteByte('\\')
			default:
				// Unknown escape: emit both characters verbatim.
				b.WriteByte('\\')
				b.WriteByte(s[i])
			}
		} else {
			b.WriteByte(s[i])
		}
	}
	return b.String()
}
