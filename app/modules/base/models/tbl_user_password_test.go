package models

import (
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestNormalizeResUsersPasswordAlwaysHashes(t *testing.T) {
	values := map[string]any{"password": "$2xyz"}
	if err := normalizeResUsersPassword(values); err != nil {
		t.Fatal(err)
	}
	hash, _ := values["password"].(string)
	if hash == "$2xyz" || bcrypt.CompareHashAndPassword([]byte(hash), []byte("$2xyz")) != nil {
		t.Fatalf("password starting with $2 must be hashed, got %q", hash)
	}
}

func TestNormalizeResUsersPasswordRejectsOver72Bytes(t *testing.T) {
	values := map[string]any{"new_password": strings.Repeat("a", 73)}
	if err := normalizeResUsersPassword(values); err == nil {
		t.Fatal("expected error for >72 byte password")
	}
}

func TestNormalizeResUsersPasswordDropsEchoedMask(t *testing.T) {
	values := map[string]any{"password": resUsersPasswordMask, "name": "x"}
	if err := normalizeResUsersPassword(values); err != nil {
		t.Fatal(err)
	}
	if _, ok := values["password"]; ok {
		t.Fatalf("masked password must not be written back: %v", values)
	}
}
