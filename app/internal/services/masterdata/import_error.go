package masterdata

import (
	"fmt"
)

// ImportError is a master-data import failure tied to a source file and, when
// known, a 1-based line (or item index). Callers use errors.As to report the
// location without parsing the message. Line is 0 when the failure is about the
// file as a whole.
type ImportError struct {
	File string
	Line int
	Err  error
}

func (e *ImportError) Error() string {
	if e.Line > 0 {
		return fmt.Sprintf("%s:%d: %v", e.File, e.Line, e.Err)
	}
	return fmt.Sprintf("%s: %v", e.File, e.Err)
}

func (e *ImportError) Unwrap() error { return e.Err }

// importErrorf builds an ImportError; format may use %w to wrap a cause.
func importErrorf(file string, line int, format string, args ...any) error {
	return &ImportError{File: file, Line: line, Err: fmt.Errorf(format, args...)}
}
