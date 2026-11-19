package models

// File overview:
// - Responsibility: defines built-in runtime model metadata and server-side behavior for ui menu inside the kernel model catalog.
// - Key symbols: `UIMenuRecord`, `UIMenuModelName`, `UIMenuDefinition`, `UIMenuModelSpec`.

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/fnv"
	"sort"
	"strconv"
	"strings"
	"time"

	"git.thebase.vn/base_hub/internal/config"
	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
)

// UIMenuRecord stores one navigable menu node for the frontend shell.
// The model only supports URL actions so the stored action payload remains
// small, deterministic, and easy for the client to consume.
type UIMenuRecord struct {
	orm.BaseModel
	// translate:true (SRS-F-021): menu labels store per-language values in a
	// jsonb envelope; reads resolve to the environment language with en
	// fallback. Note: complete_name stays a plain field derived from the
	// writing language (en for seeds) — it is informational, not translated.
	Name         string         `bun:"name" json:"name" meta:"translate,index,label:Name,help:Stores the human readable name for this record."`
	BID          string         `bun:"bid" json:"bid" meta:"index,label:BID,help:Stores the universal global unique record identifier for this record."`
	Sequence     int64          `bun:"sequence" json:"sequence" meta:"label:Sequence,help:Stores the numeric value for sequence."`
	ParentID     *int64         `bun:"parent_id" json:"parent_id" meta:"kind:many2one,model:ui.menu,ondelete:RESTRICT,index,label:Parent ID,help:Links this record to the related ui.menu record for parent id."`
	ChildIDs     []int64        `bun:"child_ids" json:"child_ids" meta:"kind:one2many,model:ui.menu,inverse:parent_id,stored:false,label:Child IDs,help:Lists the related ui.menu records linked back through parent_id."`
	CompleteName string         `bun:"complete_name" json:"complete_name" meta:"index,label:Complete Name,help:Stores the complete name value for this record."`
	RoutePath    string         `bun:"route_path" json:"route_path" meta:"index,label:Route Path,help:Stores the route path value for this record."`
	Icon         string         `bun:"icon" json:"icon" meta:"label:Icon,help:Stores the icon value for this record."`
	Action       map[string]any `bun:"action,type:jsonb,nullzero" json:"action" meta:"kind:json,label:Action,help:Stores the action payload in JSON format."`
	// Empty means visible to everyone who passes the model ACL; otherwise only callers holding one of these groups (implied groups included) or a superadmin see the entry.
	GroupBIDs []string `bun:"group_bids,type:jsonb,nullzero" json:"group_bids" meta:"kind:json,label:Group BIDs,help:Restricts this menu to callers holding at least one of these group BIDs. Leave empty to show it to everyone with model access."`
	// Keep the statusbar hint on the model so the ui.menu form
	// view can render it, while stored:false keeps it out of the SQL projection.
	StatusbarVisible string `bun:"statusbar_visible" json:"statusbar_visible" meta:"compute:_compute_statusbar_visible,stored:false,label:Statusbar Visible,help:Stores the computed value for statusbar visible."`
}

const UIMenuModelName = "ui.menu"

var UIMenuDefinition = orm.ModelDefinition(UIMenuModelName, (*UIMenuRecord)(nil))

var UIMenuModelSpec = orm.ModelSpec{
	Name:        UIMenuModelName,
	Label:       "UI Menu",
	Description: "Sidebar menu registry with URL-only actions",
	Definition:  UIMenuDefinition,
	RuntimeMethods: []orm.RuntimeMethod{
		{Name: "create", Public: true, ModelOnly: true, Call: createUIMenu},
		{Name: "write", Public: true, UsesPathID: true, Call: writeUIMenu},
		{Name: "get_user_roots", Public: true, ModelOnly: true, ReadOnly: true, Call: getUserRootsUIMenu},
		{Name: "load_menus_root", Public: true, ModelOnly: true, ReadOnly: true, Call: loadMenusRootUIMenu},
		{Name: "load_menus", Public: true, ModelOnly: true, ReadOnly: true, Call: loadMenusUIMenu},
	},
}

// UIMenuModel owns the persisted UI menu catalog and the RPC methods used by
// the frontend sidebar bootstrap flow.
// Keep menu customization in composition style by isolating tree rules in
// helper functions and handler options rather than one customize function.
var UIMenuModel = orm.Define[UIMenuRecord](&Models, 160, UIMenuModelSpec)

// These stay function vars (not plain funcs) so menu tests can stub composed
// action-dict loading without a database. They bind directly to the named
// loaders below: runtime CRUD now flows through the shared ORM repository
// helpers, so no init()-time rebinding or "not initialized" fallback is needed.
var (
	uiMenuLoadWindowAction = loadUIMenuWindowActionDict
	uiMenuLoadClientAction = loadUIMenuClientActionDict
)

// loadUIMenuWindowActionDict loads one ui.actions.window row and expands it
// into the canonical action dict used by the sidebar payload.
func loadUIMenuWindowActionDict(ctx context.Context, actionID int64) (map[string]any, error) {
	row, err := loadUIActionWindowRow(ctx, actionID)
	if err != nil {
		return nil, err
	}
	return buildUIActionWindowDict(ctx, row)
}

// loadUIMenuClientActionDict loads one ui.actions.client row and normalizes it
// into the canonical client-action dict used by the sidebar payload.
func loadUIMenuClientActionDict(ctx context.Context, actionID int64) (map[string]any, error) {
	row, err := loadUIActionClientRow(ctx, actionID)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"type":      UIActionClientModelName,
		"id":        int64Value(row["id"]),
		"name":      firstNonEmpty(strings.TrimSpace(stringValue(row["name"])), strings.TrimSpace(stringValue(row["tag"])), "Client Action"),
		"tag":       strings.TrimSpace(stringValue(row["tag"])),
		"target":    firstNonEmpty(strings.TrimSpace(stringValue(row["target"])), "current"),
		"sys_model": strings.TrimSpace(stringValue(row["sys_model"])),
		"context":   normalizeUIActionContextForOutput(row["context"]),
		"params":    normalizeUIActionContextForOutput(row["params"]),
	}, nil
}

func init() {
	// Expose the legacy frontend-only view hint through runtime enrichment
	// without requiring a physical PostgreSQL column on ui_menu; startup
	// compute validation (SRS-F-018) fails fast when this registration is missing.
	orm.RegisterCompute(UIMenuModelName, "statusbar_visible", computeUIMenuStatusbarVisible)
	registerFrontendBIDConstraint(UIMenuModelName)
}

// computeUIMenuStatusbarVisible computes the ui menu statusbar visible handled by this package.
func computeUIMenuStatusbarVisible(_ context.Context, records orm.RecordSet, _ orm.FieldMetadata) (map[string]any, error) {
	values := make(map[string]any, records.Len())
	// Walk each ids() entry in order so validation and side effects stay scoped to the current element.
	for _, id := range records.IDs() {
		// The compatibility field is optional UI metadata, so default to the
		// empty string when no persisted statusbar visibility is defined.
		values[id] = ""
	}
	return values, nil
}

type uiMenuTreeRow struct {
	ID           int64          `json:"id"`
	Name         string         `json:"name"`
	Active       bool           `json:"active"`
	Sequence     int64          `json:"sequence"`
	ParentID     *int64         `json:"parent_id"`
	CompleteName string         `json:"complete_name"`
	RoutePath    string         `json:"route_path"`
	Icon         string         `json:"icon"`
	Action       map[string]any `json:"action"`
	UpdatedAt    time.Time      `json:"updated_at"`
	// GroupBIDs is a server-side visibility input only; it never reaches the sidebar payload
	// (nodes are built field by field in buildUIMenuSidebarTree). It is serialized only
	// so the shared base.kvt cache round-trips it.
	GroupBIDs []string `json:"group_bids"`
}

// createUIMenu creates the ui menu handled by this package.
func createUIMenu(ctx context.Context, model *orm.ModelMetadata, _ []string, args []any, kwargs map[string]any) (any, error) {
	// complete_name derivation reads parent names as plain strings; a request
	// without lang would otherwise receive the translation envelope.
	ctx = orm.EnsureContextLang(ctx, "")
	// Accept the same positional or keyword payload shapes used by the generic
	// runtime create path so callers do not need a menu-specific transport.
	values, err := recordMapFromArgs(args, kwargs, "values")
	if err != nil {
		return nil, err
	}
	if values == nil {
		log.Errorf("ui.menu.create requires values")
		return nil, fmt.Errorf("ui.menu.create requires values")
	}

	// Normalize once before insert so stored rows always keep the same URL-only
	// action representation regardless of caller payload quirks.
	normalized, err := normalizeUIMenuValues(ctx, values, "", true)
	if err != nil {
		return nil, err
	}
	// Raw insert on purpose: the form view's create posts every field, including the
	// non-stored computed statusbar_visible, which CreateRecord rejects. ui.menu has no
	// company_id, and the T15 dispatcher guard covers record rules.
	created, err := orm.RuntimeRepositoryCreate(ctx, orm.RuntimeModelRef(UIMenuModelName, model), normalized)
	if err != nil {
		return nil, err
	}

	// Recompute the branch after insert so complete_name always reflects the
	// resolved parent chain stored in the orm.
	createdID, ok := int64FromAny(created["id"])
	if !ok {
		return created, nil
	}
	if err := syncUIMenuBranch(ctx, createdID); err != nil {
		return nil, err
	}
	// Keep menu writes available even if the notification path is temporarily
	// down: the next sidebar bootstrap still picks up the persisted menu state.
	if err := publishUIMenuUpdated(ctx); err != nil {
		log.Warnf("ui.menu.create publish update: %v", err)
	}
	return created, nil
}

func writeUIMenu(ctx context.Context, model *orm.ModelMetadata, ids []string, args []any, kwargs map[string]any) (any, error) {
	// Same plain-string requirement as createUIMenu for name/parent derivation.
	ctx = orm.EnsureContextLang(ctx, "")
	// Writes are only valid for concrete menu ids because complete-name and
	// parent-cycle checks depend on the current stored rows.
	if len(ids) == 0 {
		log.Errorf("ui.menu.write requires ids")
		return nil, fmt.Errorf("ui.menu.write requires ids")
	}
	values, err := recordMapFromArgs(args, kwargs, "values")
	if err != nil {
		return nil, err
	}
	if values == nil {
		log.Errorf("ui.menu.write requires values")
		return nil, fmt.Errorf("ui.menu.write requires values")
	}

	// Load the first row so merge-style normalization can preserve the current
	// URL action when the caller updates only non-action fields.
	currentRows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(UIMenuModelName, model), []string{ids[0]}, []string{"action", "route_path"})
	if err != nil {
		return nil, err
	}
	currentAction := ""
	currentRoutePath := ""
	if len(currentRows) > 0 {
		currentAction = canonicalActionURL(currentRows[0]["action"])
		currentRoutePath = strings.TrimSpace(stringValue(currentRows[0]["route_path"]))
	}

	// Validate the parent move before persisting it so callers get a clear error
	// instead of a silently corrupted recursive menu tree.
	if parentID, changed, err := optionalInt64FromRecord(values, "parent_id"); err != nil {
		return nil, err
	} else if changed {
		for _, id := range ids {
			if err := ensureUIMenuParentAllowed(ctx, id, parentID); err != nil {
				return nil, err
			}
		}
	}

	// Normalize with the current persisted route/action so partial updates keep
	// the menu leaf behavior stable.
	normalized, err := normalizeUIMenuValues(ctx, values, firstNonEmpty(currentAction, currentRoutePath), false)
	if err != nil {
		return nil, err
	}
	written, err := orm.WriteRecords(ctx, orm.RuntimeModelRef(UIMenuModelName, model), ids, normalized)
	if err != nil {
		return nil, err
	}

	// Refresh every touched branch after a write because name or parent changes
	// alter complete_name for the whole subtree.
	for _, id := range ids {
		parsedID, ok := int64FromAny(id)
		if !ok {
			log.Errorf("ui.menu invalid id %q", id)
			return nil, fmt.Errorf("ui.menu invalid id %q", id)
		}
		if err := syncUIMenuBranch(ctx, parsedID); err != nil {
			return nil, err
		}
	}
	// Publish after all branches reconcile so listeners reload only the final
	// canonical tree and not an intermediate parent/name state.
	if err := publishUIMenuUpdated(ctx); err != nil {
		log.Warnf("ui.menu.write publish update: %v", err)
	}
	return written, nil
}

// getUserRootsUIMenu returns the ids of the top-level menus the caller actually
// sees. It derives them from the same tree load_menus serves, so a root that
// load_menus auto-hides (a group header with no visible children left) is not
// advertised here either.
func getUserRootsUIMenu(ctx context.Context, _ *orm.ModelMetadata, _ []string, _ []any, _ map[string]any) (any, error) {
	rows, hadChildren, err := fetchUIMenuRows(ctx)
	if err != nil {
		return nil, err
	}
	tree := buildUIMenuSidebarTree(rows, hadChildren)
	rootIDs := make([]int64, 0, len(tree))
	for _, node := range tree {
		if id, ok := node["id"].(int64); ok {
			rootIDs = append(rootIDs, id)
		}
	}
	return rootIDs, nil
}

// loadMenusRootUIMenu loads the menus root ui menu handled by this package.
func loadMenusRootUIMenu(ctx context.Context, _ *orm.ModelMetadata, _ []string, _ []any, _ map[string]any) (any, error) {
	// Keep the root payload shape aligned with load_menus so the frontend can
	// reuse one normalization path for bootstrap and refresh flows.
	rows, hadChildren, err := fetchUIMenuRows(ctx)
	if err != nil {
		return nil, err
	}
	return buildUIMenuSidebarPayload(ctx, rows, hadChildren), nil
}

// loadMenusUIMenu loads the menus ui menu handled by this package.
func loadMenusUIMenu(ctx context.Context, _ *orm.ModelMetadata, _ []string, _ []any, _ map[string]any) (any, error) {
	// Serve the sidebar tree in one RPC so the shell can bootstrap without
	// client-side joins across search_read and action resolution calls.
	return loadMenusRootUIMenu(ctx, nil, nil, nil, nil)
}

// fetchUIMenuRows also reports, per parent id, whether that parent had at
// least one child row before ACL/company/DB filtering ran — so
// buildUIMenuSidebarTree can tell a group header that lost every child from
// one that never had any, and auto-hide only the former.
func fetchUIMenuRows(ctx context.Context) ([]uiMenuTreeRow, map[int64]bool, error) {
	// Menu rows feed the sidebar payload as plain strings, so pin a language
	// when the caller sent none (the cache is keyed per language).
	ctx = orm.EnsureContextLang(ctx, "")
	// The resolved rows are the same for every caller and come from the shared
	// base.kvt cache when it is current (see ui_menu_cache.go); everything below
	// is per-caller and always runs.
	menuRows, hadChildren, err := loadUIMenuResolvedRows(ctx)
	if err != nil {
		return nil, nil, err
	}

	// Resolve the caller's groups once per load, and only when some menu is group-restricted.
	groupFilter, err := newUIMenuGroupFilter(ctx, menuRows)
	if err != nil {
		return nil, nil, err
	}

	out := make([]uiMenuTreeRow, 0, len(menuRows))
	for _, menuRow := range menuRows {
		if !allowUIMenuRowForCurrentDB(ctx, menuRow) ||
			!allowUIMenuRowForRootCompany(ctx, menuRow) ||
			!groupFilter.allows(menuRow) ||
			!allowUIMenuRowForCurrentUser(ctx, menuRow) {
			continue
		}
		out = append(out, menuRow)
	}
	return out, hadChildren, nil
}

// readUIMenuTreeRows reads every active menu row and records which parents had
// children before any row gets dropped, here or by the per-caller gates, so a
// header that lost every child can be told apart from one that never had any.
func readUIMenuTreeRows(ctx context.Context) ([]uiMenuTreeRow, map[int64]bool, error) {
	// Read only the columns needed by sidebar rendering so menu boot remains
	// deterministic and cheap even as the table grows.
	rows, err := orm.RuntimeRepositorySearchRead(
		ctx, orm.RuntimeModelRef(UIMenuModelName, nil),
		orm.RecordMap{"active": true, "__order": "sequence asc, id asc"},
		[]string{"id", "name", "active", "sequence", "parent_id", "complete_name", "route_path", "icon", "action", "group_bids", "updated_at"},
	)
	if err != nil {
		return nil, nil, err
	}
	menuRows := make([]uiMenuTreeRow, 0, len(rows))
	for _, row := range rows {
		menuRow, err := treeRowFromRecord(row)
		if err != nil {
			return nil, nil, err
		}
		menuRows = append(menuRows, menuRow)
	}
	hadChildren := make(map[int64]bool, len(menuRows))
	for _, menuRow := range menuRows {
		if menuRow.ParentID != nil {
			hadChildren[*menuRow.ParentID] = true
		}
	}
	return menuRows, hadChildren, nil
}

// resolveUIMenuRows resolves each row's action. A row whose action cannot be
// resolved is dropped (its parent then auto-hides if it was the last child) so
// one dangling reference never takes the whole sidebar down.
func resolveUIMenuRows(ctx context.Context, menuRows []uiMenuTreeRow) ([]uiMenuTreeRow, error) {
	resolvedRows := make([]uiMenuTreeRow, 0, len(menuRows))
	for _, menuRow := range menuRows {
		resolved, err := resolveUIMenuSidebarAction(ctx, menuRow.Action)
		if err != nil {
			// Cancellation is a real failure; anything else hides just this row.
			if ctx.Err() != nil {
				return nil, err
			}
			log.Warnf("ui.menu %d hidden: cannot resolve action: %v", menuRow.ID, err)
			continue
		}
		if len(resolved) > 0 {
			menuRow.Action = resolved
			if strings.TrimSpace(menuRow.RoutePath) == "" {
				menuRow.RoutePath = canonicalActionURL(resolved)
			}
		}
		resolvedRows = append(resolvedRows, menuRow)
	}
	return resolvedRows, nil
}

// buildUIMenuResolvedRows is the cache-fill path: every active row, resolved,
// independent of the caller (it runs as sudo).
func buildUIMenuResolvedRows(ctx context.Context) ([]uiMenuTreeRow, map[int64]bool, error) {
	menuRows, hadChildren, err := readUIMenuTreeRows(ctx)
	if err != nil {
		return nil, nil, err
	}
	resolved, err := resolveUIMenuRows(ctx, menuRows)
	if err != nil {
		return nil, nil, err
	}
	return resolved, hadChildren, nil
}

// buildUIMenuCallerRows is the no-cache path (cache unavailable, or ui.menu has
// per-caller record rules): it reads as the caller and resolves actions only for
// rows that survive the gates that need no query, so rows the caller would not
// see never pay for action resolution. fetchUIMenuRows re-applies every gate
// afterwards, which is idempotent.
func buildUIMenuCallerRows(ctx context.Context) ([]uiMenuTreeRow, map[int64]bool, error) {
	menuRows, hadChildren, err := readUIMenuTreeRows(ctx)
	if err != nil {
		return nil, nil, err
	}
	groupFilter, err := newUIMenuGroupFilter(ctx, menuRows)
	if err != nil {
		return nil, nil, err
	}
	candidates := make([]uiMenuTreeRow, 0, len(menuRows))
	for _, menuRow := range menuRows {
		if allowUIMenuRowForCurrentDB(ctx, menuRow) && allowUIMenuRowForRootCompany(ctx, menuRow) && groupFilter.allows(menuRow) {
			candidates = append(candidates, menuRow)
		}
	}
	resolved, err := resolveUIMenuRows(ctx, candidates)
	if err != nil {
		return nil, nil, err
	}
	return resolved, hadChildren, nil
}

// uiMenuGroupFilter answers whether the caller's groups satisfy a menu's group_bids restriction.
type uiMenuGroupFilter struct {
	unrestricted bool
	held         map[string]struct{}
}

// newUIMenuGroupFilter skips the group lookup entirely when no menu is restricted, and treats
// sudo/superadmin callers and a missing environment as unrestricted.
func newUIMenuGroupFilter(ctx context.Context, rows []uiMenuTreeRow) (uiMenuGroupFilter, error) {
	restricted := false
	for _, row := range rows {
		if len(row.GroupBIDs) > 0 {
			restricted = true
			break
		}
	}
	env := orm.EnvironmentFromContext(ctx)
	if !restricted || env == nil || env.IsSudo() {
		return uiMenuGroupFilter{unrestricted: true}, nil
	}
	bids, err := orm.CallerGroupBIDs(ctx)
	if err != nil {
		return uiMenuGroupFilter{}, err
	}
	held := make(map[string]struct{}, len(bids))
	for _, bid := range bids {
		held[bid] = struct{}{}
	}
	return uiMenuGroupFilter{held: held}, nil
}

func (f uiMenuGroupFilter) allows(row uiMenuTreeRow) bool {
	if f.unrestricted || len(row.GroupBIDs) == 0 {
		return true
	}
	for _, bid := range row.GroupBIDs {
		if _, ok := f.held[bid]; ok {
			return true
		}
	}
	return false
}

// allowUIMenuRowForCurrentUser hides menu entries the caller cannot actually
// read, so the sidebar never advertises a link that will fail with a
// permission-denied error the moment the user opens it. Menus that don't
// resolve to a concrete model (URL-only entries, folders) are left visible.
// A menu pointing at a model that is no longer registered (a seeded row left
// behind after its module dropped the model, e.g. edc.device.group) is hidden:
// the link can only 404, and probing it through EnsureRecordReadAccess would
// log an error on every menu load/reinit. An explicit ACL/record-rule denial
// hides the row too; any other lookup error is left visible rather than
// silently swallowed, since that's an integrity problem, not an access decision.
func allowUIMenuRowForCurrentUser(ctx context.Context, row uiMenuTreeRow) bool {
	resModel := strings.TrimSpace(stringValue(row.Action["sys_model"]))
	if resModel == "" {
		return true
	}
	if _, registered := orm.LookupModel(resModel); !registered {
		return false
	}
	err := orm.EnsureRecordReadAccess(ctx, resModel, nil)
	if err == nil {
		return true
	}
	return !isUIMenuAccessDenied(err)
}

// isUIMenuAccessDenied recognises an ACL/record-rule denial: the typed
// ErrForbidden code, or the legacy untyped "permission denied: ..." errors the
// access layer still emits in places.
func isUIMenuAccessDenied(err error) bool {
	var typed *orm.Error
	if errors.As(err, &typed) && typed.Code == orm.ErrForbidden {
		return true
	}
	return strings.HasPrefix(strings.TrimSpace(err.Error()), "permission denied")
}

// buildUIMenuSidebarPayload keeps the bootstrap payload and live-refresh
// payload on the same contract so the frontend can normalize both paths once.
func buildUIMenuSidebarPayload(ctx context.Context, rows []uiMenuTreeRow, hadChildren map[int64]bool) map[string]any {
	scopeKey := uiMenuScopeKey(ctx)
	version := uiMenuVersion(rows)
	return map[string]any{
		"menu_scope_key": scopeKey,
		"scope_key":      scopeKey,
		"menu_version":   version,
		"menu_cache_key": uiMenuCacheKey(scopeKey, version, rows),
		"records":        buildUIMenuSidebarTree(rows, hadChildren),
	}
}

// publishUIMenuUpdated emits one global bus event telling every connected
// sidebar to refetch. The event is deliberately not scoped: it goes to all
// users, while a menu tree, scope key and cache key are per db/user/company, so
// copying the writer's values would hand other callers a key that means nothing
// for them. Each client reloads load_menus and compares its own cache key.
// menu_version is only a monotonic stamp so listeners can drop out-of-order
// deliveries.
func publishUIMenuUpdated(ctx context.Context) error {
	eventPayload := map[string]any{
		"menu_version": time.Now().UTC().UnixNano(),
	}
	_, err := orm.ExecuteRuntimeMethod(
		ctx,
		// base.service.bus lives in modules/base-bus, out of reach for this
		// internal/ package under the cross-import rule (internal must not
		// import modules/*) — looked up by name through the runtime registry
		// instead, same as any other cross-module model reference. Keep these
		// literals in sync with modules/base-bus/models/service_bus.go's
		// ServiceBusModelName and ServiceBusGlobalChannel().
		"base.service.bus",
		"publish",
		nil,
		[]any{[]string{"servicebus:global"}, "ui.menu.updated", eventPayload},
		nil,
	)
	return err
}

// uiMenuScopeKey binds the cache namespace to the authenticated DB/user/company
// scope so cached trees cannot bleed across tenants or active company changes.
func uiMenuScopeKey(ctx context.Context) string {
	env := orm.EnvironmentFromContext(ctx)
	if env == nil {
		return UIMenuModelName
	}
	dbName := strings.TrimSpace(env.DBName())
	if dbName == "" {
		dbName = strings.TrimSpace(config.HomeDBName())
	}
	currentCompanyID, err := env.CompanyID()
	if err != nil {
		currentCompanyID = 0
	}
	allowedCompanyIDs, err := env.AllowedCompanyIDs()
	if err != nil {
		allowedCompanyIDs = nil
	}
	return fmt.Sprintf(
		"%s:db=%s:user=%d:company=%d:allowed=%s",
		UIMenuModelName,
		dbName,
		env.UserID(),
		currentCompanyID,
		int64SliceCachePart(allowedCompanyIDs),
	)
}

// uiMenuVersion tracks the newest persisted menu timestamp so refresh events
// can reject out-of-order deliveries without diffing the whole menu tree.
func uiMenuVersion(rows []uiMenuTreeRow) int64 {
	version := int64(0)
	for _, row := range rows {
		updatedAt := row.UpdatedAt.UTC().UnixNano()
		if updatedAt > version {
			version = updatedAt
		}
	}
	return version
}

// uiMenuCacheKey combines scope, revision, row count and a fingerprint of the
// visible row ids. The fingerprint is what notices a permission change (group
// membership, ACL, company) that reveals or hides rows without touching any
// ui.menu updated_at, which neither the version nor an equal-sized swap of rows
// would show.
func uiMenuCacheKey(scopeKey string, version int64, rows []uiMenuTreeRow) string {
	return fmt.Sprintf("%s:v%d:n%d:h%x", scopeKey, version, len(rows), uiMenuRowsFingerprint(rows))
}

// uiMenuRowsFingerprint hashes the visible row ids order-independently.
func uiMenuRowsFingerprint(rows []uiMenuTreeRow) uint64 {
	ids := make([]int64, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	h := fnv.New64a()
	var buf [8]byte
	for _, id := range ids {
		binary.LittleEndian.PutUint64(buf[:], uint64(id))
		_, _ = h.Write(buf[:])
	}
	return h.Sum64()
}

// int64SliceCachePart keeps cache-scope serialization stable and human-readable
// for debugging across both tests and browser/localStorage inspection.
func int64SliceCachePart(values []int64) string {
	if len(values) == 0 {
		return "none"
	}
	parts := make([]string, 0, len(values))
	for _, value := range values {
		parts = append(parts, strconv.FormatInt(value, 10))
	}
	return strings.Join(parts, ",")
}

func allowUIMenuRowForCurrentDB(ctx context.Context, row uiMenuTreeRow) bool {
	env := orm.EnvironmentFromContext(ctx)
	if env == nil {
		return true
	}
	currentDBName := strings.TrimSpace(env.DBName())
	if currentDBName == "" || currentDBName == strings.TrimSpace(config.HomeDBName()) {
		return true
	}
	if strings.TrimSpace(row.RoutePath) == "/web/base.database" {
		return false
	}
	if strings.TrimSpace(stringValue(row.Action["sys_model"])) == "base.database" {
		return false
	}
	// The service health page reports control-plane process state (DB/Kafka/MQTT
	// connectivity, uptime) that isn't meaningful per tenant, same reasoning as
	// base.database above.
	if strings.TrimSpace(row.RoutePath) == "/web/system-health" {
		return false
	}
	return true
}

// adminMenuRootName must stay in sync with the "Admin" menuitem name seeded in
// modules/base/views/menu-base.xml. ui.menu has no company_id column, so
// this is a name-based match rather than a stored scope field.
// TestAdminMenuRootNameMatchesSeed pins it to the seed, so renaming the menu
// fails CI instead of silently exposing the Admin tree to every company.
const adminMenuRootName = "Admin"

// rootCompanyID is the one company allowed to see and operate the global
// system-configuration screens (models, ACLs, views, actions). Those objects
// have no per-company isolation of their own, so any other company editing
// them would corrupt state shared by every tenant.
const rootCompanyID int64 = 1

// allowUIMenuRowForRootCompany hides the "Admin" menu and all of its
// descendants for any company other than rootCompanyID. This is sidebar-only
// visibility; the actual access boundary is enforced at the model layer (see
// sys_admin_company_access.go) so a hidden menu can't be bypassed by calling
// the underlying model's RPC methods directly.
func allowUIMenuRowForRootCompany(ctx context.Context, row uiMenuTreeRow) bool {
	if row.CompleteName != adminMenuRootName && !strings.HasPrefix(row.CompleteName, adminMenuRootName+" / ") {
		return true
	}
	env := orm.EnvironmentFromContext(ctx)
	if env == nil {
		return true
	}
	companyID, err := env.CompanyID()
	if err != nil {
		return true
	}
	return companyID == rootCompanyID
}

// resolveUIMenuSidebarAction resolves the ui menu sidebar action handled by this package.
func resolveUIMenuSidebarAction(ctx context.Context, action map[string]any) (map[string]any, error) {
	if len(action) == 0 {
		return nil, nil
	}
	switch strings.TrimSpace(stringValue(action["type"])) {
	case "", "ui.actions.url":
		return action, nil
	case UIActionWindowModelName:
		return resolveUIMenuReferencedAction(ctx, action, UIActionWindowModelName, uiMenuLoadWindowAction)
	case UIActionClientModelName:
		return resolveUIMenuReferencedAction(ctx, action, UIActionClientModelName, uiMenuLoadClientAction)
	default:
		return action, nil
	}
}

// resolveUIMenuReferencedAction resolves the ui menu referenced action handled by this package.
func resolveUIMenuReferencedAction(ctx context.Context, action map[string]any, expectedModel string, load func(context.Context, int64) (map[string]any, error)) (map[string]any, error) {
	if id, ok := int64FromAny(action["id"]); ok && id > 0 {
		resolved, err := load(ctx, id)
		if err != nil {
			return nil, err
		}
		return mergeResolvedMenuAction(action, resolved), nil
	}
	bidValue := strings.TrimSpace(stringValue(action["bid"]))
	if bidValue == "" {
		return action, nil
	}
	targetID, err := lookupUIMenuActionTargetID(ctx, bidValue, expectedModel)
	if err != nil {
		return nil, err
	}
	if targetID <= 0 {
		return action, nil
	}
	resolved, err := load(ctx, targetID)
	if err != nil {
		return nil, err
	}
	return mergeResolvedMenuAction(action, resolved), nil
}

// lookupUIMenuActionTargetID looks up the ui menu action target id handled by this package.
func lookupUIMenuActionTargetID(ctx context.Context, bidValue, expectedModel string) (int64, error) {
	rows, err := orm.RuntimeRepositorySearchRead(ctx, orm.RuntimeModelRef(SysModelDataModelName, nil), orm.RecordMap{"complete_name": bidValue}, []string{"model", "model_id"})
	if err != nil {
		return 0, err
	}
	if len(rows) == 0 {
		return 0, nil
	}
	modelName := strings.TrimSpace(stringValue(rows[0]["model"]))
	if expectedModel != "" && modelName != expectedModel {
		log.Errorf("ui.menu action %q points to model %q, expected %q", bidValue, modelName, expectedModel)
		return 0, fmt.Errorf("ui.menu action %q points to model %q, expected %q", bidValue, modelName, expectedModel)
	}
	targetID, _ := int64FromAny(rows[0]["model_id"])
	return targetID, nil
}

func mergeResolvedMenuAction(source, resolved map[string]any) map[string]any {
	out := make(map[string]any, len(source)+len(resolved))
	for key, value := range source {
		out[key] = value
	}
	for key, value := range resolved {
		out[key] = value
	}
	if url := strings.TrimSpace(stringValue(out["url"])); url == "" {
		if resModel := strings.TrimSpace(stringValue(out["sys_model"])); resModel != "" && strings.TrimSpace(stringValue(out["type"])) == UIActionWindowModelName {
			out["url"] = windowActionRoutePath(resModel)
		}
	}
	return out
}

func windowActionRoutePath(resModel string) string {
	resModel = strings.TrimSpace(resModel)
	if resModel == "" {
		return ""
	}
	return "/web/" + resModel
}

// computeUIMenuCompleteName computes the ui menu complete name handled by this package.
func computeUIMenuCompleteName(ctx context.Context, name string, parentID *int64) (string, error) {
	// Root menus store their plain name so the sidebar keeps concise labels.
	if parentID == nil || *parentID <= 0 {
		return name, nil
	}

	// Parent complete_name is reused so descendants do not need to rebuild the
	// full chain on every read.
	rows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(UIMenuModelName, nil), []string{strconv.FormatInt(*parentID, 10)}, []string{"id", "complete_name", "name"})
	if err != nil {
		return "", err
	}
	if len(rows) == 0 {
		log.Errorf("ui.menu parent %d not found", *parentID)
		return "", fmt.Errorf("ui.menu parent %d not found", *parentID)
	}
	parentName := strings.TrimSpace(stringValue(rows[0]["complete_name"]))
	if parentName == "" {
		parentName = strings.TrimSpace(stringValue(rows[0]["name"]))
	}
	if parentName == "" {
		return name, nil
	}
	return parentName + " / " + name, nil
}

// ensureUIMenuParentAllowed ensures the ui menu parent allowed handled by this package.
func ensureUIMenuParentAllowed(ctx context.Context, recordID string, parentID *int64) error {
	// Nil or zero parent means promoting the node to a root menu, which is safe.
	if parentID == nil || *parentID <= 0 {
		return nil
	}
	currentID, err := strconv.ParseInt(strings.TrimSpace(recordID), 10, 64)
	if err != nil {
		log.Errorf("ui.menu invalid id %q", recordID)
		return fmt.Errorf("ui.menu invalid id %q", recordID)
	}
	if currentID == *parentID {
		log.Errorf("ui.menu cannot be its own parent")
		return fmt.Errorf("ui.menu cannot be its own parent")
	}

	// Walk the proposed parent's ancestor chain and reject cycles eagerly before
	// the write lands in storage.
	nextID := *parentID
	seen := map[int64]struct{}{currentID: {}}
	for nextID > 0 {
		if _, exists := seen[nextID]; exists {
			log.Errorf("ui.menu recursive parent assignment detected")
			return fmt.Errorf("ui.menu recursive parent assignment detected")
		}
		seen[nextID] = struct{}{}
		rows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(UIMenuModelName, nil), []string{strconv.FormatInt(nextID, 10)}, []string{"parent_id"})
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			log.Errorf("ui.menu parent %d not found", nextID)
			return fmt.Errorf("ui.menu parent %d not found", nextID)
		}
		parentValue, ok, err := optionalInt64FromAny(rows[0]["parent_id"])
		if err != nil {
			return err
		}
		if !ok {
			break
		}
		nextID = *parentValue
	}
	return nil
}

// syncUIMenuBranch synchronizes the ui menu branch handled by this package.
func syncUIMenuBranch(ctx context.Context, rootID int64) error {
	// Load the branch row first so recursive rewrites use the latest stored
	// parent and name values after create/write completed.
	rows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(UIMenuModelName, nil), []string{strconv.FormatInt(rootID, 10)}, []string{"id", "name", "parent_id"})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	name := strings.TrimSpace(stringValue(rows[0]["name"]))
	parentID, _, err := optionalInt64FromAny(rows[0]["parent_id"])
	if err != nil {
		return err
	}
	completeName, err := computeUIMenuCompleteName(ctx, name, parentID)
	if err != nil {
		return err
	}
	if _, err := orm.RuntimeRepositoryWrite(ctx, orm.RuntimeModelRef(UIMenuModelName, nil), []string{strconv.FormatInt(rootID, 10)}, orm.RecordMap{"complete_name": completeName}); err != nil {
		return err
	}

	// Recurse into direct children so every descendant reflects the branch move
	// or rename that triggered the sync.
	children, err := orm.RuntimeRepositorySearchRead(
		ctx, orm.RuntimeModelRef(UIMenuModelName, nil),
		orm.RecordMap{"parent_id": []any{rootID}, "__order": "sequence asc, id asc"},
		[]string{"id"},
	)
	if err != nil {
		return err
	}
	for _, child := range children {
		childID, ok := int64FromAny(child["id"])
		if !ok {
			continue
		}
		if err := syncUIMenuBranch(ctx, childID); err != nil {
			return err
		}
	}
	return nil
}

// buildUIMenuSidebarTree builds the ui menu sidebar tree handled by this package.
func buildUIMenuSidebarTree(rows []uiMenuTreeRow, hadChildrenBeforeFilter map[int64]bool) []map[string]any {
	// Index rows by id and parent so the final payload keeps a predictable
	// hierarchy and sibling order without extra database round-trips.
	childrenByParent := map[int64][]uiMenuTreeRow{}
	for _, row := range rows {
		parentKey := int64(0)
		if row.ParentID != nil {
			parentKey = *row.ParentID
		}
		childrenByParent[parentKey] = append(childrenByParent[parentKey], row)
	}
	for parentID := range childrenByParent {
		sort.SliceStable(childrenByParent[parentID], func(i, j int) bool {
			if childrenByParent[parentID][i].Sequence == childrenByParent[parentID][j].Sequence {
				return childrenByParent[parentID][i].ID < childrenByParent[parentID][j].ID
			}
			return childrenByParent[parentID][i].Sequence < childrenByParent[parentID][j].Sequence
		})
	}

	// Recursively materialize the tree into the flattened map payload consumed
	// by the existing frontend menu adapter. Only rows reachable from the root
	// (key 0) are emitted: a row whose parent was filtered out earlier (ACL,
	// company, DB, group) is never visited, so hiding a parent hides its whole
	// subtree even if the children themselves would have passed the gates.
	var build func(parentID int64) []map[string]any
	build = func(parentID int64) []map[string]any {
		nodes := make([]map[string]any, 0, len(childrenByParent[parentID]))
		for _, row := range childrenByParent[parentID] {
			url := strings.TrimSpace(row.RoutePath)
			if url == "" {
				url = canonicalActionURL(row.Action)
			}
			resolvedURL := firstNonEmpty(url, "#")
			children := build(row.ID)
			// A menu that grouped submenus but has none left visible (every
			// child was filtered out by ACL/company/DB rules) is a dead group
			// header with nothing to show — auto-hide it, unless it also
			// carries its own action/route, in which case it still functions
			// as a plain leaf link.
			if len(children) == 0 && hadChildrenBeforeFilter[row.ID] && resolvedURL == "#" {
				continue
			}
			node := map[string]any{
				"id":       row.ID,
				"name":     row.Name,
				"url":      resolvedURL,
				"sequence": row.Sequence,
				"section":  uiMenuSidebarSection(row),
				"icon":     uiMenuSidebarIcon(row),
			}
			if row.Action != nil {
				node["action"] = row.Action
			}
			if len(children) > 0 {
				node["child_ids"] = children
			}
			nodes = append(nodes, node)
		}
		return nodes
	}
	return build(0)
}

func uiMenuSidebarSection(row uiMenuTreeRow) string {
	// Match the current frontend heuristics so menus group into stable sidebar
	// sections without requiring extra authoring metadata for now.
	name := strings.ToLower(strings.TrimSpace(row.Name))
	route := strings.ToLower(strings.TrimSpace(firstNonEmpty(row.RoutePath, canonicalActionURL(row.Action))))
	icon := strings.ToLower(strings.TrimSpace(row.Icon))
	switch {
	case strings.Contains(name, "setting"), strings.Contains(route, "setting"), icon == "settings-2", icon == "search":
		return "settings"
	case strings.Contains(name, "report"), strings.Contains(name, "document"), strings.Contains(name, "library"), icon == "database", icon == "file":
		return "admin"
	default:
		return "main"
	}
}

func uiMenuSidebarIcon(row uiMenuTreeRow) string {
	// Default to the existing file icon so the UI never renders an empty slot.
	icon := strings.TrimSpace(row.Icon)
	if icon == "" {
		return "file"
	}
	return icon
}

func treeRowFromRecord(record orm.RecordMap) (uiMenuTreeRow, error) {
	// Convert the generic runtime record into a typed helper row so tree
	// building stays straightforward and local.
	id, ok := int64FromAny(record["id"])
	if !ok {
		log.Errorf("ui.menu row is missing id")
		return uiMenuTreeRow{}, fmt.Errorf("ui.menu row is missing id")
	}
	parentID, _, err := optionalInt64FromAny(record["parent_id"])
	if err != nil {
		return uiMenuTreeRow{}, err
	}
	row := uiMenuTreeRow{
		ID:           id,
		Name:         strings.TrimSpace(stringValue(record["name"])),
		Active:       boolValue(record["active"]),
		Sequence:     int64Value(record["sequence"]),
		ParentID:     parentID,
		CompleteName: strings.TrimSpace(stringValue(record["complete_name"])),
		RoutePath:    strings.TrimSpace(stringValue(record["route_path"])),
		Icon:         strings.TrimSpace(stringValue(record["icon"])),
		UpdatedAt:    timeValue(record["updated_at"]),
		GroupBIDs:    normalizeSysRuleGroupBIDs(ruleStringSlice(record["group_bids"])),
	}
	if action, err := actionMapValue(record["action"]); err == nil && len(action) > 0 {
		row.Action = action
	}
	return row, nil
}
