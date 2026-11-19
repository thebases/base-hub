package models

import (
	"context"
	"testing"
	"time"
)

func TestFTSCheckFreshCachesPositiveForeverNegativeForTTL(t *testing.T) {
	now := time.Now()
	if !(ftsCheck{ready: true, checkedAt: now.Add(-24 * time.Hour)}).fresh(now) {
		t.Fatal("positive FTS result must not expire")
	}
	if !(ftsCheck{ready: false, checkedAt: now.Add(-ftsNegativeTTL / 2)}).fresh(now) {
		t.Fatal("negative FTS result inside TTL must be served from cache")
	}
	if (ftsCheck{ready: false, checkedAt: now.Add(-ftsNegativeTTL - time.Second)}).fresh(now) {
		t.Fatal("negative FTS result past TTL must be re-checked")
	}
}

func TestEnsureSearchReadyRechecksExpiredNegative(t *testing.T) {
	const dbName = "fts-negative-ttl"
	stale := time.Now().Add(-2 * ftsNegativeTTL)
	ftsStateMu.Lock()
	ftsState[dbName] = ftsCheck{ready: false, checkedAt: stale}
	ftsStateMu.Unlock()

	if EnsureSearchReady(t.Context(), nil, dbName) {
		t.Fatal("nil db must report FTS unavailable")
	}
	ftsStateMu.RLock()
	got := ftsState[dbName]
	ftsStateMu.RUnlock()
	if !got.checkedAt.After(stale) {
		t.Fatalf("expired negative was served from cache instead of re-checked: %+v", got)
	}
}

func TestEnsureSearchReadyDoesNotCacheCancelledCaller(t *testing.T) {
	const dbName = "fts-cancelled"
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	EnsureSearchReady(ctx, nil, dbName)
	ftsStateMu.RLock()
	_, cached := ftsState[dbName]
	ftsStateMu.RUnlock()
	if cached {
		t.Fatal("a failure caused by the caller's cancelled ctx must not be cached")
	}
}
