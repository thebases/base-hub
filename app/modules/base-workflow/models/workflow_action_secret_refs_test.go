package models

// Plan T40: action secrets are credential references only; exports never carry a literal;
// outbound authorization_ref is engine-owned; the boot migration finds every literal.

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"git.thebase.vn/base_hub/internal/orm"
)

func TestNormalizeWorkflowActionValuesRefusesLiteralSecrets(t *testing.T) {
	cases := []orm.RecordMap{
		{"action_code": "hmac_action", "hmac_key": "plain-key"},
		{"action_code": "aes_action", "aes_key": "0123456789abcdef"},
		{"action_code": "notify", "target_ref": "https://example.com", "authorization": "Bearer abc"},
		{"action_code": "hmac_action", "extra_params": map[string]any{"key": "plain-key"}},
		{"action_code": "notify", "target_ref": "https://example.com", "extra_params": map[string]any{"authorization": "Bearer abc"}},
		{"action_code": "hmac_action", "hmac_key": "@cred:"},
	}
	for _, values := range cases {
		_, err := normalizeWorkflowActionValues(context.Background(), values, nil, true)
		var ormErr *orm.Error
		if !errors.As(err, &ormErr) || ormErr.Code != orm.ErrInvalidParams {
			t.Fatalf("%#v: err = %v, want invalid params", values, err)
		}
	}
}

func TestNormalizeWorkflowActionValuesAcceptsCredentialRefs(t *testing.T) {
	cases := []orm.RecordMap{
		{"action_code": "hmac_action", "hmac_key": "@cred:signing"},
		{"action_code": "aes_action", "aes_key": " @syscred:vault "},
		{"action_code": "notify", "target_ref": "https://example.com", "authorization": "@syscred:partner"},
		{"action_code": "hmac_action", "extra_params": map[string]any{"key": "@cred:signing", "key_field": "_k"}},
		{"action_code": "hmac_action", "hmac_key": ""},
	}
	for _, values := range cases {
		if _, err := normalizeWorkflowActionValues(context.Background(), values, nil, true); err != nil {
			t.Fatalf("%#v: %v", values, err)
		}
	}
}

func TestRedactWorkflowActionLiteralSecrets(t *testing.T) {
	row := orm.RecordMap{
		"step_key":      "sign",
		"hmac_key":      "plain-key",
		"aes_key":       "@cred:keep",
		"authorization": "",
		"extra_params":  map[string]any{"key": "plain", "key_field": "_k"},
	}
	redactWorkflowActionLiteralSecrets(row)
	if row["hmac_key"] != "@cred:redacted_sign_hmac_key" {
		t.Fatalf("hmac_key = %#v", row["hmac_key"])
	}
	if row["aes_key"] != "@cred:keep" || row["authorization"] != "" {
		t.Fatalf("refs/empties must be kept: %#v", row)
	}
	extra := row["extra_params"].(map[string]any)
	if extra["key"] != "@cred:redacted_sign_extra_key" || extra["key_field"] != "_k" {
		t.Fatalf("extra_params = %#v", extra)
	}
}

func TestWorkflowActionLiteralSecrets(t *testing.T) {
	row := workflowActionSecretRow{
		ID:            12,
		WorkflowID:    sql.NullInt64{Int64: 3, Valid: true},
		HmacKey:       "plain",
		AesKey:        "@cred:already",
		Authorization: "Bearer abc",
	}
	got := workflowActionLiteralSecrets(row, map[string]any{"key": "k", "authorization": "@syscred:x", "other": "v"})
	want := map[string]string{
		"action_12_hmac_key":      "plain",
		"action_12_authorization": "Bearer abc",
		"action_12_extra_key":     "k",
	}
	if len(got) != len(want) {
		t.Fatalf("got %d secrets %#v, want %d", len(got), got, len(want))
	}
	for _, secret := range got {
		if want[secret.credentialName] != secret.value {
			t.Fatalf("unexpected secret %#v", secret)
		}
	}
}

func TestOutboundAuthorizationRefIsEngineOwned(t *testing.T) {
	rpc := orm.ContextWithEnvironment(context.Background(), workflowRPCEnv(7))
	assertForbidden := func(name string, err error) {
		t.Helper()
		var ormErr *orm.Error
		if !errors.As(err, &ormErr) || ormErr.Code != orm.ErrForbidden {
			t.Fatalf("%s: err = %v, want forbidden", name, err)
		}
	}

	assertForbidden("rpc create with ref", ensureOutboundCreateAllowed(rpc, orm.RecordMap{"authorization_ref": "@syscred:partner"}))
	if err := ensureOutboundCreateAllowed(rpc, orm.RecordMap{"target_ref": "https://x"}); err != nil {
		t.Fatalf("rpc create without ref: %v", err)
	}
	if err := ensureOutboundCreateAllowed(OutboundEngineContext(rpc), orm.RecordMap{"authorization_ref": "@syscred:partner"}); err != nil {
		t.Fatalf("engine create with ref: %v", err)
	}
	if err := ensureOutboundCreateAllowed(context.Background(), orm.RecordMap{"authorization_ref": "Bearer abc"}); err == nil {
		t.Fatalf("a literal authorization_ref must be refused")
	}

	row := orm.RecordMap{"authorization_ref": "@syscred:partner", "target_ref": "https://partner.example", "headers": map[string]any{"X": "1"}}
	assertForbidden("move target", ensureOutboundWriteAllowed(rpc, []orm.RecordMap{row}, orm.RecordMap{"target_ref": "https://attacker.example"}))
	assertForbidden("change ref", ensureOutboundWriteAllowed(rpc, []orm.RecordMap{{"authorization_ref": ""}}, orm.RecordMap{"authorization_ref": "@syscred:partner"}))
	assertForbidden("change body", ensureOutboundWriteAllowed(rpc, []orm.RecordMap{row}, orm.RecordMap{"request_body": map[string]any{"a": 1}}))
	if err := ensureOutboundWriteAllowed(rpc, []orm.RecordMap{row}, orm.RecordMap{
		"status":     "queued",
		"target_ref": "https://partner.example",
		"headers":    map[string]any{"X": "1"},
	}); err != nil {
		t.Fatalf("unchanged bound fields and status writes must pass: %v", err)
	}
	if err := ensureOutboundWriteAllowed(rpc, []orm.RecordMap{{"authorization_ref": ""}}, orm.RecordMap{"target_ref": "https://elsewhere"}); err != nil {
		t.Fatalf("rows without a ref keep today's behavior: %v", err)
	}
}
