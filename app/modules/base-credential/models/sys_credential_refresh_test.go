package models

import (
	"testing"
	"time"

	"git.thebase.vn/base_hub/internal/orm"
)

func TestNextLoginAttemptAtBacksOffExponentiallyAndCaps(t *testing.T) {
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name     string
		previous *SysCredentialSecrets
		want     time.Duration
	}{
		{"unknown previous", nil, loginRetryBaseDelay},
		{"previous success", &SysCredentialSecrets{LastLoginStatus: "ok", LastLoginAt: now.Add(-time.Hour)}, loginRetryBaseDelay},
		{"second failure doubles", &SysCredentialSecrets{LastLoginStatus: "error", LastLoginAt: now.Add(-2 * time.Minute), NextAttemptAt: now.Add(-time.Minute)}, 2 * time.Minute},
		{"capped", &SysCredentialSecrets{LastLoginStatus: "error", LastLoginAt: now.Add(-5 * time.Hour), NextAttemptAt: now.Add(-time.Hour)}, loginRetryMaxDelay},
		{"inverted bookkeeping falls back to base", &SysCredentialSecrets{LastLoginStatus: "error", LastLoginAt: now, NextAttemptAt: now.Add(-time.Hour)}, loginRetryBaseDelay},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := nextLoginAttemptAt(tc.previous, now).Sub(now); got != tc.want {
				t.Fatalf("delay = %v, want %v", got, tc.want)
			}
		})
	}
}

// next_attempt_at is cron bookkeeping: a client create/write must not be able to clear a backoff.
func TestNormalizeSysCredentialValuesStripsBookkeeping(t *testing.T) {
	normalized, err := normalizeSysCredentialValues(orm.RecordMap{"description": "x", "next_attempt_at": nil}, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := normalized["next_attempt_at"]; ok {
		t.Fatal("next_attempt_at must be stripped from client writes")
	}
	if normalized["description"] != "x" {
		t.Fatalf("description lost: %v", normalized)
	}
}

func TestDecryptSysCredentialRowReadsBackoffState(t *testing.T) {
	at := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	secrets, err := decryptSysCredentialRow(orm.RecordMap{
		"id": int64(3), "last_login_status": "error", "last_login_at": at, "next_attempt_at": at.Add(time.Minute).Format(time.RFC3339),
	})
	if err != nil {
		t.Fatal(err)
	}
	if secrets.LastLoginStatus != "error" || !secrets.LastLoginAt.Equal(at) || !secrets.NextAttemptAt.Equal(at.Add(time.Minute)) {
		t.Fatalf("backoff state not read: %+v", secrets)
	}
}
