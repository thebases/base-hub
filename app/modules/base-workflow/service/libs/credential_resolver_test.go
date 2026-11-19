package libs

import (
	"context"
	"testing"
)

// stubCredentialRefLookup replaces the credential stores for one test and records every ref the
// resolver asked to decrypt.
func stubCredentialRefLookup(t *testing.T) *[]string {
	t.Helper()
	var asked []string
	prev := credentialRefLookup
	credentialRefLookup = func(_ context.Context, _ string, ref string) (string, error) {
		asked = append(asked, ref)
		return "SECRET(" + ref + ")", nil
	}
	t.Cleanup(func() { credentialRefLookup = prev })
	return &asked
}

func TestResolveCredentialRefsIgnoresRefsProducedByInterpolation(t *testing.T) {
	asked := stubCredentialRefLookup(t)
	authored := map[string]any{
		"authorization": "{{token}}",
		"headers":       "Bearer {{token}}",
		"url":           "{{callback_url}}",
		"body":          "#token",
	}
	interpolated := map[string]any{
		"authorization": "@syscred:admin",
		"headers":       "Bearer @cred:db",
		"url":           "@cred:db",
		"body":          "@cred:db",
	}

	got, err := ResolveCredentialRefs(context.Background(), authored, interpolated)
	if err != nil {
		t.Fatalf("ResolveCredentialRefs: %v", err)
	}
	if len(*asked) != 0 {
		t.Fatalf("resolver must not run for payload-supplied refs, asked %v", *asked)
	}
	for k, want := range interpolated {
		if got[k] != want {
			t.Fatalf("%s = %#v, want literal %#v", k, got[k], want)
		}
	}
}

func TestResolveCredentialRefsResolvesAuthoredRefs(t *testing.T) {
	asked := stubCredentialRefLookup(t)
	authored := map[string]any{
		"authorization":     " @syscred:admin ",
		"api_key":           "@cred:db",
		"name_from_payload": "@cred:{{name}}",
		"url":               "https://example.test/{{id}}",
		"retries":           3,
	}
	// What interpolation would hand over: the authored refs carry no tokens so they are unchanged,
	// except the templated ref, which the payload tried to steer to another credential.
	interpolated := map[string]any{
		"authorization":     " @syscred:admin ",
		"api_key":           "@cred:db",
		"name_from_payload": "@cred:other",
		"url":               "https://example.test/42",
		"retries":           3,
	}

	got, err := ResolveCredentialRefs(context.Background(), authored, interpolated)
	if err != nil {
		t.Fatalf("ResolveCredentialRefs: %v", err)
	}
	want := map[string]any{
		"authorization": "SECRET(@syscred:admin)",
		"api_key":       "SECRET(@cred:db)",
		// The ref is read verbatim from the authored text, so the payload cannot pick the name.
		"name_from_payload": "SECRET(@cred:{{name}})",
		"url":               "https://example.test/42",
		"retries":           3,
	}
	for k, w := range want {
		if got[k] != w {
			t.Fatalf("%s = %#v, want %#v", k, got[k], w)
		}
	}
	if len(*asked) != 3 {
		t.Fatalf("expected 3 lookups, got %v", *asked)
	}
	if interpolated["api_key"] != "@cred:db" {
		t.Fatalf("input map was mutated: %#v", interpolated)
	}
}
