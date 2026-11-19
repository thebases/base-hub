package routes

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/uptrace/bun"
)

// stubDocsSync swaps the sync body for the test and restores it afterwards.
// Not parallel: it replaces a package-level function.
func stubDocsSync(t *testing.T, fn func(context.Context, *bun.DB, MarkdownRouteOptions, string) bool) {
	t.Helper()
	previous := syncFilesystemDocsToDBFn
	syncFilesystemDocsToDBFn = fn
	t.Cleanup(func() { syncFilesystemDocsToDBFn = previous })
}

func TestSyncFilesystemDocsToDBOnceRetriesAfterFailure(t *testing.T) {
	var runs atomic.Int32
	results := []bool{false, true}
	stubDocsSync(t, func(context.Context, *bun.DB, MarkdownRouteOptions, string) bool {
		return results[runs.Add(1)-1]
	})
	const dbName = "sync-retry"
	for range 3 {
		syncFilesystemDocsToDBOnce(t.Context(), nil, MarkdownRouteOptions{}, dbName)
	}
	if got := runs.Load(); got != 2 {
		t.Fatalf("sync ran %d times, want 2 (failed pass retried, then marked done)", got)
	}
}

func TestSyncFilesystemDocsToDBOnceConcurrentCallersRunOnce(t *testing.T) {
	var runs atomic.Int32
	stubDocsSync(t, func(context.Context, *bun.DB, MarkdownRouteOptions, string) bool {
		runs.Add(1)
		time.Sleep(20 * time.Millisecond)
		return true
	})
	const dbName = "sync-concurrent"
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() { syncFilesystemDocsToDBOnce(t.Context(), nil, MarkdownRouteOptions{}, dbName) })
	}
	wg.Wait()
	if got := runs.Load(); got != 1 {
		t.Fatalf("sync ran %d times for concurrent first callers, want 1", got)
	}
}

func TestEnsureMarkdownMenuSnapshotSkipsUnchangedWrite(t *testing.T) {
	root := t.TempDir()
	mustWriteMarkdownFile(t, root, "docs/api.md", "# API\n")
	if _, err := ensureMarkdownMenuSnapshot(root); err != nil {
		t.Fatalf("first ensureMarkdownMenuSnapshot: %v", err)
	}
	menuPath := filepath.Join(root, markdownMenuFileName)
	old := time.Now().Add(-time.Hour).Truncate(time.Second)
	if err := os.Chtimes(menuPath, old, old); err != nil {
		t.Fatal(err)
	}
	if _, err := ensureMarkdownMenuSnapshot(root); err != nil {
		t.Fatalf("second ensureMarkdownMenuSnapshot: %v", err)
	}
	info, err := os.Stat(menuPath)
	if err != nil {
		t.Fatal(err)
	}
	if !info.ModTime().Equal(old) {
		t.Fatalf("menu.json rewritten although content was unchanged (mtime %v, want %v)", info.ModTime(), old)
	}
}

func TestMarkdownMenuSnapshotConcurrentListAndLabelKeepsLabel(t *testing.T) {
	root := t.TempDir()
	mustWriteMarkdownFile(t, root, "docs/a.md", "# A\n")
	payload, err := ensureMarkdownMenuSnapshot(root)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() { _, _ = ensureMarkdownMenuSnapshot(root) })
	}
	if err := applyMarkdownMenuLabel(root, payload, "docs/a.md", "Label A"); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	persisted, err := readMarkdownMenuSnapshot(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(persisted.Items) != 1 || persisted.Items[0].Name != "Label A" {
		t.Fatalf("label lost under concurrent list refresh: %+v", persisted.Items)
	}
}
