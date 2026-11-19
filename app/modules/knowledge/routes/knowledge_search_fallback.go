package routes

// File overview:
// - Responsibility: filesystem-based fallback search index for knowledge documents, used
//   only on a tenant DB where Postgres FTS isn't usable (knowledgemodels.EnsureSearchReady
//   returned false -- e.g. a locked-down/managed Postgres without CREATE INDEX privilege).
//   Ranks by content-substring hits (with a name-match boost) instead of the plain
//   filename-substring check FindDocuments does, so it's a real improvement even without a
//   database backing it.
// - Key symbols: `searchFilesystemIndex`, `rebuildFilesystemIndex`.
// - Pattern: one JSON cache file per tenant, sibling to that tenant's user-docs root, holding
//   full lowercased content so a repeat search doesn't re-read every document from disk.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"git.thebase.vn/base_hub/internal/services/log"
)

type filesystemSearchIndexEntry struct {
	Name             string `json:"name"`
	RelativePath     string `json:"relative_path"`
	Source           string `json:"source"`
	ReadOnly         bool   `json:"readonly"`
	LowercaseContent string `json:"lowercase_content"`
}

type filesystemSearchIndex struct {
	Documents []filesystemSearchIndexEntry `json:"documents"`
}

// filesystemSearchIndexPath places the cache as a sibling of the tenant's
// user-docs root (<sharedFolder>/<db>/knowledge-search-index.json), clearly
// outside the doc tree itself so ListDocuments' filesystem walk never picks
// it up as a document.
func filesystemSearchIndexPath(o MarkdownRouteOptions, dbName string) string {
	publicRoot, _ := requestStateForDB(o, dbName)
	return filepath.Join(filepath.Dir(strings.TrimRight(publicRoot, string(os.PathSeparator))), "knowledge-search-index.json")
}

// rebuildFilesystemIndex re-walks the full doc tree and rewrites the cache
// file. Called after any mutation while Postgres FTS isn't available, and
// lazily the first time a search misses a cache file.
func rebuildFilesystemIndex(o MarkdownRouteOptions, dbName string) {
	menu, err := ListDocuments(o, dbName)
	if err != nil {
		log.Warnf("knowledge: list documents to rebuild filesystem search index on %q: %v", dbName, err)
		return
	}
	index := filesystemSearchIndex{Documents: make([]filesystemSearchIndexEntry, 0, len(menu.Items))}
	for _, item := range menu.Items {
		content, err := ReadDocument(o, dbName, item.RelativePath)
		if err != nil {
			log.Warnf("knowledge: read document %q to rebuild filesystem search index on %q: %v", item.RelativePath, dbName, err)
			continue
		}
		index.Documents = append(index.Documents, filesystemSearchIndexEntry{
			Name:             item.Name,
			RelativePath:     item.RelativePath,
			Source:           item.Source,
			ReadOnly:         item.ReadOnly,
			LowercaseContent: strings.ToLower(string(content)),
		})
	}

	path := filesystemSearchIndexPath(o, dbName)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		log.Warnf("knowledge: create directory for filesystem search index on %q: %v", dbName, err)
		return
	}
	payload, err := json.Marshal(index)
	if err != nil {
		log.Warnf("knowledge: marshal filesystem search index on %q: %v", dbName, err)
		return
	}
	if err := writeFileAtomic(path, payload, 0o644); err != nil {
		log.Warnf("knowledge: write filesystem search index on %q: %v", dbName, err)
	}
}

// searchFilesystemIndex loads the cache file (rebuilding it first if
// missing), then ranks documents by lowercased-content occurrence count, with
// a fixed boost for a name match so an exact-title hit still surfaces first.
func searchFilesystemIndex(o MarkdownRouteOptions, dbName, query string) ([]markdownMenuItem, error) {
	path := filesystemSearchIndexPath(o, dbName)
	payload, err := os.ReadFile(path)
	if err != nil {
		rebuildFilesystemIndex(o, dbName)
		payload, err = os.ReadFile(path)
		if err != nil {
			return nil, err
		}
	}

	var index filesystemSearchIndex
	if err := json.Unmarshal(payload, &index); err != nil {
		return nil, err
	}

	needle := strings.ToLower(query)
	type scored struct {
		item  markdownMenuItem
		score int
	}
	matches := make([]scored, 0)
	for _, entry := range index.Documents {
		score := strings.Count(entry.LowercaseContent, needle)
		if strings.Contains(strings.ToLower(entry.Name), needle) {
			score += 1000 // name match ranks above any number of content hits
		}
		if score == 0 {
			continue
		}
		matches = append(matches, scored{
			item: markdownMenuItem{
				Name:         entry.Name,
				RelativePath: entry.RelativePath,
				Source:       entry.Source,
				ReadOnly:     entry.ReadOnly,
			},
			score: score,
		})
	}
	sort.SliceStable(matches, func(i, j int) bool { return matches[i].score > matches[j].score })

	const limit = 20
	if len(matches) > limit {
		matches = matches[:limit]
	}
	items := make([]markdownMenuItem, 0, len(matches))
	for _, m := range matches {
		items = append(items, m.item)
	}
	return items, nil
}
