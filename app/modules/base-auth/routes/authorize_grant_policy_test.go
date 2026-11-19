package routes

import (
	"testing"

	authmodels "git.thebase.vn/base_hub/modules/base-auth/models"
)

type fakeClientPolicy struct {
	allowed map[string]bool
	public  bool
}

func (p fakeClientPolicy) AllowsGrant(grant string) bool { return p.allowed[grant] }
func (p fakeClientPolicy) RequiresS256PKCE() bool        { return p.public }

// Plan T36 / SEC-T-18: /oauth/authorize applies the client's allowed grants and requires S256
// PKCE from a public client, reporting both as redirect errors.
func TestValidateClientGrantPolicy(t *testing.T) {
	codeOnly := fakeClientPolicy{allowed: map[string]bool{authmodels.OAuthGrantAuthorizationCode: true}}
	if perr := validateClientGrantPolicy(codeOnly, authorizeParams{ResponseType: "code"}); perr != nil {
		t.Fatalf("allowed code flow refused: %v", perr)
	}
	if perr := validateClientGrantPolicy(codeOnly, authorizeParams{ResponseType: "token"}); perr == nil || perr.Code != "unauthorized_client" {
		t.Fatalf("implicit on a code-only client: want unauthorized_client, got %v", perr)
	}

	public := fakeClientPolicy{allowed: map[string]bool{authmodels.OAuthGrantAuthorizationCode: true}, public: true}
	for _, params := range []authorizeParams{
		{ResponseType: "code"},
		{ResponseType: "code", CodeChallenge: "abc", CodeChallengeMethod: "plain"},
		{ResponseType: "code", CodeChallengeMethod: "S256"},
	} {
		if perr := validateClientGrantPolicy(public, params); perr == nil || perr.Code != "invalid_request" {
			t.Fatalf("public client %+v: want invalid_request, got %v", params, perr)
		}
	}
	if perr := validateClientGrantPolicy(public, authorizeParams{ResponseType: "code", CodeChallenge: "abc", CodeChallengeMethod: "s256"}); perr != nil {
		t.Fatalf("public client with S256: %v", perr)
	}
}
