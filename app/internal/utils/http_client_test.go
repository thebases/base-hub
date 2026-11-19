package utils

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestBuildRequestBodySupportsContentTypeParameters(t *testing.T) {
	headers := map[string]string{"Content-Type": "application/json; charset=utf-8"}

	body, err := buildRequestBody(http.MethodPost, headers, map[string]any{"status": "ok"})
	if err != nil {
		t.Fatalf("buildRequestBody() error = %v", err)
	}
	if body == nil {
		t.Fatal("buildRequestBody() returned nil body")
	}
}

func TestBuildRequestBodyEncodesForms(t *testing.T) {
	headers := map[string]string{"Content-Type": "application/x-www-form-urlencoded; charset=utf-8"}

	body, err := buildRequestBody(http.MethodPost, headers, url.Values{"name": {"base"}})
	if err != nil {
		t.Fatalf("buildRequestBody() error = %v", err)
	}
	if body == nil {
		t.Fatal("buildRequestBody() returned nil body")
	}

	reader, ok := body.(*strings.Reader)
	if !ok {
		t.Fatalf("buildRequestBody() type = %T, want *strings.Reader", body)
	}
	if got := reader.Len(); got == 0 {
		t.Fatal("buildRequestBody() encoded empty form body")
	}
}
