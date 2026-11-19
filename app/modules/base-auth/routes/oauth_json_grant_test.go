package routes

// File overview:
// - Responsibility: DB-free coverage for POST /oauth/token's flat JSON-body grant_type contract
//   (jsonTokenRequest/dispatchGrant in oauth_route.go) — verifies it is told apart from the legacy
//   {"params":{"login":...}} JSON envelope and reads the same field names the form-encoded flow
//   uses, without needing a live Postgres fixture.
// - Key symbols: `TestJSONGrantTypeUsesSameFieldNamesAsForm`, `TestJSONGrantTypeUnsupportedMatchesForm`.

import (
	"net/http"
	"net/url"
	"testing"

	testutil "git.thebase.vn/base_hub/internal/test"
)

// TestJSONGrantTypeUsesSameFieldNamesAsForm asserts a flat JSON body with
// grant_type=password is routed through the same handlePasswordGrant
// validation the form-encoded flow uses (oauth_route_test.go), not the legacy
// {"params":{"login":...}} envelope (password_login.go) — a missing
// "username" key must produce the form flow's "username is required" oauth
// error body, never the legacy "login is required" plain-error body.
func TestJSONGrantTypeUsesSameFieldNamesAsForm(t *testing.T) {
	app := newOAuthTestApp()

	resp, err := app.Test(jsonRequest(t, http.MethodPost, "/oauth/token",
		`{"grant_type":"password","password":"secret","db":"anything"}`))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
	body := decodeOAuthError(t, resp)
	if body.Error != "invalid_request" || body.ErrorDescription != "username is required" {
		t.Fatalf("unexpected error body: %#v", body)
	}
}

// TestJSONGrantTypeUnsupportedMatchesForm asserts an unrecognized grant_type
// produces byte-identical error bodies whether sent as JSON or form-encoded —
// the two wire formats must share one grant dispatch, not drift independently.
func TestJSONGrantTypeUnsupportedMatchesForm(t *testing.T) {
	app := newOAuthTestApp()

	jsonResp, err := app.Test(jsonRequest(t, http.MethodPost, "/oauth/token", `{"grant_type":"bogus"}`))
	if err != nil {
		t.Fatalf("json request: %v", err)
	}
	formResp, err := app.Test(formRequest(t, "/oauth/token", url.Values{"grant_type": {"bogus"}}))
	if err != nil {
		t.Fatalf("form request: %v", err)
	}
	if jsonResp.StatusCode != http.StatusBadRequest || formResp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status codes = %d / %d, want both %d", jsonResp.StatusCode, formResp.StatusCode, http.StatusBadRequest)
	}
	jsonBody := decodeOAuthError(t, jsonResp)
	formBody := decodeOAuthError(t, formResp)
	if jsonBody != formBody {
		t.Fatalf("json and form error bodies diverged: %#v vs %#v", jsonBody, formBody)
	}
}

type oauthErrBody struct {
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

func decodeOAuthError(t *testing.T, resp *http.Response) oauthErrBody {
	t.Helper()
	return testutil.DecodeJSON[oauthErrBody](t, resp.Body)
}

// TestClientGrantFailureHidesInternalError: the token endpoint is
// unauthenticated, so a failure deep in the grant (here: no reachable tenant
// DB, which yields an "auth: ..." internal error) must reach the client only
// as the fixed RFC 6749 description (plan T31).
func TestClientGrantFailureHidesInternalError(t *testing.T) {
	app := newOAuthTestApp()

	resp, err := app.Test(formRequest(t, "/oauth/token", url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {"t31-client"},
		"client_secret": {"nope"},
	}))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}
	body := decodeOAuthError(t, resp)
	if body.Error != "invalid_client" || body.ErrorDescription != "invalid client credentials" {
		t.Fatalf("unexpected error body: %#v", body)
	}
}
