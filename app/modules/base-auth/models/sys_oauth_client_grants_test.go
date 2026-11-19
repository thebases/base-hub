package models

import (
	"testing"

	"git.thebase.vn/base_hub/internal/orm"
)

// Plan T36 / SEC-T-18: per-client allowed_grants (empty = all) and the public-client rule.
func TestOAuthClientAllowsGrant(t *testing.T) {
	all := &oauthClientRow{}
	for _, grant := range []string{OAuthGrantAuthorizationCode, OAuthGrantImplicit, OAuthGrantClientCredentials} {
		if !all.AllowsGrant(grant) {
			t.Fatalf("empty allowed_grants must keep %s allowed", grant)
		}
	}
	if all.RequiresS256PKCE() {
		t.Fatal("confidential client must not require PKCE")
	}

	limited := &oauthClientRow{AllowedGrants: []string{OAuthGrantClientCredentials}}
	if !limited.AllowsGrant(OAuthGrantClientCredentials) || limited.AllowsGrant(OAuthGrantAuthorizationCode) || limited.AllowsGrant(OAuthGrantImplicit) {
		t.Fatal("allowed_grants must restrict to the listed grants")
	}

	public := &oauthClientRow{IsPublic: true, AllowedGrants: []string{OAuthGrantAuthorizationCode, OAuthGrantClientCredentials, OAuthGrantImplicit}}
	if !public.AllowsGrant(OAuthGrantAuthorizationCode) || public.AllowsGrant(OAuthGrantClientCredentials) || public.AllowsGrant(OAuthGrantImplicit) {
		t.Fatal("a public client gets authorization_code only, even when the list names more")
	}
	if !public.RequiresS256PKCE() {
		t.Fatal("a public client must require S256 PKCE")
	}
	var missing *oauthClientRow
	if missing.AllowsGrant(OAuthGrantAuthorizationCode) {
		t.Fatal("nil client allows nothing")
	}
}

func TestNormalizeOAuthClientGrants(t *testing.T) {
	for _, tc := range []struct {
		in   any
		want []string
	}{
		{[]any{" Authorization_Code ", "client_credentials", "authorization_code"}, []string{"authorization_code", "client_credentials"}},
		{[]string{"implicit"}, []string{"implicit"}},
		{"authorization_code, implicit", []string{"authorization_code", "implicit"}},
		{nil, []string{}},
		{false, []string{}},
	} {
		values := orm.RecordMap{"allowed_grants": tc.in}
		if err := normalizeOAuthClientGrants(values); err != nil {
			t.Fatalf("%v: %v", tc.in, err)
		}
		got := values["allowed_grants"].([]string)
		if len(got) != len(tc.want) {
			t.Fatalf("%v: got %v want %v", tc.in, got, tc.want)
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Fatalf("%v: got %v want %v", tc.in, got, tc.want)
			}
		}
	}
	for _, bad := range []any{[]any{"password"}, []any{1}, map[string]any{}} {
		if err := normalizeOAuthClientGrants(orm.RecordMap{"allowed_grants": bad}); err == nil {
			t.Fatalf("%v must be refused", bad)
		}
	}
	untouched := orm.RecordMap{"service_name": "x"}
	if err := normalizeOAuthClientGrants(untouched); err != nil || len(untouched) != 1 {
		t.Fatal("a write without allowed_grants must not add the key")
	}
}
