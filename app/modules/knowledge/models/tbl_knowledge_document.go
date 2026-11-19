package models

// File overview:
// - Responsibility: Postgres-backed full-text search index for knowledge documents, so
//   base-mcp's knowledge lookups can rank real content matches instead of the old
//   filename-substring check. Mirrors internal/services/idempotency/pgstorage.go's
//   "ensure table exists on first use" pattern -- this is an internal search index, not a
//   user-facing business model, so it deliberately skips the generic orm.RegisterModelType
//   registry (no RBAC/sys.model catalog exposure needed for a cache table).
// - Key symbols: `EnsureSearchReady`, `SearchDocuments`, `UpsertDocument`, `FTSAvailable`.
// - Pattern: FTS availability is determined lazily per database (GIN index creation is
//   cached once it succeeds); a database where that fails (e.g. a locked-down/managed
//   Postgres without CREATE INDEX privilege) is recorded as FTS-unavailable for
//   ftsNegativeTTL so callers fall back to a filesystem-based index, then re-checked.

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/uptrace/bun"
)

const knowledgeDocumentTable = "tbl_knowledge_document"

// Document is the DB row shape for one indexed knowledge document.
type Document struct {
	bun.BaseModel `bun:"table:tbl_knowledge_document"`

	ID           int64  `bun:"id,pk,autoincrement"`
	RelativePath string `bun:"relative_path,notnull,unique"`
	Name         string `bun:"name,notnull"`
	Source       string `bun:"source,notnull"`
	ReadOnly     bool   `bun:"read_only,notnull,default:false"`
	Content      string `bun:"content,notnull,default:''"`
}

// ftsNegativeTTL bounds how long an "FTS unavailable" result is trusted. A
// false can be transient (DB restarting, lock timeout, request deadline), so
// it must not pin a tenant to the filesystem fallback for the whole process
// lifetime; a true never needs re-checking (the DDL is idempotent).
const ftsNegativeTTL = time.Minute

// ftsCheck is one cached EnsureSearchReady outcome.
type ftsCheck struct {
	ready     bool
	checkedAt time.Time
}

// fresh reports whether the cached outcome can still be served at now.
func (c ftsCheck) fresh(now time.Time) bool {
	return c.ready || now.Sub(c.checkedAt) < ftsNegativeTTL
}

var (
	ftsStateMu sync.RWMutex
	// ftsState caches, per dbName, whether Postgres FTS (table + GIN index) is
	// usable. Populated lazily by EnsureSearchReady; negatives expire after
	// ftsNegativeTTL.
	ftsState = map[string]ftsCheck{}
)

// FTSAvailable reports the cached FTS-readiness state for dbName. Call
// EnsureSearchReady first; this only reads the cache.
func FTSAvailable(dbName string) bool {
	ftsStateMu.RLock()
	defer ftsStateMu.RUnlock()
	return ftsState[strings.TrimSpace(dbName)].ready
}

// EnsureSearchReady creates the tbl_knowledge_document table and its GIN trigram
// index if missing, and records whether Postgres FTS is usable on this
// database. Safe to call repeatedly -- table/index creation is idempotent. A
// positive result is cached for the process lifetime; a negative one only for
// ftsNegativeTTL, and not at all when the caller's ctx ended (that failure says
// nothing about the database).
func EnsureSearchReady(ctx context.Context, db *bun.DB, dbName string) bool {
	dbName = strings.TrimSpace(dbName)
	ftsStateMu.RLock()
	cached, checked := ftsState[dbName]
	ftsStateMu.RUnlock()
	if checked && cached.fresh(time.Now()) {
		return cached.ready
	}

	ready := ensureSearchTableAndIndex(ctx, db)
	if !ready && ctx.Err() != nil {
		return false
	}
	ftsStateMu.Lock()
	ftsState[dbName] = ftsCheck{ready: ready, checkedAt: time.Now()}
	ftsStateMu.Unlock()
	return ready
}

func ensureSearchTableAndIndex(ctx context.Context, db *bun.DB) bool {
	if db == nil {
		return false
	}
	if _, err := db.NewRaw(fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s (
			id            bigserial PRIMARY KEY,
			relative_path text NOT NULL UNIQUE,
			name          text NOT NULL,
			source        text NOT NULL,
			read_only     boolean NOT NULL DEFAULT false,
			content       text NOT NULL DEFAULT '',
			content_tsv   tsvector,
			created_at    timestamptz NOT NULL DEFAULT now(),
			updated_at    timestamptz NOT NULL DEFAULT now()
		)
	`, knowledgeDocumentTable)).Exec(ctx); err != nil {
		return false
	}
	if _, err := db.NewRaw(fmt.Sprintf(
		`CREATE INDEX IF NOT EXISTS tbl_knowledge_document_content_tsv_idx ON %s USING GIN (content_tsv)`,
		knowledgeDocumentTable,
	)).Exec(ctx); err != nil {
		return false
	}
	return true
}

// UpsertDocument writes one document's content into the search index,
// recomputing its tsvector in the same statement. Call only after
// EnsureSearchReady(ctx, db, dbName) returned true.
func UpsertDocument(ctx context.Context, db *bun.DB, doc Document) error {
	_, err := db.NewRaw(fmt.Sprintf(`
		INSERT INTO %s (relative_path, name, source, read_only, content, content_tsv, updated_at)
		VALUES (?, ?, ?, ?, ?, to_tsvector('simple', ?), now())
		ON CONFLICT (relative_path) DO UPDATE SET
			name = EXCLUDED.name,
			source = EXCLUDED.source,
			read_only = EXCLUDED.read_only,
			content = EXCLUDED.content,
			content_tsv = EXCLUDED.content_tsv,
			updated_at = now()
	`, knowledgeDocumentTable),
		doc.RelativePath, doc.Name, doc.Source, doc.ReadOnly, doc.Content, doc.Content,
	).Exec(ctx)
	return err
}

// DeleteDocument removes one document from the search index by its logical
// path. A no-op (not an error) if the row was never indexed.
func DeleteDocument(ctx context.Context, db *bun.DB, relativePath string) error {
	_, err := db.NewRaw(fmt.Sprintf(`DELETE FROM %s WHERE relative_path = ?`, knowledgeDocumentTable), relativePath).Exec(ctx)
	return err
}

// SearchDocuments ranks documents whose content matches query via Postgres
// full-text search. Call only after EnsureSearchReady(ctx, db, dbName)
// returned true.
func SearchDocuments(ctx context.Context, db *bun.DB, query string, limit int) ([]Document, error) {
	if limit <= 0 {
		limit = 20
	}
	var rows []Document
	err := db.NewRaw(fmt.Sprintf(`
		SELECT id, relative_path, name, source, read_only, content
		FROM %s
		WHERE content_tsv @@ plainto_tsquery('simple', ?)
		ORDER BY ts_rank(content_tsv, plainto_tsquery('simple', ?)) DESC
		LIMIT ?
	`, knowledgeDocumentTable), query, query, limit).Scan(ctx, &rows)
	if err != nil {
		return nil, err
	}
	return rows, nil
}
