package models

// File overview:
// - Responsibility: verifies the transport-agnostic sys.attachment rules — mimetype trust policy,
//   datas decoding, and binding validation (SRS-F-019).
// - Key symbols: focused Test cases, no database required.
// - Pattern: Locks down validation invariants so refactors preserve the attachment contract.

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"

	"git.thebase.vn/base_hub/internal/orm"
)

// TestResolveSysAttachmentMimetypeNeverTrustsHTMLishClaims verifies the
// SRS-F-019 serving rule: HTML-like client content types are replaced by the
// server-side detection so stored XSS cannot ride on a forged mimetype.
func TestResolveSysAttachmentMimetypeNeverTrustsHTMLishClaims(t *testing.T) {
	pngHeader := []byte("\x89PNG\r\n\x1a\n" + strings.Repeat("\x00", 32))

	// An HTML-ish client claim over binary content falls back to detection.
	if got := resolveSysAttachmentMimetype("text/html", pngHeader); strings.Contains(got, "html") {
		t.Fatalf("expected html client claim to be replaced, got %q", got)
	}
	// A benign client claim over benign content is preserved.
	if got := resolveSysAttachmentMimetype("image/png", pngHeader); got != "image/png" {
		t.Fatalf("expected benign client mimetype to be kept, got %q", got)
	}
	// HTML content is always labeled by detection, regardless of the claim.
	htmlPayload := []byte("<!DOCTYPE html><html><body>hi</body></html>")
	if got := resolveSysAttachmentMimetype("image/png", htmlPayload); !IsHTMLishMimetype(got) {
		t.Fatalf("expected html content to be detected as html-ish, got %q", got)
	}
}

// TestIsHTMLishMimetype verifies the closed set of browser-executable markers.
func TestIsHTMLishMimetype(t *testing.T) {
	for _, dangerous := range []string{"text/html", "application/xhtml+xml", "image/svg+xml", "text/javascript", "application/xml"} {
		if !IsHTMLishMimetype(dangerous) {
			t.Fatalf("expected %q to be html-ish", dangerous)
		}
	}
	for _, safe := range []string{"image/png", "application/pdf", "text/plain", "application/octet-stream"} {
		if IsHTMLishMimetype(safe) {
			t.Fatalf("expected %q to be safe", safe)
		}
	}
}

// TestDecodeSysAttachmentDatas verifies the base64 payload contract.
func TestDecodeSysAttachmentDatas(t *testing.T) {
	payload := []byte("attachment content")
	decoded, err := decodeSysAttachmentDatas(orm.RecordMap{"datas": base64.StdEncoding.EncodeToString(payload)})
	if err != nil {
		t.Fatalf("decode valid datas: %v", err)
	}
	if string(decoded) != string(payload) {
		t.Fatalf("decoded payload mismatch")
	}

	if _, err := decodeSysAttachmentDatas(orm.RecordMap{"datas": ""}); err == nil {
		t.Fatalf("expected empty datas to be rejected")
	}
	if _, err := decodeSysAttachmentDatas(orm.RecordMap{"datas": "not-base64!!!"}); err == nil {
		t.Fatalf("expected invalid base64 to be rejected")
	}
}

// TestValidateSysAttachmentBinding verifies the (sys_model, model_id) contract
// for the cases that need no database: unknown model, orphan model_id, and the
// unbound shape.
func TestValidateSysAttachmentBinding(t *testing.T) {
	ctx := context.Background()

	// Unbound attachments are valid.
	if err := validateSysAttachmentBinding(ctx, nil, orm.RecordMap{}); err != nil {
		t.Fatalf("expected unbound attachment to validate, got %v", err)
	}
	// model_id without sys_model is ambiguous and rejected.
	if err := validateSysAttachmentBinding(ctx, nil, orm.RecordMap{"model_id": 7}); err == nil {
		t.Fatalf("expected model_id without sys_model to be rejected")
	}
	// Unknown sys_model is a validation error per SRS-F-019.
	if err := validateSysAttachmentBinding(ctx, nil, orm.RecordMap{"sys_model": "model.that.does.not.exist", "model_id": 7}); err == nil {
		t.Fatalf("expected unregistered sys_model to be rejected")
	}
	// A registered model with a non-positive id is rejected before any access check.
	if err := validateSysAttachmentBinding(ctx, nil, orm.RecordMap{"sys_model": SysAttachmentModelName, "model_id": 0}); err == nil {
		t.Fatalf("expected non-positive model_id to be rejected")
	}
}
