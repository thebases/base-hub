package models

// T23: the mail server password is sealed on create/write (via ModelSpec.Prepare) and opened by the sender.

import (
	"context"
	"strings"
	"testing"

	"git.thebase.vn/base_hub/internal/orm"
)

func withMailServerKey(t *testing.T) {
	t.Helper()
	previous := mailServerAppKey
	mailServerAppKey = func() string { return "test-encrypt-key-0123456789abcdef" }
	t.Cleanup(func() { mailServerAppKey = previous })
}

func TestPrepareMailServerSealsPassword(t *testing.T) {
	withMailServerKey(t)
	for _, create := range []bool{true, false} {
		values := orm.RecordMap{"password": "s3cret", "host": "smtp"}
		if err := prepareMailServer(context.Background(), orm.WriteOp{Create: create, Values: values}); err != nil {
			t.Fatal(err)
		}
		sealed, _ := values["password"].(string)
		if !strings.HasPrefix(sealed, "v1:") {
			t.Fatalf("create=%v: password not sealed: %#v", create, values["password"])
		}
		plain, err := openMailServerPassword(sealed)
		if err != nil || plain != "s3cret" {
			t.Fatalf("round trip failed: %q %v", plain, err)
		}
		if values["host"] != "smtp" {
			t.Fatal("other fields must be left alone")
		}
	}
	for _, clear := range []any{false, nil, "", "  "} {
		values := orm.RecordMap{"password": clear}
		if err := prepareMailServer(context.Background(), orm.WriteOp{Values: values}); err != nil {
			t.Fatal(err)
		}
		if values["password"] != clear {
			t.Fatalf("clearing the password must be left alone, got %#v", values["password"])
		}
	}
}

func TestMailServerPasswordRefusesCiphertextAndOpensLegacy(t *testing.T) {
	withMailServerKey(t)
	if err := prepareMailServer(context.Background(), orm.WriteOp{Values: orm.RecordMap{"password": "v1:abc"}}); err == nil {
		t.Fatal("client-supplied ciphertext accepted")
	}
	if plain, err := openMailServerPassword("legacy-plain"); err != nil || plain != "legacy-plain" {
		t.Fatalf("legacy plaintext should pass through, got %q %v", plain, err)
	}
}
