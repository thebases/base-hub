package models

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"html"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"git.thebase.vn/base_hub/internal/config"
	kernelmodules "git.thebase.vn/base_hub/internal/modules"
	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/uptrace/bun"
)

// applyWebsiteRevisionCAS implements the editor's optimistic-concurrency contract: writes send
// {revision, ...patch}. A nonzero "revision" must match the row, otherwise the write is rejected
// instead of silently overwriting a concurrent edit; 0/omitted bypasses the check (deliberate, the
// editor often sends 0). The compare and the bump are one conditional UPDATE, so two writers that
// sent the same revision cannot both pass (a compare in Go followed by the ORM write could); the
// caller must run it in the same transaction as the record write (runInWebsiteTx), so the row lock
// taken here holds until that write commits and a failed write rolls the bump back. working's
// "revision" is set to the new value so the record write persists it unchanged.
func applyWebsiteRevisionCAS(ctx context.Context, modelName string, id string, working orm.RecordMap) error {
	rowID, err := strconv.ParseInt(strings.TrimSpace(id), 10, 64)
	if err != nil || rowID <= 0 {
		return orm.InvalidParamsf("%s: invalid id %q", modelName, id)
	}
	table := orm.RuntimeModelRef(modelName, nil).StorageName
	db := websiteSiteTxHandle(ctx)
	if table == "" || db == nil {
		return fmt.Errorf("%s revision check requires a registered model and a database handle", modelName)
	}
	clientRevision, _ := int64FromAny(working["revision"])
	next, err := casWebsiteRevision(ctx, db, modelName, table, rowID, clientRevision)
	if err != nil {
		return err
	}
	working["revision"] = next
	return nil
}

// casWebsiteRevision is the single conditional UPDATE behind applyWebsiteRevisionCAS: it bumps the
// row's revision when clientRevision is 0 or equal to the stored one and returns the new value.
func casWebsiteRevision(ctx context.Context, db bun.IDB, modelName, table string, rowID, clientRevision int64) (int64, error) {
	var next []int64
	if err := db.NewRaw("UPDATE ? SET revision = revision + 1 WHERE id = ? AND (? = 0 OR revision = ?) RETURNING revision",
		bun.Ident(table), rowID, clientRevision, clientRevision).Scan(ctx, &next); err != nil {
		return 0, fmt.Errorf("%s %d: bump revision: %w", modelName, rowID, err)
	}
	if len(next) == 0 {
		if clientRevision == 0 {
			return 0, orm.NotFoundf("%s %d not found", modelName, rowID)
		}
		return 0, orm.Conflictf("%s %d revision conflict: it was changed by someone else (expected revision %d); reload it and apply your changes again",
			modelName, rowID, clientRevision)
	}
	return next[0], nil
}

// websiteSudoContext runs a lookup without the caller's record rules and company scope. Uniqueness
// checks need it: a duplicate the caller cannot see still collides (and the unique index rejects it).
func websiteSudoContext(ctx context.Context) context.Context {
	env := orm.EnvironmentFromContext(ctx)
	if env == nil || env.IsSudo() {
		return ctx
	}
	return orm.ContextWithEnvironment(ctx, env.WithUser(env.UserID(), true))
}

// pgUniqueViolation is PostgreSQL's SQLSTATE for a unique index/constraint violation.
const pgUniqueViolation = "23505"

// websiteUniqueViolation turns a unique violation of index (raised when two writers pass the Go check
// concurrently) into a -32009 Conflict carrying message; any other error is returned unchanged.
func websiteUniqueViolation(err error, index, message string) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation && pgErr.ConstraintName == index {
		return orm.Conflictf("%s", message)
	}
	return err
}

func recordMapFromArgs(args []any, kwargs map[string]any, keys ...string) (orm.RecordMap, error) {
	if len(args) > 0 {
		if record, ok := args[0].(map[string]any); ok {
			return orm.RecordMap(record), nil
		}
		if record, ok := args[0].(orm.RecordMap); ok {
			return cloneRecordMap(record), nil
		}
	}
	for _, key := range keys {
		if record, ok := mapValue(kwargs, key).(map[string]any); ok {
			return orm.RecordMap(record), nil
		}
		if record, ok := mapValue(kwargs, key).(orm.RecordMap); ok {
			return cloneRecordMap(record), nil
		}
	}
	return nil, nil
}

func cloneRecordMap(values orm.RecordMap) orm.RecordMap {
	cloned := make(orm.RecordMap, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func mapValue(values map[string]any, key string) any {
	if values == nil {
		return nil
	}
	return values[key]
}

func stringValue(raw any) string {
	switch typed := raw.(type) {
	case string:
		return strings.TrimSpace(typed)
	default:
		return ""
	}
}

func int64FromAny(raw any) (int64, bool) {
	switch typed := raw.(type) {
	case int64:
		return typed, true
	case int:
		return int64(typed), true
	case int32:
		return int64(typed), true
	case float64:
		return int64(typed), true
	case string:
		value, err := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		return value, err == nil
	case []any:
		// Runtime reads serialize many2one values as [id, display_name], so
		// website model actions must peel the id out before validating links.
		if len(typed) == 0 {
			return 0, false
		}
		return int64FromAny(typed[0])
	case [2]any:
		// RuntimeRead/RuntimeSearchRead enrich many2one fields into a fixed-size
		// [id, display_name] tuple (see prefetchManyToOneDisplaysCore), distinct
		// from the []any slice shape above -- both must be peeled the same way.
		return int64FromAny(typed[0])
	case map[string]any:
		return int64FromAny(typed["id"])
	default:
		return 0, false
	}
}

func anyToInt64(raw any) int64 {
	value, _ := int64FromAny(raw)
	return value
}

func boolValue(raw any) (bool, bool) {
	switch typed := raw.(type) {
	case bool:
		return typed, true
	case string:
		switch strings.ToLower(strings.TrimSpace(typed)) {
		case "1", "true", "yes":
			return true, true
		case "0", "false", "no":
			return false, true
		}
	case int:
		return typed != 0, true
	case int64:
		return typed != 0, true
	case float64:
		return typed != 0, true
	}
	return false, false
}

func hasRecordMapKey(values orm.RecordMap, key string) bool {
	if values == nil {
		return false
	}
	_, ok := values[key]
	return ok
}

func slugify(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	var builder strings.Builder
	lastDash := false
	for _, r := range raw {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			builder.WriteRune(r)
			lastDash = false
			continue
		}
		if lastDash {
			continue
		}
		builder.WriteByte('-')
		lastDash = true
	}
	slug := strings.Trim(builder.String(), "-")
	if slug == "" {
		return "website"
	}
	return slug
}

func normalizeRoutePath(raw string, isHomepage bool) string {
	if isHomepage {
		return "/"
	}
	path := strings.TrimSpace(raw)
	if path == "" || path == "/" {
		return "/"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	for strings.Contains(path, "//") {
		path = strings.ReplaceAll(path, "//", "/")
	}
	path = strings.TrimRight(path, "/")
	if path == "" {
		return "/"
	}
	return path
}

func nowRFC3339() string {
	return time.Now().UTC().Format(time.RFC3339)
}

func readSiteAndPageRows(ctx context.Context, siteID int64) ([]orm.RecordMap, []orm.RecordMap, error) {
	siteRows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(WebsiteSiteModelName, nil), []string{strconv.FormatInt(siteID, 10)}, nil)
	if err != nil {
		return nil, nil, err
	}
	pageRows, err := orm.RuntimeRepositorySearchRead(ctx, orm.RuntimeModelRef(WebsitePageModelName, nil), orm.RecordMap{
		"site_id": siteID,
		"__order": "id asc",
		"__limit": 500,
	}, nil)
	if err != nil {
		return nil, nil, err
	}
	return siteRows, pageRows, nil
}

func firstRow(rows []orm.RecordMap, label string) (orm.RecordMap, error) {
	if len(rows) == 0 {
		return nil, orm.NotFoundf("%s not found", label)
	}
	return rows[0], nil
}

func loadWebsiteSiteRow(ctx context.Context, siteID int64) (orm.RecordMap, error) {
	rows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(WebsiteSiteModelName, nil), []string{strconv.FormatInt(siteID, 10)}, nil)
	if err != nil {
		return nil, err
	}
	return firstRow(rows, "tbl.website_site")
}

func websiteContextDBName(ctx context.Context) string {
	env := orm.EnvironmentFromContext(ctx)
	if env == nil {
		return ""
	}
	return strings.TrimSpace(env.DBName())
}

func ensureWebsiteDeploymentFolder(ctx context.Context, deploymentKey string) (string, error) {
	dbName := websiteContextDBName(ctx)
	if dbName == "" {
		return "", fmt.Errorf("website publish cannot resolve the current database name")
	}
	if strings.TrimSpace(deploymentKey) == "" {
		return "", fmt.Errorf("website publish requires a deployment key")
	}
	deploymentPath := filepath.Join(websiteSharedRoot(), dbName, "public", "deployments", deploymentKey)
	// Publish must create the tenant deployment folder before the DB record is
	// activated; otherwise the admin would report a deployment that cannot be
	// inspected or populated by later filesystem emission steps.
	if err := os.MkdirAll(deploymentPath, 0o755); err != nil {
		return "", fmt.Errorf("create website deployment folder %q: %w", deploymentPath, err)
	}
	return deploymentPath, nil
}

// ensureWebsiteSiteDeployFolder returns the site's stable deploy folder,
// creating and persisting it on tbl.website_site.deploy_path the first time.
// Every page belonging to the site deploys its static file into this same
// folder across publishes (only the per-page random filename changes).
func ensureWebsiteSiteDeployFolder(ctx context.Context, siteID int64) (string, error) {
	siteRow, err := loadWebsiteSiteRow(ctx, siteID)
	if err != nil {
		return "", err
	}
	if existing := stringValue(siteRow["deploy_path"]); existing != "" {
		// A stored folder is reused only while it stays inside the tenant public root: a value outside it
		// (tampered row, restored foreign backup) is treated like a missing folder and replaced by a fresh
		// one, so publish never writes outside <shared>/<db>/public and the site is not stuck on it.
		if confined, ok := ConfinedWebsiteDeployPath(websiteContextDBName(ctx), existing); ok {
			if info, statErr := os.Stat(confined); statErr == nil && info.IsDir() {
				return confined, nil
			}
		} else {
			log.Warnf("website site %v: replacing deploy_path %q outside the tenant public root", siteID, existing)
		}
	}
	suffix, err := randomWebsiteDeploySuffix()
	if err != nil {
		return "", err
	}
	deploymentKey := fmt.Sprintf("%s-%s", slugify(stringValue(siteRow["slug"])), suffix)
	folder, err := ensureWebsiteDeploymentFolder(ctx, deploymentKey)
	if err != nil {
		return "", err
	}
	if _, err := orm.RuntimeRepositoryWrite(ctx, orm.RuntimeModelRef(WebsiteSiteModelName, nil), []string{strconv.FormatInt(siteID, 10)}, orm.RecordMap{
		"deploy_path": folder,
	}); err != nil {
		return "", err
	}
	return folder, nil
}

const websiteDeployFilenameAlphabet = "abcdefghijklmnopqrstuvwxyz"
const websiteDeployFilenameLength = 8

// randomWebsiteDeploySuffix returns an 8-random-lowercase-letter string used
// to disambiguate both site deploy folders and page deploy files.
func randomWebsiteDeploySuffix() (string, error) {
	buf := make([]byte, websiteDeployFilenameLength)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate website deploy suffix: %w", err)
	}
	letters := make([]byte, websiteDeployFilenameLength)
	for i, b := range buf {
		letters[i] = websiteDeployFilenameAlphabet[int(b)%len(websiteDeployFilenameAlphabet)]
	}
	return string(letters), nil
}

// websitePublicRoot is <shared>/<db>/public: the only tree publish may write into and /_site may
// serve from.
func websitePublicRoot(dbName string) string {
	return filepath.Join(websiteSharedRoot(), dbName, "public")
}

// websiteSharedRoot is the shared folder as an absolute path. The default shared folder is
// relative ("./data"), and deploy paths stored from it were relative too, which the absolute-path
// confinement check then rejected: a fresh install could never publish its seeded site.
func websiteSharedRoot() string {
	return absWebsitePath(config.ResolvedSharedFolder())
}

// absWebsitePath cleans path and makes it absolute against the process working directory, the same
// base a relative shared folder is resolved from when files are written.
func absWebsitePath(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		return abs
	}
	return filepath.Clean(path)
}

// ConfinedWebsiteDeployPath returns the cleaned deploy path when it lies strictly under the tenant's
// public root, ok=false otherwise. deploy_path is a DB value, so serving and publishing must not trust
// it: without this check a row pointing at a system folder would be served anonymously under /_site
// or overwritten by publish. Existing paths are also checked after symlink resolution, so a link
// inside the root cannot point the site outside it.
func ConfinedWebsiteDeployPath(dbName, path string) (string, bool) {
	dbName, path = strings.TrimSpace(dbName), strings.TrimSpace(path)
	if dbName == "" || path == "" {
		return "", false
	}
	root := websitePublicRoot(dbName)
	// Rows written while the shared folder was relative hold relative paths; resolving them the same
	// way as the root keeps those sites working. The strictly-under check below still confines them.
	clean := absWebsitePath(path)
	if !pathStrictlyUnder(root, clean) {
		return "", false
	}
	// A missing path cannot be served and publish creates it under the root, so only an existing one
	// needs the symlink check. The root is resolved too, in case the shared folder itself is a link.
	if resolved, err := filepath.EvalSymlinks(clean); err == nil {
		resolvedRoot, rootErr := filepath.EvalSymlinks(root)
		if rootErr != nil || !pathStrictlyUnder(resolvedRoot, resolved) {
			return "", false
		}
	}
	return clean, true
}

func pathStrictlyUnder(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." || filepath.IsAbs(rel) {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

// websiteServerManagedKey reports whether a payload key targets a column only server code may set
// (deploy_path, revision, publish_pending). Keys are compared lowercased without underscores because
// the repository maps aliases ("DeployPath", "deployPath") onto the same column.
func websiteServerManagedKey(key string, fields ...string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(key, "_", ""))
	for _, field := range fields {
		if normalized == strings.ReplaceAll(field, "_", "") {
			return true
		}
	}
	return false
}

// stripWebsiteServerManagedFields drops client-supplied server-managed fields from a create/write
// payload. Silent rather than rejected: the builder and form views read these fields and may send
// them back on save. Legitimate writers (publish, scheduler, revision CAS) set them through
// repository writes or after this strip, so they are unaffected.
func stripWebsiteServerManagedFields(values orm.RecordMap, fields ...string) {
	for key := range values {
		if websiteServerManagedKey(key, fields...) {
			delete(values, key)
		}
	}
}

// deleteWebsiteStaticFile removes a previously deployed page path (an Owl
// scaffold folder, or a legacy pre-migration single .html file), guarding
// against deleting anything outside the tenant's public folder in case of
// corrupted/foreign path data. Missing paths are treated as already-deleted.
func deleteWebsiteStaticFile(ctx context.Context, path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	dbName := websiteContextDBName(ctx)
	if dbName == "" {
		return fmt.Errorf("website deploy cleanup cannot resolve the current database name")
	}
	root := websitePublicRoot(dbName)
	cleanPath := absWebsitePath(path)
	rootWithSeparator := root + string(os.PathSeparator)
	if !strings.HasPrefix(cleanPath, rootWithSeparator) {
		return fmt.Errorf("refusing to remove website deploy file outside public root: %q", path)
	}
	info, statErr := os.Stat(cleanPath)
	if statErr != nil {
		if errors.Is(statErr, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("stat website deploy path %q: %w", cleanPath, statErr)
	}
	if info.IsDir() {
		if err := os.RemoveAll(cleanPath); err != nil {
			return fmt.Errorf("remove website deploy folder %q: %w", cleanPath, err)
		}
		// A logged-in page keeps its template in <site>/protected/<Name>.xml, outside the page folder.
		if filepath.Base(filepath.Dir(cleanPath)) == "pages" {
			name := filepath.Base(cleanPath)
			_ = os.Remove(filepath.Join(filepath.Dir(filepath.Dir(cleanPath)), websiteProtectedDir, name+".xml"))
		}
		return nil
	}
	if err := os.Remove(cleanPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove website deploy file %q: %w", cleanPath, err)
	}
	return nil
}

// websiteStaticJSResourceDir is the module-relative directory (see
// ReadModuleResource/ListModuleResourceDir path conventions) that every
// published website page must attach its <script> tags from.
const websiteStaticJSResourceDir = "website/static/js"

// websiteStaticJSPublicPathPrefix is where serveModuleStaticResource
// (internal/cmd/server.go, route "/:module/static/*") exposes that same
// directory to public/runtime page requests.
const websiteStaticJSPublicPathPrefix = "/website/static/js/"

// renderWebsiteStaticScriptTags auto-discovers every file currently under
// app/modules/website/static/js (disk in dev, embedded in prod via
// kernelmodules.ListModuleResourceDir) and returns one <script src> tag per
// file, so adding or removing a file there requires no further code change
// to reach published pages. A listing failure (directory missing) yields no
// tags rather than failing the publish.
func renderWebsiteStaticScriptTags() string {
	names, err := kernelmodules.ListModuleResourceDir(websiteStaticJSResourceDir)
	if err != nil {
		return ""
	}
	var builder strings.Builder
	for _, name := range names {
		if !strings.HasSuffix(strings.ToLower(name), ".js") {
			continue
		}
		builder.WriteString("<script src=\"")
		builder.WriteString(html.EscapeString(websiteStaticJSPublicPathPrefix + name))
		builder.WriteString("\"></script>\n")
	}
	return builder.String()
}

// websitePageSEO reads pageRow's nested Seo jsonb map, returning nil if unset
// or malformed rather than panicking on the type assertion.
func websitePageSEO(pageRow orm.RecordMap) map[string]any {
	seo, _ := pageRow["seo"].(map[string]any)
	return seo
}

func websiteSEOStringField(seo map[string]any, key string) string {
	if seo == nil {
		return ""
	}
	return stringValue(seo[key])
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
