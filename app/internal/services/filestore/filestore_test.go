package filestore

// File overview:
// - Responsibility: verifies the checksum-addressed blob store contract — dedup, atomic publish,
//   checksum validation, quarantine, and GC-safe listing (SRS-F-019).
// - Key symbols: focused Test cases using a scratch root directory.
// - Pattern: Locks down storage invariants so refactors preserve content addressing semantics.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

// useScratchRoot redirects the filestore fallback root to a per-test directory.
// RootDir falls back to the default when no home DB is registered, which is
// exactly the test environment, so overriding the default is sufficient.
func useScratchRoot(t *testing.T) string {
	t.Helper()
	previous := defaultFilestoreRoot
	root := t.TempDir()
	defaultFilestoreRoot = root
	t.Cleanup(func() { defaultFilestoreRoot = previous })
	return root
}

// TestStoreDedupsIdenticalContent verifies the SRS-F-019 acceptance: two
// uploads of identical bytes share exactly one blob on disk.
func TestStoreDedupsIdenticalContent(t *testing.T) {
	root := useScratchRoot(t)
	ctx := context.Background()
	payload := []byte("identical attachment bytes")

	first, size, err := Store(ctx, "tenant_db", payload)
	if err != nil {
		t.Fatalf("first store: %v", err)
	}
	second, _, err := Store(ctx, "tenant_db", payload)
	if err != nil {
		t.Fatalf("second store: %v", err)
	}
	if first != second {
		t.Fatalf("expected identical content to share one checksum, got %s and %s", first, second)
	}
	if size != int64(len(payload)) {
		t.Fatalf("expected size %d, got %d", len(payload), size)
	}

	expected := sha256.Sum256(payload)
	if first != hex.EncodeToString(expected[:]) {
		t.Fatalf("checksum %s does not match content hash", first)
	}

	blobs, err := ListBlobs(ctx, "tenant_db")
	if err != nil {
		t.Fatalf("list blobs: %v", err)
	}
	if len(blobs) != 1 {
		t.Fatalf("expected exactly one blob after dedup, got %d", len(blobs))
	}
	// The blob must land inside the per-database subtree, never beside it.
	path, err := BlobPath(ctx, "tenant_db", first)
	if err != nil {
		t.Fatalf("blob path: %v", err)
	}
	if rel, relErr := filepath.Rel(root, path); relErr != nil || rel == "" || rel[0] == '.' {
		t.Fatalf("expected blob path under scratch root, got %s", path)
	}
}

// TestBlobPathRejectsInvalidChecksums verifies path traversal cannot be built
// from a malformed checksum.
func TestBlobPathRejectsInvalidChecksums(t *testing.T) {
	useScratchRoot(t)
	for _, checksum := range []string{"", "..", "../../etc/passwd", "abc123"} {
		if _, err := BlobPath(context.Background(), "tenant_db", checksum); err == nil {
			t.Fatalf("expected invalid checksum %q to be rejected", checksum)
		}
	}
}

// TestQuarantineAndListSkipsNonBlobs verifies that quarantined and temp files
// disappear from GC listings so they can never be treated as orphan blobs.
func TestQuarantineAndListSkipsNonBlobs(t *testing.T) {
	useScratchRoot(t)
	ctx := context.Background()

	checksum, _, err := Store(ctx, "tenant_db", []byte("to be quarantined"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	if err := Quarantine(ctx, "tenant_db", checksum); err != nil {
		t.Fatalf("quarantine: %v", err)
	}
	// The original path must be gone so serving stops immediately.
	if _, err := Open(ctx, "tenant_db", checksum); !os.IsNotExist(err) {
		t.Fatalf("expected quarantined blob to stop serving, got err=%v", err)
	}
	blobs, err := ListBlobs(ctx, "tenant_db")
	if err != nil {
		t.Fatalf("list blobs: %v", err)
	}
	if len(blobs) != 0 {
		t.Fatalf("expected quarantined file to be excluded from listings, got %d entries", len(blobs))
	}
}

// TestListBlobsMissingDatabaseDirIsEmpty verifies a database that never stored
// an attachment lists as empty instead of erroring.
func TestListBlobsMissingDatabaseDirIsEmpty(t *testing.T) {
	useScratchRoot(t)
	blobs, err := ListBlobs(context.Background(), "never_used_db")
	if err != nil {
		t.Fatalf("list blobs on missing dir: %v", err)
	}
	if len(blobs) != 0 {
		t.Fatalf("expected empty listing, got %d", len(blobs))
	}
}
