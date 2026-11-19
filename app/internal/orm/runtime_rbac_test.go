package orm

// File overview:
// - Responsibility: verifies ORM runtime semantics, repository behavior, and registry coordination for runtime rbac.
// - Key symbols: focused Test cases plus file-local fixtures and assertion helpers.
// - Pattern: Locks down runtime invariants so refactors preserve transaction, RBAC, registry, and storage behavior.

// These tests lock in RBAC behavior so permission checks remain predictable as
// more runtime methods and roles are introduced.

import (
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestParseRBACGroupID(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want int64
	}{
		{
			name: "blank role id",
			raw:  "",
			want: 0,
		},
		{
			name: "whitespace role id",
			raw:  "   ",
			want: 0,
		},
		{
			name: "numeric role id",
			raw:  "42",
			want: 42,
		},
		{
			name: "trimmed numeric role id",
			raw:  " 7 ",
			want: 7,
		},
		{
			name: "invalid legacy role id",
			raw:  "not-a-number",
			want: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseRBACGroupID(tt.raw); got != tt.want {
				t.Fatalf("parseRBACGroupID(%q) = %d, want %d", tt.raw, got, tt.want)
			}
		})
	}
}

func TestIdentityFromRBACUserUsesNormalizedRoleID(t *testing.T) {
	user := &rbacUserRow{
		ID:         11,
		Login:      "operator",
		GroupIDRaw: " 9 ",
	}

	identity := identityFromRBACUser(user, "bearer")
	if identity == nil {
		t.Fatalf("expected identity to be created")
	}
	if identity.RoleID != 9 {
		t.Fatalf("expected normalized role id 9, got %d", identity.RoleID)
	}
}

func TestMatchStoredPasswordDetailFlagsLegacyBranches(t *testing.T) {
	// SEC-02: the legacy flag drives the login-time bcrypt re-hash, so a bcrypt
	// match must never report legacy and a plaintext/SHA-1 match always must.
	bcryptHash, err := bcrypt.GenerateFromPassword([]byte("admin"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("GenerateFromPassword() error = %v", err)
	}

	tests := []struct {
		name        string
		stored      string
		candidate   string
		wantMatched bool
		wantLegacy  bool
	}{
		{
			name:        "bcrypt match is not legacy",
			stored:      string(bcryptHash),
			candidate:   "admin",
			wantMatched: true,
			wantLegacy:  false,
		},
		{
			name:        "bcrypt mismatch reports neither",
			stored:      string(bcryptHash),
			candidate:   "wrong",
			wantMatched: false,
			wantLegacy:  false,
		},
		{
			name:        "plaintext match is legacy",
			stored:      "admin",
			candidate:   "admin",
			wantMatched: true,
			wantLegacy:  true,
		},
		{
			name:        "sha1 match is legacy",
			stored:      "d033e22ae348aeb5660fc2140aec35850c4da997",
			candidate:   "admin",
			wantMatched: true,
			wantLegacy:  true,
		},
		{
			name:        "uppercase sha1 still matches",
			stored:      "D033E22AE348AEB5660FC2140AEC35850C4DA997",
			candidate:   "admin",
			wantMatched: true,
			wantLegacy:  true,
		},
		{
			name:        "mismatch reports neither",
			stored:      "admin",
			candidate:   "wrong",
			wantMatched: false,
			wantLegacy:  false,
		},
		{
			name:        "empty stored reports neither",
			stored:      "",
			candidate:   "admin",
			wantMatched: false,
			wantLegacy:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matched, legacy := matchStoredPasswordDetail(tt.stored, tt.candidate)
			if matched != tt.wantMatched || legacy != tt.wantLegacy {
				t.Fatalf("matchStoredPasswordDetail(%q, %q) = (%v, %v), want (%v, %v)",
					tt.stored, tt.candidate, matched, legacy, tt.wantMatched, tt.wantLegacy)
			}
		})
	}
}

// Plan T36 / SEC-T-19: the timing equalizer compares against a real bcrypt hash at the default
// cost, so an unknown login costs the same as a wrong password.
func TestEqualizePasswordCheckTimingUsesDefaultCost(t *testing.T) {
	cost, err := bcrypt.Cost(dummyPasswordHash())
	if err != nil || cost != bcrypt.DefaultCost {
		t.Fatalf("dummy hash cost = %d, %v; want %d", cost, err, bcrypt.DefaultCost)
	}
	EqualizePasswordCheckTiming("anything")
}
