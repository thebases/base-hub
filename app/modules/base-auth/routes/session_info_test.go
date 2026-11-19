package routes

import (
	"net/http"
	"testing"
)

// TestSessionInfoUnauthenticatedIs401 guards T77: GET /oauth/me without credentials used to
// answer 200 with uid 1 (the admin id); it must refuse instead.
func TestSessionInfoUnauthenticatedIs401(t *testing.T) {
	app := newOAuthTestApp()
	req, err := http.NewRequest(http.MethodGet, "/oauth/me", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
	if got := resp.Header.Get("WWW-Authenticate"); got != "Bearer" {
		t.Fatalf("WWW-Authenticate = %q, want Bearer", got)
	}
}
