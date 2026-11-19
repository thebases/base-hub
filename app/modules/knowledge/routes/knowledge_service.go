package routes

// File overview:
// - Responsibility: exports a fiber-free service API over the markdown doc tree
//   (list/read/create/update/rename/delete) so other in-process modules (the
//   base-mcp knowledge.* tool group) can reuse the exact same doc sources and
//   business logic the HTTP /md routes already use, without importing Fiber.
// - Key symbols: `SetActiveOptions`, `ListDocuments`,
//   `ReadDocument`, `CreateDocument`, `UpdateDocument`, `RenameDocument`,
//   `DeleteDocument`.
// - Pattern: thin wrappers around the same unexported helpers RegisterMarkdownRoutes'
//   handlers call (buildMarkdownMenu, resolvePublicMarkdownPath, ensureMarkdownMenuSnapshot,
//   readMarkdownDocument, ...) — no file IO or menu logic is reimplemented here.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var (
	activeOptionsMu sync.RWMutex
	activeOptions   *MarkdownRouteOptions
)

// SetActiveOptions registers the live MarkdownRouteOptions this module was
// bootstrapped with (RegisterRoutes' embedded FS handles + tenant doc root
// resolver), so ActiveOptions callers reuse the exact same doc sources
// instead of re-deriving them.
func SetActiveOptions(o MarkdownRouteOptions) {
	activeOptionsMu.Lock()
	defer activeOptionsMu.Unlock()
	activeOptions = &o
}

// requestStateForDB mirrors resolveMarkdownRequestState for a known dbName
// instead of parsing one off a fiber request header.
func requestStateForDB(o MarkdownRouteOptions, dbName string) (publicRoot string, includeBase bool) {
	publicRoot = o.PublicRootForDB(dbName)
	includeBase = dbName == strings.TrimSpace(o.HomeDBName)
	return publicRoot, includeBase
}

// ListDocuments returns the menu (base/user_guide/public namespaces per the
// same visibility rules GET /md/menu.json applies) for dbName.
func ListDocuments(o MarkdownRouteOptions, dbName string) (markdownMenuResponse, error) {
	publicRoot, includeBase := requestStateForDB(o, dbName)
	return buildMarkdownMenu(o.BaseFS, o.UserGuideFS, publicRoot, includeBase)
}

// ReadDocument returns one document's raw content by its logical path
// ("base/...", "user_guide/...", or "user/..."), mirroring GET /md/*.
func ReadDocument(o MarkdownRouteOptions, dbName, path string) ([]byte, error) {
	publicRoot, includeBase := requestStateForDB(o, dbName)
	return readMarkdownDocument(o.BaseFS, o.UserGuideFS, publicRoot, path, includeBase)
}

// FindDocuments returns every document (across the same base/user_guide/user
// namespaces ListDocuments already applies tenant visibility rules to) whose
// display name or filename contains query, case-insensitively. This lets a
// caller locate a document's logical path from a partial name or keyword
// instead of having to already know the exact namespace-prefixed path
// ReadDocument requires -- e.g. query "user guide" or "mcp" both match
// "user_guide/base-mcp-user-guide.md" (display name "Base Mcp User Guide").
func FindDocuments(o MarkdownRouteOptions, dbName, query string) ([]markdownMenuItem, error) {
	trimmedQuery := strings.TrimSpace(query)
	if trimmedQuery == "" {
		return nil, errors.New("query is required")
	}
	menu, err := ListDocuments(o, dbName)
	if err != nil {
		return nil, err
	}
	needle := strings.ToLower(trimmedQuery)
	matches := make([]markdownMenuItem, 0)
	for _, item := range menu.Items {
		filename := strings.TrimSuffix(filepath.Base(item.RelativePath), filepath.Ext(item.RelativePath))
		if strings.Contains(strings.ToLower(item.Name), needle) || strings.Contains(strings.ToLower(filename), needle) {
			matches = append(matches, item)
		}
	}
	return matches, nil
}

// CreateDocument creates a new "public" namespace document, mirroring
// POST /md/api/files. Returns the created document's logical path.
func CreateDocument(ctx context.Context, o MarkdownRouteOptions, dbName, path, name, content string) (string, error) {
	publicRoot, _ := requestStateForDB(o, dbName)
	absolutePath, relativePath, err := resolvePublicMarkdownPath(publicRoot, path)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(absolutePath); err == nil {
		return "", errors.New("markdown file already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(absolutePath), 0o755); err != nil {
		return "", err
	}
	if err := writeFileAtomic(absolutePath, []byte(content), 0o644); err != nil {
		return "", err
	}
	payload, err := ensureMarkdownMenuSnapshot(publicRoot)
	if err != nil {
		return "", err
	}
	if err := applyMarkdownMenuLabel(publicRoot, payload, relativePath, name); err != nil {
		return "", err
	}
	logicalPath := joinMarkdownLogicalPath(markdownPublicNamespace, relativePath)
	reindexDocument(ctx, o, dbName, logicalPath)
	return logicalPath, nil
}

// UpdateDocument overwrites an existing "public" namespace document, mirroring
// PUT /md/api/files/*. Returns the document's logical path.
func UpdateDocument(ctx context.Context, o MarkdownRouteOptions, dbName, path, content string) (string, error) {
	publicRoot, _ := requestStateForDB(o, dbName)
	absolutePath, relativePath, err := resolvePublicMarkdownPath(publicRoot, path)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(absolutePath); err != nil {
		return "", err
	}
	if err := writeFileAtomic(absolutePath, []byte(content), 0o644); err != nil {
		return "", err
	}
	if _, err := ensureMarkdownMenuSnapshot(publicRoot); err != nil {
		return "", err
	}
	logicalPath := joinMarkdownLogicalPath(markdownPublicNamespace, relativePath)
	reindexDocument(ctx, o, dbName, logicalPath)
	return logicalPath, nil
}

// RenameDocument moves a "public" namespace document, mirroring
// POST /md/api/files/rename. Returns the document's new logical path.
func RenameDocument(ctx context.Context, o MarkdownRouteOptions, dbName, fromPath, toPath string) (string, error) {
	publicRoot, _ := requestStateForDB(o, dbName)
	fromAbsolutePath, fromRelativePath, err := resolvePublicMarkdownPath(publicRoot, fromPath)
	if err != nil {
		return "", err
	}
	toAbsolutePath, toRelativePath, err := resolvePublicMarkdownPath(publicRoot, toPath)
	if err != nil {
		return "", err
	}
	previousMenu, _ := readMarkdownMenuSnapshot(publicRoot)
	existingName := markdownLabelForPath(previousMenu, fromRelativePath)

	if _, err := os.Stat(fromAbsolutePath); err != nil {
		return "", err
	}
	if fromAbsolutePath != toAbsolutePath {
		if _, err := os.Stat(toAbsolutePath); err == nil {
			return "", errors.New("target markdown file already exists")
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
	}
	if err := os.MkdirAll(filepath.Dir(toAbsolutePath), 0o755); err != nil {
		return "", err
	}
	if err := os.Rename(fromAbsolutePath, toAbsolutePath); err != nil {
		return "", err
	}
	payload, err := ensureMarkdownMenuSnapshot(publicRoot)
	if err != nil {
		return "", err
	}
	if err := applyMarkdownMenuLabel(publicRoot, payload, toRelativePath, existingName); err != nil {
		return "", err
	}
	fromLogicalPath := joinMarkdownLogicalPath(markdownPublicNamespace, fromRelativePath)
	toLogicalPath := joinMarkdownLogicalPath(markdownPublicNamespace, toRelativePath)
	if fromLogicalPath != toLogicalPath {
		removeIndexedDocument(ctx, o, dbName, fromLogicalPath)
	}
	reindexDocument(ctx, o, dbName, toLogicalPath)
	return toLogicalPath, nil
}

// DeleteDocument removes a "public" namespace document, mirroring
// DELETE /md/api/files/*. Returns the deleted document's logical path.
func DeleteDocument(ctx context.Context, o MarkdownRouteOptions, dbName, path string) (string, error) {
	publicRoot, _ := requestStateForDB(o, dbName)
	absolutePath, relativePath, err := resolvePublicMarkdownPath(publicRoot, path)
	if err != nil {
		return "", err
	}
	if err := os.Remove(absolutePath); err != nil {
		return "", err
	}
	if _, err := ensureMarkdownMenuSnapshot(publicRoot); err != nil {
		return "", err
	}
	logicalPath := joinMarkdownLogicalPath(markdownPublicNamespace, relativePath)
	removeIndexedDocument(ctx, o, dbName, logicalPath)
	return logicalPath, nil
}
