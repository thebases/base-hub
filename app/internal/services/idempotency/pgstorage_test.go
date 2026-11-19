package idempotency

// File overview:
// - Responsibility: verifies the fiber.Storage contract of the PostgreSQL-backed idempotency
//   adapter against a real fixture database.
// - Key symbols: focused Test cases plus a storage fixture helper.
// - Pattern: Locks down the miss contract (nil,nil), expiry visibility, and scheduled GC behavior
//   so middleware semantics survive refactors.

import (
	"testing"
	"time"

	"git.thebase.vn/base_hub/internal/orm"
	testutil "git.thebase.vn/base_hub/internal/test"
)

// setupPGStorage opens the fixture DB and returns a storage with an empty table.
func setupPGStorage(t *testing.T) *PGStorage {
	t.Helper()

	bunDB, err := orm.NewBunDB(testutil.EnvPostgresConfig())
	if err != nil {
		t.Skipf("open postgres fixture: %v", err)
	}
	t.Cleanup(func() { _ = bunDB.Close() })

	store, err := NewPGStorage(t.Context(), bunDB.DB)
	if err != nil {
		t.Fatalf("NewPGStorage: %v", err)
	}
	if err := store.Reset(); err != nil {
		t.Fatalf("reset storage: %v", err)
	}
	return store
}

func TestPGStorageSetGetRoundTrip(t *testing.T) {
	store := setupPGStorage(t)

	if err := store.Set("key-a", []byte("value-a"), time.Hour); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := store.Get("key-a")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(got) != "value-a" {
		t.Fatalf("Get = %q, want value-a", got)
	}

	// Upsert replaces the previous value for the same key.
	if err := store.Set("key-a", []byte("value-b"), time.Hour); err != nil {
		t.Fatalf("Set overwrite: %v", err)
	}
	got, err = store.Get("key-a")
	if err != nil || string(got) != "value-b" {
		t.Fatalf("Get after overwrite = (%q, %v), want value-b", got, err)
	}
}

func TestPGStorageMissAndExpiryContract(t *testing.T) {
	store := setupPGStorage(t)

	// Miss contract: nil, nil — the middleware treats any error as fatal.
	got, err := store.Get("absent")
	if err != nil || got != nil {
		t.Fatalf("Get(absent) = (%v, %v), want (nil, nil)", got, err)
	}

	// An already-expired entry is invisible to Get even before GC runs.
	if err := store.Set("expired", []byte("stale"), time.Nanosecond); err != nil {
		t.Fatalf("Set expired: %v", err)
	}
	time.Sleep(5 * time.Millisecond)
	got, err = store.Get("expired")
	if err != nil || got != nil {
		t.Fatalf("Get(expired) = (%v, %v), want (nil, nil)", got, err)
	}

	// exp == 0 means no expiration: the entry survives the scheduled GC.
	if err := store.Set("forever", []byte("keep"), 0); err != nil {
		t.Fatalf("Set forever: %v", err)
	}
	deleted, err := store.DeleteExpired(t.Context())
	if err != nil {
		t.Fatalf("DeleteExpired: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("DeleteExpired removed %d rows, want 1 (only the expired entry)", deleted)
	}
	got, err = store.Get("forever")
	if err != nil || string(got) != "keep" {
		t.Fatalf("Get(forever) after GC = (%q, %v), want keep", got, err)
	}
}

func TestPGStorageDeleteAndReset(t *testing.T) {
	store := setupPGStorage(t)

	if err := store.Set("key-a", []byte("v"), time.Hour); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := store.Delete("key-a"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	// Deleting a missing key is not an error per the fiber contract.
	if err := store.Delete("key-a"); err != nil {
		t.Fatalf("Delete(missing): %v", err)
	}
	if got, err := store.Get("key-a"); err != nil || got != nil {
		t.Fatalf("Get after delete = (%v, %v), want (nil, nil)", got, err)
	}

	if err := store.Set("key-b", []byte("v"), time.Hour); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := store.Reset(); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if got, err := store.Get("key-b"); err != nil || got != nil {
		t.Fatalf("Get after reset = (%v, %v), want (nil, nil)", got, err)
	}

	// Empty keys and values are ignored without error per the fiber contract.
	if err := store.Set("", []byte("v"), 0); err != nil {
		t.Fatalf("Set empty key: %v", err)
	}
	if err := store.Set("key-c", nil, 0); err != nil {
		t.Fatalf("Set empty value: %v", err)
	}
	if got, err := store.Get(""); err != nil || got != nil {
		t.Fatalf("Get empty key = (%v, %v), want (nil, nil)", got, err)
	}
}
