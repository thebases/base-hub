package routes

import (
	"bufio"
	"bytes"
	"testing"
)

// TestWriteServiceBusSSERowFramesIDAndData locks the wire format the
// browser's EventSource parses: an `id:` line (so Last-Event-ID resumes the
// cursor), one `data:` line of JSON, and the blank-line terminator.
func TestWriteServiceBusSSERowFramesIDAndData(t *testing.T) {
	var buf bytes.Buffer
	w := bufio.NewWriter(&buf)
	if err := writeServiceBusSSERow(w, map[string]any{"id": int64(42), "type": "toast"}); err != nil {
		t.Fatalf("write row: %v", err)
	}
	want := "id: 42\ndata: {\"id\":42,\"type\":\"toast\"}\n\n"
	if got := buf.String(); got != want {
		t.Fatalf("frame mismatch\n got: %q\nwant: %q", got, want)
	}
}

// TestWriteServiceBusSSERowWithoutIDOmitsIDLine: a row with no int64 id
// must still be delivered, just without an `id:` line (no resume point).
func TestWriteServiceBusSSERowWithoutIDOmitsIDLine(t *testing.T) {
	var buf bytes.Buffer
	w := bufio.NewWriter(&buf)
	if err := writeServiceBusSSERow(w, map[string]any{"type": "toast"}); err != nil {
		t.Fatalf("write row: %v", err)
	}
	want := "data: {\"type\":\"toast\"}\n\n"
	if got := buf.String(); got != want {
		t.Fatalf("frame mismatch\n got: %q\nwant: %q", got, want)
	}
}
