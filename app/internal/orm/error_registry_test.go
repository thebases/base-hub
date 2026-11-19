package orm

import (
	"errors"
	"fmt"
	"testing"
)

func TestErrorFromKeepsWrappedTypedError(t *testing.T) {
	err := fmt.Errorf("ctx: %w", NotFoundf("device %d not found", 3))
	got := ErrorFrom(err)
	if got.Code != ErrNotFound || got.Status != 404 {
		t.Fatalf("got %+v", got)
	}
}

func TestErrorFromDefaultsPlainErrorToServerError(t *testing.T) {
	got := ErrorFrom(errors.New("boom"))
	if got.Code != ErrServer || got.Status != 400 {
		t.Fatalf("got %+v", got)
	}
}

func TestErrorFromMapsAliasError(t *testing.T) {
	got := ErrorFrom(&AliasError{Status: 400, Code: "invalid_json", Message: "bad"})
	if got.Code != ErrParse {
		t.Fatalf("got %+v", got)
	}
}

func TestLegacyErrorDropsRedundantReason(t *testing.T) {
	got := LegacyError(404, map[string]any{"error": "not_found", "message": "device command not found"})
	if got.Code != ErrNotFound || got.Data != nil || got.Message != "device command not found" {
		t.Fatalf("unexpected %+v", got)
	}
	custom := LegacyError(400, map[string]any{"error": "bad_serial", "message": "serial is bad"})
	if custom.Data["reason"] != "bad_serial" || custom.Message != "serial is bad" {
		t.Fatalf("module reason lost: %+v", custom)
	}
	if empty := LegacyError(404, map[string]any{}); empty.Message == "" {
		t.Fatal("message must never be empty")
	}
}
