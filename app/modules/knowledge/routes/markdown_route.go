package routes

// File overview:
// - Responsibility: implements Fiber transport handlers and request-bound orchestration for the markdown route surface.
// - Key symbols: `buildMarkdownMenu`, `MarkdownRouteOptions`, `markdownMenuItem`.
// - Pattern: Keeps transport concerns at the HTTP boundary: requests are parsed and validated here before business work is delegated deeper into services or the ORM runtime.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"git.thebase.vn/base_hub/internal/orm"
	internalroutes "git.thebase.vn/base_hub/internal/routes"
	knowledgemodels "git.thebase.vn/base_hub/modules/knowledge/models"
	"github.com/gofiber/fiber/v3"
)

const (
	markdownMenuFileName       = "menu.json"
	markdownBaseNamespace      = "base"
	markdownUserGuideNamespace = "user_guide"
	markdownPublicNamespace    = "public"
)

// MarkdownRouteOptions wires the read-only embedded document sources and the
// tenant-scoped public-doc storage resolver into the route handlers.
//   - BaseFS (namespace "base") is control-plane content: only surfaced when
//     the requesting tenant is HomeDBName. Everything under BaseFS, including
//     the nested "user_guide" sub-tree, is always read-only.
//   - UserGuideFS (namespace "user_guide") is shared, read-only content
//     surfaced identically for every tenant.
//   - PublicRootForDB resolves the tenant-scoped, read/write "public"
//     namespace storage root for a given database name. This is the only
//     namespace new documents may be created or moved into.
type MarkdownRouteOptions struct {
	BaseFS          fs.FS
	UserGuideFS     fs.FS
	HomeDBName      string
	PublicRootForDB func(dbName string) string
	// Installed reports whether the knowledge module is installed for the
	// request's tenant. When set and false, GET /md/menu.json answers an empty
	// tree (200) instead of the kernel guard's 404, so the always-loaded
	// sidebar doesn't log a failed request on tenants without the module.
	// nil means "always installed" (tests, direct callers).
	Installed func(ctx context.Context) (bool, error)
}

type markdownMenuItem struct {
	Name         string `json:"name"`
	RelativePath string `json:"relative_path"`
	URL          string `json:"url"`
	RoutePath    string `json:"route_path"`
	SizeBytes    int64  `json:"size_bytes"`
	UpdatedAt    int64  `json:"updated_at"` // UTC Unix seconds; 0 when unknown
	Source       string `json:"source"`
	ReadOnly     bool   `json:"readonly"`
}

// UnmarshalJSON accepts updated_at as Unix seconds (current snapshots) or an RFC3339 string
// (the legacy format still shipped in the embedded base documents' menu.json), so one old
// snapshot cannot fail the whole menu with a 500.
func (item *markdownMenuItem) UnmarshalJSON(data []byte) error {
	type plain markdownMenuItem
	aux := struct {
		*plain
		UpdatedAt json.RawMessage `json:"updated_at"`
	}{plain: (*plain)(item)}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	item.UpdatedAt = 0
	raw := strings.TrimSpace(string(aux.UpdatedAt))
	switch {
	case raw == "" || raw == "null":
	case strings.HasPrefix(raw, "\""):
		var text string
		if err := json.Unmarshal(aux.UpdatedAt, &text); err != nil {
			return err
		}
		if parsed, err := time.Parse(time.RFC3339, text); err == nil {
			item.UpdatedAt = parsed.UTC().Unix()
		}
	default:
		if err := json.Unmarshal(aux.UpdatedAt, &item.UpdatedAt); err != nil {
			return err
		}
	}
	return nil
}

type markdownMenuResponse struct {
	Items []markdownMenuItem `json:"items"`
}

// markdownMenuResult wraps the menu in the standard {"result": {items, total}} envelope.
func markdownMenuResult(menu markdownMenuResponse) fiber.Map {
	return fiber.Map{"result": fiber.Map{"items": menu.Items, "total": len(menu.Items)}}
}

type markdownMutationRequest struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Content string `json:"content"`
}

type markdownRenameRequest struct {
	FromPath string `json:"from_path"`
	ToPath   string `json:"to_path"`
}

// resolveMarkdownRequestState resolves the requesting tenant's public-doc
// storage root and whether the Home-only "base" namespace should be visible.
// Every route handler must call this per request instead of caching a single
// startup-time root, since the storage root is tenant-scoped.
//
// The DB is the one bindRequestDatabase validated and bound for this request
// (?db=/X-Database-Name, else the locked db cookie, else home), so a
// cookie-bound tenant session reads and writes its own tenant's documents.
func resolveMarkdownRequestState(c fiber.Ctx, options MarkdownRouteOptions) (publicRoot string, includeBase bool, err error) {
	dbName, err := internalroutes.RequestRPCDBName(c)
	if err != nil {
		return "", false, err
	}
	publicRoot = options.PublicRootForDB(dbName)
	includeBase = dbName == strings.TrimSpace(options.HomeDBName)
	return publicRoot, includeBase, nil
}

// requireMarkdownWriter authenticates the caller and applies the knowledge
// write rule (sudo or base.group_system, knowledgemodels.EnsureKnowledgeWriteAllowed).
// The identity's token is already bound to the request DB by RequireRPCIdentity.
func requireMarkdownWriter(c fiber.Ctx) error {
	identity, err := internalroutes.RequireRPCIdentity(c, false)
	if err != nil {
		return err
	}
	ctx := orm.ContextWithEnvironment(c.Context(), orm.EnvironmentForIdentity(&orm.RBACIdentity{
		UserID:       identity.UserID,
		DBName:       identity.DBName,
		Login:        identity.Username,
		RoleID:       identity.RoleID,
		RoleName:     identity.RoleName,
		GroupIDs:     append([]int64(nil), identity.GroupIDs...),
		CompanyID:    identity.CompanyID,
		CompanyIDs:   append([]int64(nil), identity.CompanyIDs...),
		AuthMode:     identity.AuthMode,
		IsSuperadmin: identity.Sudo,
	}))
	if err := knowledgemodels.EnsureKnowledgeWriteAllowed(ctx); err != nil {
		var typed *orm.Error
		if errors.As(err, &typed) && typed.Code == orm.ErrForbidden {
			return fiber.NewError(fiber.StatusForbidden, typed.Message)
		}
		log.Printf("knowledge: write permission check failed: %v", err)
		return fiber.ErrInternalServerError
	}
	return nil
}

// RegisterMarkdownMenuRoute registers GET /md/menu.json. It must be registered
// before the /md/* catch-all in RegisterMarkdownDocumentRoutes so Fiber matches
// it first.
func RegisterMarkdownMenuRoute(app fiber.Router, options MarkdownRouteOptions) {
	app.Get("/md/menu.json", func(c fiber.Ctx) error {
		// Module not installed for this tenant: no documents, not an error.
		if options.Installed != nil {
			installed, err := options.Installed(c.Context())
			if err != nil {
				return fiber.ErrInternalServerError
			}
			if !installed {
				return c.JSON(markdownMenuResult(markdownMenuResponse{Items: []markdownMenuItem{}}))
			}
		}
		publicRoot, includeBase, err := resolveMarkdownRequestState(c, options)
		if err != nil {
			return fiber.NewError(fiber.StatusBadRequest, err.Error())
		}
		// The sidebar KnowLedge section is only for superadmin (base.group_system)
		// callers: an authenticated non-superadmin gets an empty menu. Anonymous
		// callers are unchanged (the /md/* documents stay public), so this only
		// hides the sidebar entry and is not an access control on the documents.
		if identity, idErr := internalroutes.CurrentRPCIdentity(c); idErr == nil && identity != nil && !identity.Sudo {
			return c.JSON(markdownMenuResult(markdownMenuResponse{Items: []markdownMenuItem{}}))
		}
		payload, err := buildMarkdownMenu(options.BaseFS, options.UserGuideFS, publicRoot, includeBase)
		if err != nil {
			// The API error handler masks 500 text from clients; keep the cause in the log.
			log.Printf("knowledge: build menu failed (db root %q, base=%v): %v", publicRoot, includeBase, err)
			return fiber.NewError(fiber.StatusInternalServerError, err.Error())
		}
		return c.JSON(markdownMenuResult(payload))
	})
}

// RegisterMarkdownDocumentRoutes registers the document read/write routes
// (/md/api/files*, /md/*). Callers mount these behind the module install guard.
func RegisterMarkdownDocumentRoutes(app fiber.Router, options MarkdownRouteOptions) {
	baseFS := options.BaseFS
	userGuideFS := options.UserGuideFS

	app.Post("/md/api/files", func(c fiber.Ctx) error {
		if err := requireMarkdownWriter(c); err != nil {
			return err
		}
		publicRoot, _, err := resolveMarkdownRequestState(c, options)
		if err != nil {
			return fiber.NewError(fiber.StatusBadRequest, err.Error())
		}

		var req markdownMutationRequest
		if err := c.Bind().Body(&req); err != nil {
			return fiber.NewError(fiber.StatusBadRequest, "invalid request body")
		}

		absolutePath, relativePath, err := resolvePublicMarkdownPath(publicRoot, req.Path)
		if err != nil {
			return fiber.NewError(fiber.StatusBadRequest, err.Error())
		}
		if _, err := os.Stat(absolutePath); err == nil {
			return fiber.NewError(fiber.StatusConflict, "markdown file already exists")
		} else if !errors.Is(err, os.ErrNotExist) {
			return fiber.NewError(fiber.StatusInternalServerError, err.Error())
		}

		if err := os.MkdirAll(filepath.Dir(absolutePath), 0o755); err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, err.Error())
		}
		if err := writeFileAtomic(absolutePath, []byte(req.Content), 0o644); err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, err.Error())
		}
		payload, err := ensureMarkdownMenuSnapshot(publicRoot)
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, err.Error())
		}
		if err := applyMarkdownMenuLabel(publicRoot, payload, relativePath, req.Name); err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, err.Error())
		}

		return c.Status(fiber.StatusCreated).JSON(fiber.Map{"result": fiber.Map{"path": joinMarkdownLogicalPath(markdownPublicNamespace, relativePath)}})
	})

	app.Put("/md/api/files/*", func(c fiber.Ctx) error {
		if err := requireMarkdownWriter(c); err != nil {
			return err
		}
		publicRoot, _, err := resolveMarkdownRequestState(c, options)
		if err != nil {
			return fiber.NewError(fiber.StatusBadRequest, err.Error())
		}

		var req markdownMutationRequest
		if err := c.Bind().Body(&req); err != nil {
			return fiber.NewError(fiber.StatusBadRequest, "invalid request body")
		}

		targetPath := strings.TrimPrefix(c.Params("*"), "/")
		absolutePath, relativePath, err := resolvePublicMarkdownPath(publicRoot, targetPath)
		if err != nil {
			return fiber.NewError(fiber.StatusBadRequest, err.Error())
		}
		if _, err := os.Stat(absolutePath); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return fiber.NewError(fiber.StatusNotFound, "markdown file not found")
			}
			return fiber.NewError(fiber.StatusInternalServerError, err.Error())
		}
		if err := writeFileAtomic(absolutePath, []byte(req.Content), 0o644); err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, err.Error())
		}
		if _, err := ensureMarkdownMenuSnapshot(publicRoot); err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, err.Error())
		}

		return c.JSON(fiber.Map{"result": fiber.Map{"path": joinMarkdownLogicalPath(markdownPublicNamespace, relativePath)}})
	})

	app.Post("/md/api/files/rename", func(c fiber.Ctx) error {
		if err := requireMarkdownWriter(c); err != nil {
			return err
		}
		publicRoot, _, err := resolveMarkdownRequestState(c, options)
		if err != nil {
			return fiber.NewError(fiber.StatusBadRequest, err.Error())
		}

		var req markdownRenameRequest
		if err := c.Bind().Body(&req); err != nil {
			return fiber.NewError(fiber.StatusBadRequest, "invalid request body")
		}

		fromAbsolutePath, fromRelativePath, err := resolvePublicMarkdownPath(publicRoot, req.FromPath)
		if err != nil {
			return fiber.NewError(fiber.StatusBadRequest, err.Error())
		}
		toAbsolutePath, toRelativePath, err := resolvePublicMarkdownPath(publicRoot, req.ToPath)
		if err != nil {
			return fiber.NewError(fiber.StatusBadRequest, err.Error())
		}
		previousMenu, _ := readMarkdownMenuSnapshot(publicRoot)
		existingName := markdownLabelForPath(previousMenu, fromRelativePath)

		if _, err := os.Stat(fromAbsolutePath); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return fiber.NewError(fiber.StatusNotFound, "markdown file not found")
			}
			return fiber.NewError(fiber.StatusInternalServerError, err.Error())
		}
		if fromAbsolutePath != toAbsolutePath {
			if _, err := os.Stat(toAbsolutePath); err == nil {
				return fiber.NewError(fiber.StatusConflict, "target markdown file already exists")
			} else if !errors.Is(err, os.ErrNotExist) {
				return fiber.NewError(fiber.StatusInternalServerError, err.Error())
			}
		}
		if err := os.MkdirAll(filepath.Dir(toAbsolutePath), 0o755); err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, err.Error())
		}
		if err := os.Rename(fromAbsolutePath, toAbsolutePath); err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, err.Error())
		}
		payload, err := ensureMarkdownMenuSnapshot(publicRoot)
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, err.Error())
		}
		if err := applyMarkdownMenuLabel(publicRoot, payload, toRelativePath, existingName); err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, err.Error())
		}

		return c.JSON(fiber.Map{"result": fiber.Map{"path": joinMarkdownLogicalPath(markdownPublicNamespace, toRelativePath)}})
	})

	app.Delete("/md/api/files/*", func(c fiber.Ctx) error {
		if err := requireMarkdownWriter(c); err != nil {
			return err
		}
		publicRoot, _, err := resolveMarkdownRequestState(c, options)
		if err != nil {
			return fiber.NewError(fiber.StatusBadRequest, err.Error())
		}

		targetPath := strings.TrimPrefix(c.Params("*"), "/")
		absolutePath, relativePath, err := resolvePublicMarkdownPath(publicRoot, targetPath)
		if err != nil {
			return fiber.NewError(fiber.StatusBadRequest, err.Error())
		}
		if err := os.Remove(absolutePath); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return fiber.NewError(fiber.StatusNotFound, "markdown file not found")
			}
			return fiber.NewError(fiber.StatusInternalServerError, err.Error())
		}
		if _, err := ensureMarkdownMenuSnapshot(publicRoot); err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, err.Error())
		}

		return c.JSON(fiber.Map{"result": fiber.Map{"deleted": true, "path": joinMarkdownLogicalPath(markdownPublicNamespace, relativePath)}})
	})

	readDocument := func(c fiber.Ctx) error {
		publicRoot, includeBase, err := resolveMarkdownRequestState(c, options)
		if err != nil {
			return fiber.NewError(fiber.StatusBadRequest, err.Error())
		}
		targetPath := strings.TrimPrefix(c.Params("*"), "/")
		content, err := readMarkdownDocument(baseFS, userGuideFS, publicRoot, targetPath, includeBase)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) || errors.Is(err, os.ErrNotExist) {
				return fiber.ErrNotFound
			}
			return fiber.NewError(fiber.StatusBadRequest, err.Error())
		}
		c.Type("md", "utf-8")
		return c.Send(content)
	}

	app.Get("/md/*", readDocument)
	app.Head("/md/*", readDocument)
}

// buildMarkdownMenu builds the markdown menu handled by this package.
// baseFS is only included when includeBase is true, keeping the Home-only
// control-plane namespace invisible to every other tenant's menu.
func buildMarkdownMenu(baseFS, userGuideFS fs.FS, publicRoot string, includeBase bool) (markdownMenuResponse, error) {
	items := make([]markdownMenuItem, 0)
	if includeBase {
		baseItems, err := listEmbeddedMarkdownMenuItems(baseFS, markdownBaseNamespace)
		if err != nil {
			return markdownMenuResponse{}, err
		}
		items = append(items, baseItems...)
	}

	userGuideItems, err := listEmbeddedMarkdownMenuItems(userGuideFS, markdownUserGuideNamespace)
	if err != nil {
		return markdownMenuResponse{}, err
	}
	items = append(items, userGuideItems...)

	// Validate the invariants for this stage before the function commits to the next step.
	publicItems, err := ensureMarkdownMenuSnapshot(publicRoot)
	// The snapshot is a cache written as a side effect of this read. If it cannot
	// be refreshed (read-only volume, permissions, corrupt menu.json), still
	// serve the menu from a plain directory listing instead of a 500.
	if err != nil {
		log.Printf("knowledge: menu snapshot unavailable for %q, listing directly: %v", publicRoot, err)
		listed, listErr := listMarkdownMenuItemsFromRoot(publicRoot)
		if listErr != nil {
			return markdownMenuResponse{}, listErr
		}
		publicItems = markdownMenuResponse{Items: listed}
	}
	items = append(items, namespaceMarkdownMenuItems(publicItems.Items, markdownPublicNamespace, markdownPublicNamespace, false)...)

	sort.Slice(items, func(i, j int) bool {
		return items[i].RelativePath < items[j].RelativePath
	})
	return markdownMenuResponse{Items: items}, nil
}

// menuSnapshotMu serialises every read-modify-write of a public menu.json
// (list refresh and label edits). Without it a list call that read the
// snapshot before a concurrent label write, and wrote after it, silently
// dropped the new label. One process-wide lock is enough: the critical
// section is a directory walk plus a small file write.
var menuSnapshotMu sync.Mutex

// ensureMarkdownMenuSnapshot ensures the markdown menu snapshot handled by this package.
func ensureMarkdownMenuSnapshot(root string) (markdownMenuResponse, error) {
	menuSnapshotMu.Lock()
	defer menuSnapshotMu.Unlock()
	existingMenu, err := readMarkdownMenuSnapshot(root)
	if err != nil {
		return markdownMenuResponse{}, err
	}
	nameByPath := make(map[string]string, len(existingMenu.Items))
	for _, item := range existingMenu.Items {
		nameByPath[item.RelativePath] = strings.TrimSpace(item.Name)
	}

	items, err := listMarkdownMenuItemsFromRoot(root)
	if err != nil {
		return markdownMenuResponse{}, err
	}
	for index := range items {
		if existingName := strings.TrimSpace(nameByPath[items[index].RelativePath]); existingName != "" {
			items[index].Name = existingName
		}
	}

	payload := markdownMenuResponse{Items: items}
	if err := writeMarkdownMenuSnapshot(root, payload); err != nil {
		return markdownMenuResponse{}, err
	}
	return payload, nil
}

// readMarkdownMenuSnapshot reads the markdown menu snapshot handled by this package.
func readMarkdownMenuSnapshot(root string) (markdownMenuResponse, error) {
	menuPath := filepath.Join(filepath.Clean(root), markdownMenuFileName)
	raw, err := os.ReadFile(menuPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return markdownMenuResponse{}, nil
		}
		return markdownMenuResponse{}, err
	}
	return decodeMarkdownMenuSnapshot(raw)
}

// readMarkdownMenuSnapshotFS reads the markdown menu snapshot fs handled by this package.
func readMarkdownMenuSnapshotFS(root fs.FS) (markdownMenuResponse, error) {
	if root == nil {
		return markdownMenuResponse{}, nil
	}
	raw, err := fs.ReadFile(root, markdownMenuFileName)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return markdownMenuResponse{}, nil
		}
		return markdownMenuResponse{}, err
	}
	return decodeMarkdownMenuSnapshot(raw)
}

// decodeMarkdownMenuSnapshot decodes the markdown menu snapshot handled by this package.
func decodeMarkdownMenuSnapshot(raw []byte) (markdownMenuResponse, error) {
	if len(raw) == 0 {
		return markdownMenuResponse{}, nil
	}

	var payload markdownMenuResponse
	if err := json.Unmarshal(raw, &payload); err != nil {
		return markdownMenuResponse{}, err
	}
	return payload, nil
}

// listEmbeddedMarkdownMenuItems lists the embedded markdown menu items handled by this package.
func listEmbeddedMarkdownMenuItems(root fs.FS, namespace string) ([]markdownMenuItem, error) {
	if root == nil {
		return []markdownMenuItem{}, nil
	}

	existingMenu, err := readMarkdownMenuSnapshotFS(root)
	if err != nil {
		return nil, err
	}
	nameByPath := make(map[string]string, len(existingMenu.Items))
	for _, item := range existingMenu.Items {
		nameByPath[item.RelativePath] = strings.TrimSpace(item.Name)
	}

	items, err := listMarkdownMenuItemsFromFS(root)
	if err != nil {
		return nil, err
	}
	for index := range items {
		if existingName := strings.TrimSpace(nameByPath[items[index].RelativePath]); existingName != "" {
			items[index].Name = existingName
		}
	}
	return namespaceMarkdownMenuItems(items, namespace, namespace, true), nil
}

// listMarkdownMenuItemsFromRoot lists the markdown menu items from root handled by this package.
func listMarkdownMenuItemsFromRoot(root string) ([]markdownMenuItem, error) {
	root = filepath.Clean(root)
	items := make([]markdownMenuItem, 0)
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if d.Name() == markdownMenuFileName || strings.ToLower(filepath.Ext(d.Name())) != ".md" {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return err
		}
		relativePath, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		items = append(items, newMarkdownMenuItem(filepath.ToSlash(relativePath), info.Size(), info.ModTime()))
		return nil
	})
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []markdownMenuItem{}, nil
		}
		return nil, err
	}

	sort.Slice(items, func(i, j int) bool {
		return items[i].RelativePath < items[j].RelativePath
	})
	return items, nil
}

// listMarkdownMenuItemsFromFS lists the markdown menu items from fs handled by this package.
func listMarkdownMenuItemsFromFS(root fs.FS) ([]markdownMenuItem, error) {
	items := make([]markdownMenuItem, 0)
	err := fs.WalkDir(root, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if d.Name() == markdownMenuFileName || strings.ToLower(filepath.Ext(d.Name())) != ".md" {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return err
		}
		items = append(items, newMarkdownMenuItem(filepath.ToSlash(path), info.Size(), info.ModTime()))
		return nil
	})
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return []markdownMenuItem{}, nil
		}
		return nil, err
	}

	sort.Slice(items, func(i, j int) bool {
		return items[i].RelativePath < items[j].RelativePath
	})
	return items, nil
}

func namespaceMarkdownMenuItems(items []markdownMenuItem, namespace, source string, readOnly bool) []markdownMenuItem {
	namespaced := make([]markdownMenuItem, 0, len(items))
	for _, item := range items {
		logicalPath := joinMarkdownLogicalPath(namespace, item.RelativePath)
		namespaced = append(namespaced, markdownMenuItem{
			Name:         item.Name,
			RelativePath: logicalPath,
			URL:          "/md/" + encodeMarkdownURLPath(logicalPath),
			RoutePath:    "/web/markdown?path=" + url.QueryEscape(logicalPath),
			SizeBytes:    item.SizeBytes,
			UpdatedAt:    item.UpdatedAt,
			Source:       source,
			ReadOnly:     readOnly,
		})
	}
	return namespaced
}

// newMarkdownMenuItem constructs and returns the markdown menu item handled by this package.
func newMarkdownMenuItem(relativePath string, sizeBytes int64, updatedAt time.Time) markdownMenuItem {
	item := markdownMenuItem{
		Name:         markdownTitle(relativePath),
		RelativePath: relativePath,
		SizeBytes:    sizeBytes,
	}
	if !updatedAt.IsZero() {
		item.UpdatedAt = updatedAt.UTC().Unix()
	}
	return item
}

func writeMarkdownMenuSnapshot(root string, payload markdownMenuResponse) error {
	root = filepath.Clean(root)
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}

	// Rewrite the snapshot atomically so the frontend never observes a partial
	// JSON document while markdown files are being mutated.
	menuPath := filepath.Join(root, markdownMenuFileName)
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	// Every menu list call refreshes the snapshot; skip the write when nothing
	// changed so steady-state reads do not churn the disk (fsync + rename).
	if existing, err := os.ReadFile(menuPath); err == nil && bytes.Equal(existing, data) {
		return nil
	}
	return writeFileAtomic(menuPath, data, 0o644)
}

func applyMarkdownMenuLabel(root string, payload markdownMenuResponse, relativePath, name string) error {
	trimmedName := strings.TrimSpace(name)
	if trimmedName == "" {
		return nil
	}
	menuSnapshotMu.Lock()
	defer menuSnapshotMu.Unlock()
	for index := range payload.Items {
		if payload.Items[index].RelativePath != relativePath {
			continue
		}
		payload.Items[index].Name = trimmedName
		return writeMarkdownMenuSnapshot(root, payload)
	}
	return nil
}

// readMarkdownDocument reads the markdown document handled by this package.
// baseFS is only consulted when includeBase is true, so a non-Home tenant
// requesting a "base/..." path (or a bare path that would fall back to it)
// gets fs.ErrNotExist instead of leaking Home-only control-plane content.
func readMarkdownDocument(baseFS, userGuideFS fs.FS, publicRoot, rawPath string, includeBase bool) ([]byte, error) {
	namespace, relativePath, err := splitMarkdownPath(rawPath)
	if err != nil {
		return nil, err
	}

	switch namespace {
	case markdownBaseNamespace:
		if !includeBase || baseFS == nil {
			return nil, fs.ErrNotExist
		}
		return fs.ReadFile(baseFS, relativePath)
	case markdownUserGuideNamespace:
		if userGuideFS == nil {
			return nil, fs.ErrNotExist
		}
		return fs.ReadFile(userGuideFS, relativePath)
	case markdownPublicNamespace:
		absolutePath := filepath.Join(filepath.Clean(publicRoot), filepath.FromSlash(relativePath))
		return os.ReadFile(filepath.Clean(absolutePath))
	default:
		absolutePublicPath := filepath.Join(filepath.Clean(publicRoot), filepath.FromSlash(relativePath))
		content, publicErr := os.ReadFile(filepath.Clean(absolutePublicPath))
		if publicErr == nil {
			return content, nil
		}
		if !errors.Is(publicErr, os.ErrNotExist) {
			return nil, publicErr
		}
		if userGuideFS != nil {
			if guideContent, guideErr := fs.ReadFile(userGuideFS, relativePath); guideErr == nil {
				return guideContent, nil
			}
		}
		if !includeBase || baseFS == nil {
			return nil, fs.ErrNotExist
		}
		return fs.ReadFile(baseFS, relativePath)
	}
}

// resolvePublicMarkdownPath resolves the public markdown path handled by this package.
func resolvePublicMarkdownPath(root, rawPath string) (string, string, error) {
	namespace, trimmedPath, err := splitMarkdownPath(rawPath)
	if err != nil {
		return "", "", err
	}
	if namespace == markdownBaseNamespace || namespace == markdownUserGuideNamespace {
		return "", "", errors.New("base and user_guide markdown documents are read-only; store new or edited documents under the public namespace instead")
	}

	absolutePath := filepath.Clean(filepath.Join(filepath.Clean(root), filepath.FromSlash(trimmedPath)))
	relativePath, err := filepath.Rel(filepath.Clean(root), absolutePath)
	if err != nil {
		return "", "", err
	}
	if relativePath == ".." || strings.HasPrefix(relativePath, ".."+string(filepath.Separator)) {
		return "", "", errors.New("markdown path escapes document root")
	}
	return absolutePath, filepath.ToSlash(relativePath), nil
}

func splitMarkdownPath(rawPath string) (string, string, error) {
	trimmedPath := strings.TrimSpace(strings.TrimPrefix(rawPath, "/"))
	if trimmedPath == "" {
		return "", "", errors.New("markdown path is required")
	}
	if strings.Contains(trimmedPath, "\\") {
		trimmedPath = strings.ReplaceAll(trimmedPath, "\\", "/")
	}
	if strings.HasPrefix(trimmedPath, "../") || strings.Contains(trimmedPath, "/../") {
		return "", "", errors.New("markdown path escapes document root")
	}
	if strings.ToLower(filepath.Ext(trimmedPath)) != ".md" {
		return "", "", errors.New("markdown files must use the .md extension")
	}

	namespace := ""
	relativePath := trimmedPath
	if head, tail, ok := strings.Cut(trimmedPath, "/"); ok {
		switch head {
		case markdownBaseNamespace, markdownUserGuideNamespace, markdownPublicNamespace:
			namespace = head
			relativePath = tail
		}
	}
	if relativePath == "" {
		return "", "", errors.New("markdown path is required")
	}

	cleanRelativePath := pathClean(relativePath)
	if cleanRelativePath == "." || cleanRelativePath == "" {
		return "", "", errors.New("markdown path is required")
	}
	if strings.HasPrefix(cleanRelativePath, "../") || cleanRelativePath == ".." {
		return "", "", errors.New("markdown path escapes document root")
	}
	return namespace, cleanRelativePath, nil
}

func joinMarkdownLogicalPath(namespace, relativePath string) string {
	if namespace == "" {
		return relativePath
	}
	return namespace + "/" + relativePath
}

func pathClean(path string) string {
	cleaned := filepath.ToSlash(filepath.Clean(filepath.FromSlash(path)))
	return strings.TrimPrefix(cleaned, "/")
}

func markdownTitle(relativePath string) string {
	base := strings.TrimSuffix(filepath.Base(relativePath), filepath.Ext(relativePath))
	base = strings.ReplaceAll(base, "-", " ")
	base = strings.ReplaceAll(base, "_", " ")
	words := strings.Fields(base)
	for index, word := range words {
		if len(word) == 0 {
			continue
		}
		words[index] = strings.ToUpper(word[:1]) + word[1:]
	}
	return strings.TrimSpace(strings.Join(words, " "))
}

func markdownLabelForPath(payload markdownMenuResponse, relativePath string) string {
	for _, item := range payload.Items {
		if item.RelativePath == relativePath {
			return strings.TrimSpace(item.Name)
		}
	}
	return ""
}

// encodeMarkdownURLPath encodes the markdown url path handled by this package.
func encodeMarkdownURLPath(relativePath string) string {
	segments := strings.Split(relativePath, "/")
	for index, segment := range segments {
		segments[index] = url.PathEscape(segment)
	}
	return strings.Join(segments, "/")
}
