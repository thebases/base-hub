package routes

// File overview:
// - Responsibility: SearchDocuments -- ranks knowledge documents by content, not just
//   filename (unlike FindDocuments' substring-on-name check). Prefers Postgres full-text
//   search (modules/knowledge/models) when the tenant DB supports it; falls back to a
//   filesystem JSON index (rebuilt from the same doc tree) when it doesn't, e.g. a
//   locked-down Postgres without CREATE INDEX privilege.
// - Key symbols: `SearchDocuments`, `syncFilesystemDocsToDBOnce`.
// - Pattern: both backends satisfy the exact same signature/return shape so callers
//   (KernelDocumentProvider, base-mcp) stay agnostic to which one actually served a query.

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
	knowledgemodels "git.thebase.vn/base_hub/modules/knowledge/models"
	"github.com/uptrace/bun"
)

// knowledgeIndexTimeout bounds index reads and writes on top of the caller ctx,
// so a stuck DB must not hold a request forever.
const knowledgeIndexTimeout = 15 * time.Second

var errQueryRequired = errors.New("query is required")

// docsSyncState guards the one-time filesystem->DB sync for one dbName. mu is
// held for the whole sync so concurrent first callers wait for it instead of
// each walking the tree; done flips only after a sync that fully succeeded,
// so a failed or timed-out pass is retried by the next search.
type docsSyncState struct {
	mu   sync.Mutex
	done bool
}

var (
	docsSyncMu sync.Mutex
	// docsSync holds, per dbName, the sync state for the filesystem doc tree
	// into the Postgres search index -- a one-time pass per process, not per
	// request.
	docsSync = map[string]*docsSyncState{}

	// syncFilesystemDocsToDBFn is the sync body; a package var so tests can
	// count runs and force failures without a Postgres connection.
	syncFilesystemDocsToDBFn = syncFilesystemDocsToDB
)

// SearchDocuments ranks documents matching query by content (falls back to
// name/filename when the query doesn't hit any content). Tries Postgres FTS
// first; a database where that isn't usable transparently gets the
// filesystem-index fallback instead.
func SearchDocuments(ctx context.Context, o MarkdownRouteOptions, dbName, query string) ([]markdownMenuItem, error) {
	trimmedQuery := strings.TrimSpace(query)
	if trimmedQuery == "" {
		return nil, errQueryRequired
	}

	ctx, cancel := context.WithTimeout(ctx, knowledgeIndexTimeout)
	defer cancel()
	if bunDB := orm.ResolveRegisteredDatabase(dbName); bunDB != nil && bunDB.DB != nil {
		if knowledgemodels.EnsureSearchReady(ctx, bunDB.DB, dbName) {
			syncFilesystemDocsToDBOnce(ctx, bunDB.DB, o, dbName)
			rows, err := knowledgemodels.SearchDocuments(ctx, bunDB.DB, trimmedQuery, 20)
			if err != nil {
				log.Warnf("knowledge: postgres FTS search failed on %q, falling back to filesystem index: %v", dbName, err)
			} else {
				return documentsToMenuItems(rows), nil
			}
		}
	}

	return searchFilesystemIndex(o, dbName, trimmedQuery)
}

func documentsToMenuItems(rows []knowledgemodels.Document) []markdownMenuItem {
	items := make([]markdownMenuItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, markdownMenuItem{
			Name:         row.Name,
			RelativePath: row.RelativePath,
			Source:       row.Source,
			ReadOnly:     row.ReadOnly,
		})
	}
	return items
}

// syncFilesystemDocsToDBOnce walks the doc tree into the Postgres search
// index once per (process, dbName) -- guarded so steady-state search calls
// don't re-walk the filesystem on every request.
func syncFilesystemDocsToDBOnce(ctx context.Context, db *bun.DB, o MarkdownRouteOptions, dbName string) {
	state := docsSyncStateFor(dbName)
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.done {
		return
	}
	state.done = syncFilesystemDocsToDBFn(ctx, db, o, dbName)
}

func docsSyncStateFor(dbName string) *docsSyncState {
	docsSyncMu.Lock()
	defer docsSyncMu.Unlock()
	state, ok := docsSync[dbName]
	if !ok {
		state = &docsSyncState{}
		docsSync[dbName] = state
	}
	return state
}

// syncFilesystemDocsToDB upserts every listed document and reports whether the
// pass is complete. A document that cannot be read (removed mid-walk) does not
// fail the pass; a DB upsert error does, so the sync is retried later rather
// than leaving the index permanently partial.
func syncFilesystemDocsToDB(ctx context.Context, db *bun.DB, o MarkdownRouteOptions, dbName string) bool {
	menu, err := ListDocuments(o, dbName)
	if err != nil {
		log.Warnf("knowledge: list documents for search index sync on %q: %v", dbName, err)
		return false
	}
	ok := true
	for _, item := range menu.Items {
		content, err := ReadDocument(o, dbName, item.RelativePath)
		if err != nil {
			log.Warnf("knowledge: read document %q for search index sync on %q: %v", item.RelativePath, dbName, err)
			continue
		}
		doc := knowledgemodels.Document{
			RelativePath: item.RelativePath,
			Name:         item.Name,
			Source:       item.Source,
			ReadOnly:     item.ReadOnly,
			Content:      string(content),
		}
		if err := knowledgemodels.UpsertDocument(ctx, db, doc); err != nil {
			log.Warnf("knowledge: index document %q on %q: %v", item.RelativePath, dbName, err)
			ok = false
		}
	}
	return ok
}

// reindexDocument keeps the search index (whichever backend is active) in
// sync with one document's content immediately after a create/update/rename,
// instead of waiting for the next process-lifetime full sync. Looks the item
// up via ListDocuments so the indexed name/source/readonly metadata always
// matches what the menu already computed, rather than re-deriving it here.
func reindexDocument(ctx context.Context, o MarkdownRouteOptions, dbName, relativePath string) {
	menu, err := ListDocuments(o, dbName)
	if err != nil {
		log.Warnf("knowledge: list documents to reindex %q on %q: %v", relativePath, dbName, err)
		return
	}
	var item *markdownMenuItem
	for i := range menu.Items {
		if menu.Items[i].RelativePath == relativePath {
			item = &menu.Items[i]
			break
		}
	}
	if item == nil {
		return
	}
	content, err := ReadDocument(o, dbName, relativePath)
	if err != nil {
		log.Warnf("knowledge: read document %q to reindex on %q: %v", relativePath, dbName, err)
		return
	}
	ctx, cancel := context.WithTimeout(ctx, knowledgeIndexTimeout)
	defer cancel()
	if bunDB := orm.ResolveRegisteredDatabase(dbName); bunDB != nil && bunDB.DB != nil && knowledgemodels.FTSAvailable(dbName) {
		doc := knowledgemodels.Document{
			RelativePath: relativePath,
			Name:         item.Name,
			Source:       item.Source,
			ReadOnly:     item.ReadOnly,
			Content:      string(content),
		}
		if err := knowledgemodels.UpsertDocument(ctx, bunDB.DB, doc); err != nil {
			log.Warnf("knowledge: reindex document %q on %q: %v", relativePath, dbName, err)
		}
		return
	}
	rebuildFilesystemIndex(o, dbName)
}

// removeIndexedDocument keeps the search index in sync with a deletion.
func removeIndexedDocument(ctx context.Context, o MarkdownRouteOptions, dbName, relativePath string) {
	ctx, cancel := context.WithTimeout(ctx, knowledgeIndexTimeout)
	defer cancel()
	if bunDB := orm.ResolveRegisteredDatabase(dbName); bunDB != nil && bunDB.DB != nil && knowledgemodels.FTSAvailable(dbName) {
		if err := knowledgemodels.DeleteDocument(ctx, bunDB.DB, relativePath); err != nil {
			log.Warnf("knowledge: remove indexed document %q on %q: %v", relativePath, dbName, err)
		}
		return
	}
	rebuildFilesystemIndex(o, dbName)
}
