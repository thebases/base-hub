package cron

// File overview:
// - Responsibility: implements the `platform.attachment_gc` scheduler handler (SRS-F-019 rule 4) —
//   deletes filestore blobs no longer referenced by any sys.attachment row on the job's database.
// - Key symbols: `runAttachmentGC`, `referencedAttachmentChecksums`.
// - Pattern: rows own references, blobs are content-addressed with dedup, so unlink never deletes
//   content synchronously; this GC is the single owner of blob deletion.

import (
	"context"
	"fmt"
	"git.thebase.vn/base_hub/internal/orm"
	"strings"
	"time"

	"git.thebase.vn/base_hub/internal/services/filestore"
	"git.thebase.vn/base_hub/internal/services/log"
	"github.com/uptrace/bun"
)

const attachmentGCHandlerKey = "platform.attachment_gc"

// attachmentGCMinBlobAge protects in-flight uploads: a blob is written to the
// filestore BEFORE its row commits, so a young unreferenced blob may simply be
// a create in progress and must never be collected.
const attachmentGCMinBlobAge = time.Hour

// attachmentTable is a var, not a const, so DB-backed tests can redirect the
// reference scan to a scratch table (same convention as the autovacuum tables).
var attachmentTable = "sys_attachment"

func init() {
	// The GC handler is kernel-owned, so it registers at package init; the
	// seeded `platform.attachment_gc` job starts firing once the scheduler runs.
	if err := RegisterHandler(attachmentGCHandlerKey, runAttachmentGC); err != nil {
		log.Errorf("cron: register attachment gc handler: %v", err)
	}
}

// runAttachmentGC removes unreferenced blobs from the job database's filestore
// subtree. Missing table and missing subtree are both no-ops: a database
// without attachments has nothing to collect.
func runAttachmentGC(ctx context.Context, _ map[string]any) error {
	db := orm.DBFromContext(ctx)
	if db == nil {
		log.Errorf("cron: attachment gc requires a database-bound context")
		return fmt.Errorf("cron: attachment gc requires a database-bound context")
	}
	dbName := vacuumDBName(ctx)

	referenced, ok, err := referencedAttachmentChecksums(ctx, db)
	if err != nil {
		return fmt.Errorf("cron: attachment gc scan references on %q: %w", dbName, err)
	}
	if !ok {
		// No sys_attachment table on this database means nothing was ever
		// uploaded through the runtime — never touch the filestore blind.
		return nil
	}

	blobs, err := filestore.ListBlobs(ctx, dbName)
	if err != nil {
		return fmt.Errorf("cron: attachment gc list blobs on %q: %w", dbName, err)
	}

	var removed, kept int64
	cutoff := time.Now().Add(-attachmentGCMinBlobAge)
	for _, blob := range blobs {
		if _, isReferenced := referenced[strings.ToLower(blob.Checksum)]; isReferenced {
			kept++
			continue
		}
		// Age gate: a fresh unreferenced blob may belong to an uncommitted
		// create; only blobs older than the grace window are provably orphans.
		if blob.ModTime.After(cutoff) {
			kept++
			continue
		}
		if err := filestore.Remove(ctx, dbName, blob.Checksum); err != nil {
			log.Warnf("cron: attachment gc remove blob %s on %q: %v", blob.Checksum, dbName, err)
			continue
		}
		removed++
	}
	// Counts are the ops-visible signal that blob GC is actually running.
	log.Infof("cron: attachment gc on %q: removed=%d kept=%d", dbName, removed, kept)
	return nil
}

// referencedAttachmentChecksums returns the set of checksums still referenced
// by sys.attachment rows. The second return reports whether the table exists.
func referencedAttachmentChecksums(ctx context.Context, db bun.IDB) (map[string]struct{}, bool, error) {
	var checksums []string
	err := db.NewRaw(fmt.Sprintf(
		// Archived rows (active = false) still reference their content: archiving
		// hides a record, it must never destroy its data.
		`SELECT DISTINCT checksum FROM %s WHERE checksum IS NOT NULL AND checksum <> ''`,
		attachmentTable,
	)).Scan(ctx, &checksums)
	if err != nil {
		// A database without the attachment table simply has nothing to collect.
		if isMissingRelationError(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	referenced := make(map[string]struct{}, len(checksums))
	for _, checksum := range checksums {
		referenced[strings.ToLower(strings.TrimSpace(checksum))] = struct{}{}
	}
	return referenced, true, nil
}
