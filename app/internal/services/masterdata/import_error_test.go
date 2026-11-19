package masterdata

import (
	"errors"
	"io"
	"testing"
)

func TestImportErrorCarriesLocationAndCause(t *testing.T) {
	err := importErrorf("data/a.json", 3, "invalid data payload: %w", io.EOF)
	var ie *ImportError
	if !errors.As(err, &ie) || ie.File != "data/a.json" || ie.Line != 3 {
		t.Fatalf("errors.As = %+v", ie)
	}
	if !errors.Is(err, io.EOF) {
		t.Fatal("cause must stay reachable through errors.Is")
	}
	if got := err.Error(); got != "data/a.json:3: invalid data payload: EOF" {
		t.Fatalf("message changed: %q", got)
	}
	if got := importErrorf("data/a.json", 0, "bad root").Error(); got != "data/a.json: bad root" {
		t.Fatalf("file-level message: %q", got)
	}
}
