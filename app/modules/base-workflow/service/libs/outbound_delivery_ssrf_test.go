package libs

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"git.thebase.vn/base_hub/internal/services/safehttp"
)

// T42: workflow sends (post_output_object and the notify delivery worker share
// postJSONRequest) must not reach loopback/private targets unless
// server.outboundAllowCidrs opens them.
func TestWorkflowHTTPRefusesLoopbackTarget(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
	}))
	defer srv.Close()

	_, _, err := PostWorkflowHTTPJSON(context.Background(), srv.URL, nil, map[string]any{"a": 1})
	if !errors.Is(err, safehttp.ErrBlockedAddress) {
		t.Fatalf("PostWorkflowHTTPJSON err = %v, want ErrBlockedAddress", err)
	}
	if resp, err := postJSONRequest(context.Background(), srv.URL+"/x", nil, map[string]any{}); err == nil {
		resp.Body.Close()
		t.Fatal("delivery post reached a loopback target")
	}
	if hits.Load() != 0 {
		t.Fatalf("loopback server received %d requests", hits.Load())
	}
}
