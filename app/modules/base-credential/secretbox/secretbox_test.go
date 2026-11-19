package secretbox

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"git.thebase.vn/base_hub/internal/config"
	"git.thebase.vn/base_hub/internal/orm"
)

const (
	testKey   = "0123456789abcdef0123456789abcdef-current"
	testKey2  = "fedcba9876543210fedcba9876543210-replacement"
	testLabel = "sys.credential"
)

// legacyZeroKeySeal reproduces the pre-2026-11-14 format: base64(nonce||ciphertext) under an
// all-zero AES-256 key with no version prefix.
func legacyZeroKeySeal(t *testing.T, plaintext string) string {
	t.Helper()
	var zero [32]byte
	block, err := aes.NewCipher(zero[:])
	if err != nil {
		t.Fatal(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(gcm.Seal(nonce, nonce, []byte(plaintext), nil))
}

func TestSealOpenRoundTripIsVersionedAndLabelScoped(t *testing.T) {
	sealed, err := Seal(testKey, testLabel, "s3cret")
	if err != nil {
		t.Fatal(err)
	}
	if !IsCurrent(sealed) || strings.Contains(sealed, "s3cret") {
		t.Fatalf("sealed value %q is not a v1 ciphertext", sealed)
	}
	plain, err := Open(testKey, testLabel, sealed)
	if err != nil || plain != "s3cret" {
		t.Fatalf("Open = %q, %v; want s3cret", plain, err)
	}
	if _, err := Open(testKey, "base.workflow.credential", sealed); err == nil {
		t.Fatal("a value sealed for one label must not open under another label's key")
	}
	if _, err := Open(testKey2, testLabel, sealed); err == nil {
		t.Fatal("a value sealed under one app key must not open under another")
	}
}

func TestEmptyKeyFailsClosed(t *testing.T) {
	if _, err := Seal("  ", testLabel, "x"); !errors.Is(err, ErrKeyNotConfigured) {
		t.Fatalf("Seal with empty key: err = %v, want ErrKeyNotConfigured", err)
	}
	sealed, err := Seal(testKey, testLabel, "x")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Open("", testLabel, sealed); !errors.Is(err, ErrKeyNotConfigured) {
		t.Fatalf("Open with empty key: err = %v, want ErrKeyNotConfigured", err)
	}
}

// The boot migration passes the current key as old and new: a legacy zero-key value must come out
// readable under the app key, and a second run must change nothing.
func TestResealMigratesLegacyZeroKeyValue(t *testing.T) {
	legacy := legacyZeroKeySeal(t, "legacy-password")
	if plain, err := Open(testKey, testLabel, legacy); err != nil || plain != "legacy-password" {
		t.Fatalf("legacy value must stay readable before migration: %q, %v", plain, err)
	}
	migrated, changed, err := Reseal(testKey, testKey, testLabel, legacy)
	if err != nil || !changed {
		t.Fatalf("Reseal legacy: changed=%v err=%v", changed, err)
	}
	if !IsCurrent(migrated) {
		t.Fatalf("migrated value %q is not v1", migrated)
	}
	if plain, err := Open(testKey, testLabel, migrated); err != nil || plain != "legacy-password" {
		t.Fatalf("migrated value: %q, %v", plain, err)
	}
	again, changed, err := Reseal(testKey, testKey, testLabel, migrated)
	if err != nil || changed || again != migrated {
		t.Fatalf("second Reseal must be a no-op: changed=%v err=%v", changed, err)
	}
}

func TestResealRotatesToNewKeyIdempotently(t *testing.T) {
	underOld, err := Seal(testKey, testLabel, "token")
	if err != nil {
		t.Fatal(err)
	}
	rotated, changed, err := Reseal(testKey, testKey2, testLabel, underOld)
	if err != nil || !changed {
		t.Fatalf("rotate: changed=%v err=%v", changed, err)
	}
	if plain, err := Open(testKey2, testLabel, rotated); err != nil || plain != "token" {
		t.Fatalf("rotated value under new key: %q, %v", plain, err)
	}
	if _, changed, err := Reseal(testKey, testKey2, testLabel, rotated); err != nil || changed {
		t.Fatalf("rerun after rotation must skip: changed=%v err=%v", changed, err)
	}
	// Legacy rows are rotated straight onto the new key as well.
	legacy := legacyZeroKeySeal(t, "old")
	fromLegacy, changed, err := Reseal(testKey, testKey2, testLabel, legacy)
	if err != nil || !changed {
		t.Fatalf("rotate legacy: changed=%v err=%v", changed, err)
	}
	if plain, err := Open(testKey2, testLabel, fromLegacy); err != nil || plain != "old" {
		t.Fatalf("rotated legacy value: %q, %v", plain, err)
	}
	if _, _, err := Reseal(testKey, testKey2, testLabel, "v1:not-base64!"); err == nil {
		t.Fatal("an undecryptable value must be reported, not silently rewritten")
	}
	if empty, changed, err := Reseal(testKey, testKey2, testLabel, ""); err != nil || changed || empty != "" {
		t.Fatalf("empty value must be left alone: %q changed=%v err=%v", empty, changed, err)
	}
}

func TestAppKeyReadsConfig(t *testing.T) {
	previous := config.Get().App.EncryptKey
	t.Cleanup(func() { config.Update(func(cfg *config.AppConfig) { cfg.App.EncryptKey = previous }) })
	config.Update(func(cfg *config.AppConfig) { cfg.App.EncryptKey = "  " + testKey + " " })
	if AppKey() != testKey {
		t.Fatalf("AppKey() did not return the trimmed config key")
	}
}

func TestRedactRemovesSecretFieldsAndAggregates(t *testing.T) {
	rows := []orm.RecordMap{
		{"id": 1, "name": "a", "password": "v1:x", "password:sum": 0.0, "client_secret": "v1:y"},
		{"id": 2, "name": "b"},
	}
	Redact(rows, "password", "client_secret")
	for _, row := range rows {
		for _, key := range []string{"password", "password:sum", "client_secret"} {
			if _, ok := row[key]; ok {
				t.Fatalf("row %v still carries %s", row["id"], key)
			}
		}
	}
	if rows[0]["name"] != "a" {
		t.Fatal("non-secret fields must be kept")
	}
	single := orm.RecordMap{"password_hint": "keep", "password": "v1:x"}
	Redact(single, "password")
	if _, ok := single["password"]; ok || single["password_hint"] != "keep" {
		t.Fatalf("single record redaction wrong: %v", single)
	}
}

func TestRedactedReadMethodsKeepDefaultFlags(t *testing.T) {
	methods := RedactedReadMethods("password")
	if len(methods) != len(redactedMethodNames)+1 {
		t.Fatalf("got %d methods, want %d", len(methods), len(redactedMethodNames)+1)
	}
	for _, method := range methods {
		base, _ := orm.LookupRuntimeMethod(method.Name)
		if method.Public != base.Public || method.ReadOnly != base.ReadOnly || method.UsesPathID != base.UsesPathID || method.ModelOnly != base.ModelOnly {
			t.Fatalf("%s: wrapper flags differ from the default method", method.Name)
		}
		if method.Call == nil {
			t.Fatalf("%s: wrapper has no call", method.Name)
		}
	}
}

// find_or_create would store a secret field unencrypted and return the stored value, so any
// mention of a secret field in its domain or values is refused before the default runs.
func TestFindOrCreateRefusesSecretFields(t *testing.T) {
	var guard orm.RuntimeMethod
	for _, method := range RedactedReadMethods("password") {
		if method.Name == "find_or_create" {
			guard = method
		}
	}
	if guard.Call == nil {
		t.Fatal("find_or_create guard missing")
	}
	cases := []struct {
		args   []any
		kwargs map[string]any
	}{
		{kwargs: map[string]any{"domain": []any{[]any{"name", "=", "x"}}, "values": map[string]any{"password": "plain"}}},
		{kwargs: map[string]any{"domain": []any{[]any{"password", "=", "v1:abc"}}}},
		{args: []any{[]any{[]any{"name", "=", "x"}}, orm.RecordMap{"password": "plain"}}},
	}
	for index, tc := range cases {
		_, err := guard.Call(context.Background(), nil, nil, tc.args, tc.kwargs)
		var typed *orm.Error
		if !errors.As(err, &typed) || typed.Code != orm.ErrInvalidParams {
			t.Fatalf("case %d: err = %v, want invalid params", index, err)
		}
	}
}
