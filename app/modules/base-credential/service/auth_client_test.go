package service

import (
	"context"
	"strings"
	"testing"

	credentialmodels "git.thebase.vn/base_hub/modules/base-credential/models"
)

type fakeFetcher struct {
	status     int
	body       string
	gotURL     string
	gotType    string
	gotPayload string
}

func (f *fakeFetcher) PostAuth(_ context.Context, url, contentType string, body []byte) (int, []byte, error) {
	f.gotURL, f.gotType, f.gotPayload = url, contentType, string(body)
	return f.status, []byte(f.body), nil
}

func withFetcher(t *testing.T, f TokenFetcher) {
	prev := tokenFetcher
	tokenFetcher = f
	t.Cleanup(func() { tokenFetcher = prev })
}

func TestCallAuthEndpointOIDCClient(t *testing.T) {
	fake := &fakeFetcher{status: 200, body: `{"access_token":"tok","refresh_token":"ref","expires_in":3600}`}
	withFetcher(t, fake)
	access, refresh, expires, err := callAuthEndpoint(context.Background(), &credentialmodels.SysCredentialSecrets{
		CredentialType: credentialmodels.CredentialTypeOIDCClient,
		AuthURL:        "https://idp.example/token",
		ClientID:       "cid",
		ClientSecret:   "sec",
	})
	if err != nil || access != "tok" || refresh != "ref" || expires == nil || *expires != 3600 {
		t.Fatalf("got %q %q %v %v", access, refresh, expires, err)
	}
	if fake.gotType != "application/x-www-form-urlencoded" || !strings.Contains(fake.gotPayload, "grant_type=client_credentials") {
		t.Fatalf("request shape: %s %s", fake.gotType, fake.gotPayload)
	}
}

func TestCallAuthEndpointRejectsNon2xx(t *testing.T) {
	withFetcher(t, &fakeFetcher{status: 401, body: `{}`})
	_, _, _, err := callAuthEndpoint(context.Background(), &credentialmodels.SysCredentialSecrets{
		CredentialType: credentialmodels.CredentialTypeRBAC, AuthURL: "https://x", UserName: "u", Password: "p",
	})
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("expected status error, got %v", err)
	}
}
