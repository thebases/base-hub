package models

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"

	kernelmodules "git.thebase.vn/base_hub/internal/modules"
	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/queries"
	"git.thebase.vn/base_hub/internal/services/log"
	"github.com/uptrace/bun"
)

var viewXMLDeclarationPattern = regexp.MustCompile(`(?is)^\s*<\?xml\b[^>]*\?>\s*`)

// UIViewCustomRecord stores per-user custom XML overrides for one base view.
// The write path clears these rows when the canonical view arch changes.
type UIViewCustomRecord struct {
	orm.BaseModel
	RefID  int64  `bun:"ref_id" json:"ref_id" meta:"kind:many2one,model:ui.view,ondelete:CASCADE,index,label:Ref ID,help:Links this record to the related ui.view record for ref id."`
	UserID int64  `bun:"user_id" json:"user_id" meta:"kind:many2one,model:base.users,ondelete:CASCADE,index,label:User ID,help:Links this record to the related base.users record for user id."`
	Arch   string `bun:"arch" json:"arch" meta:"kind:html,label:Arch,help:Stores the arch value for this record."`
}

// UIViewRecord stores one XML-authored UI view definition plus the metadata
// needed to resolve inherited views at runtime.
type UIViewRecord struct {
	orm.BaseModel
	Name               string  `bun:"name" json:"name" meta:"index,label:Name,help:Stores the human readable name for this record."`
	Model              string  `bun:"model" json:"model" meta:"index,label:Model,help:Stores the model value for this record."`
	Key                string  `bun:"key" json:"key" meta:"index,label:Key,help:Stores the key value for this record."`
	Priority           int64   `bun:"priority" json:"priority" meta:"label:Priority,help:Stores the numeric value for priority."`
	Type               string  `bun:"type" json:"type" meta:"index,label:Type,help:Stores the type value for this record."`
	Arch               string  `bun:"arch" json:"arch" meta:"kind:html,label:View Architecture,help:This field should be used when accessing view arch. It will use translation. Note that it will read 'arch_db' or 'arch_fs' if in dev-xml mode."`
	ArchBase           string  `bun:"arch_base" json:"arch_base" meta:"kind:html,label:Base View Architecture,help:This field is the same as 'arch' field without translations"`
	ArchDB             string  `bun:"arch_db" json:"arch_db" meta:"kind:html,label:Arch Blob,help:This field stores the view arch."`
	ArchFS             string  `bun:"arch_fs" json:"arch_fs" meta:"kind:html,label:Arch FS,help:Stores the arch fs value for this record."`
	ArchUpdated        bool    `bun:"arch_updated" json:"arch_updated" meta:"kind:html,label:Modified Architecture,help:Controls whether arch updated is enabled for this record."`
	UseChatter         bool    `bun:"use_chatter" json:"use_chatter" meta:"label:Use Chatter,help:When enabled on a form view, docks the discuss chatter panel onto this view without a separate extension view."`
	ArchPrev           string  `bun:"arch_prev" json:"arch_prev" meta:"kind:html,label:Previous View Architecture,help:This field will save the current 'arch_db' before writing on it. Useful to (soft) reset a broken view."`
	InheritID          *int64  `bun:"inherit_id" json:"inherit_id" meta:"kind:many2one,model:ui.view,ondelete:RESTRICT,index,label:Inherit ID,help:Links this record to the related ui.view record for inherit id."`
	InheritChildrenIDs []int64 `bun:"inherit_children_ids,type:jsonb,nullzero" json:"inherit_children_ids" meta:"kind:one2many,model:ui.view,inverse:inherit_id,stored:false,label:Inherit Children IDs,help:Lists the related ui.view records linked back through inherit_id."`
	ModelDataID        *int64  `bun:"model_data_id" json:"model_data_id" meta:"kind:many2one,model:sys.model.data,label:Model Data ID,help:Links this record to the related sys.model.data record for model data id for searching"`
	BID                string  `bun:"bid" json:"bid" meta:"index,label:BID,help:ID of the view defined in xml file."`
	GroupIDs           []int64 `bun:"group_ids,type:jsonb,nullzero" json:"group_ids" meta:"kind:many2many,model:tbl.groups,relation_table:ui_view_group_rel,relation_column:view_id,relation_key:group_id,label:Group IDs,help:Lists the related tbl.groups records associated with this record."`
	Mode               string  `bun:"mode" json:"mode" meta:"index,label:Mode,help:Only applies if this view inherits from an other one (inherit_id is not False/Null).* if extension (default), if this view is requested the closest primary view, is looked up (via inherit_id), then all views inheriting from it with this view's model are applied; if primary, the closest primary view is fully resolved (even if it uses a different model than this one), then this view's inheritance specs (<xpath/>) are applied, and the result is used as if it were this view's actual arch."`
	WarningInfo        string  `bun:"warning_info" json:"warning_info" meta:"label:Warning Info,help:Stores the warning info value for this record."`
	// Keep the statusbar hint on the model so Bun can see the
	// field metadata, while runtime projection still excludes it from SQL.
	StatusbarVisible string         `bun:"statusbar_visible" json:"statusbar_visible" meta:"compute:_compute_statusbar_visible,stored:false,label:Statusbar Visible,help:Stores the computed value for statusbar visible."`
	ModelID          *int64         `bun:"model_id" json:"model_id" meta:"kind:many2one,model:sys.model,label:Model ID,help:Links this record to the related sys.model record for model id."`
	InvalidLocators  map[string]any `bun:"invalid_locators,type:jsonb,nullzero" json:"invalid_locators" meta:"kind:json,label:Invalid Locators,help:Stores the invalid locators payload in JSON format."`
	// Module and Sequence back the type:"dashboard" variant only: a dashboard
	// view describes a whole module's dashboard tab rather than one runtime
	// model, so `model` is left blank on those rows and `module`/`sequence`
	// carry the manifest module name and tab ordering instead. Every other
	// view type leaves these columns empty.
	Module   string `bun:"module" json:"module" meta:"index,label:Module,help:Manifest module name this dashboard view belongs to (type=dashboard only)."`
	Sequence int64  `bun:"sequence" json:"sequence" meta:"label:Sequence,help:Dashboard tab ordering, lower first (type=dashboard only)."`
}

const UIViewModelName = "ui.view"
const UIViewCustomModelName = "ui.view.custom"

var UIViewDefinition = buildUIViewDefinition()
var UIViewCustomDefinition = orm.ModelDefinition(UIViewCustomModelName, (*UIViewCustomRecord)(nil))

var UIViewModelSpec = orm.ModelSpec{
	Name:        UIViewModelName,
	Label:       "UI View",
	Description: "XML-authored UI view registry for SDUI-compatible layouts",
	Definition:  UIViewDefinition,
	RuntimeMethods: []orm.RuntimeMethod{
		{Name: "create", Public: true, ModelOnly: true, Call: createUIView},
		{Name: "read", Public: true, ReadOnly: true, UsesPathID: true, Call: readUIView},
		{Name: "search_read", Public: true, ModelOnly: true, ReadOnly: true, Call: searchReadUIView},
		{Name: "write", Public: true, UsesPathID: true, Call: writeUIView},
		{Name: "unlink", Public: true, UsesPathID: true, Call: unlinkUIView},
		{Name: "copy_data", Public: true, ReadOnly: true, Call: copyDataUIView},
		{Name: "compare_arch", Public: true, ReadOnly: true, Call: compareArchUIView},
		{Name: "reset_arch", Public: true, Call: resetArchUIView},
		{Name: "default_view", Public: true, ModelOnly: true, ReadOnly: true, Call: defaultUIView},
		{Name: "get_combined_arch", Public: true, ReadOnly: true, Call: getCombinedArchUIView},
		{Name: "get_view", Public: true, ModelOnly: true, ReadOnly: true, Call: getViewUIView},
		{Name: "get_views", Public: true, ModelOnly: true, ReadOnly: true, Call: getViewsUIView},
		{Name: "list_installed_dashboards", Public: true, ModelOnly: true, ReadOnly: true, Call: listInstalledUIViewDashboards},
	},
}

// UIViewModel owns the persisted view catalog used by the XML View Pattern
// feature and the RPC methods that resolve stored XML into SDUI payloads.
// Keep view customization in composition style by splitting normalization,
// persistence wrappers, and XML resolution into small helpers instead of one
// customize function.
var UIViewModel = orm.Define[UIViewRecord](&Models, 140, UIViewModelSpec)
var UIViewCustomModel = orm.Define[UIViewCustomRecord](&Models, 150, orm.ModelSpec{
	Name:        UIViewCustomModelName,
	Label:       "UI View Custom",
	Description: "Per-user UI view overrides",
	Definition:  UIViewCustomDefinition,
})

func init() {
	// Defer binding the handler methods until package initialization so the
	// model spec can reference createUIView/writeUIView without forming a
	// static initialization cycle through UIViewModel.
	// Expose the legacy frontend-only view hint through runtime enrichment
	// without requiring a physical PostgreSQL column on ui_view.
	orm.RegisterCompute(UIViewModelName, "statusbar_visible", computeUIViewStatusbarVisible)
	registerFrontendBIDConstraint(UIViewModelName)
}

// buildUIViewDefinition builds the ui view definition handled by this package.
func buildUIViewDefinition() *orm.SchemaDefinition {
	definition := orm.ModelDefinition(UIViewModelName, (*UIViewRecord)(nil))
	// Keep RPC compatibility with the legacy frontend contract, which reads
	// this field from ui.view even when the backend does not persist it.
	statusbarVisible := orm.NewField("statusbar_visible", "statusbar_visible", orm.FieldKindText)
	statusbarVisible.Label = "Statusbar Visible"
	statusbarVisible.ReadOnly = true
	statusbarVisible.Stored = false
	statusbarVisible.Compute = "_compute_statusbar_visible"
	definition.AddField(statusbarVisible)
	return definition
}

// computeUIViewStatusbarVisible computes the ui view statusbar visible handled by this package.
func computeUIViewStatusbarVisible(_ context.Context, records orm.RecordSet, _ orm.FieldMetadata) (map[string]any, error) {
	values := make(map[string]any, records.Len())
	// Walk each ids() entry in order so validation and side effects stay scoped to the current element.
	for _, id := range records.IDs() {
		// The compatibility field is optional UI metadata, so default to the
		// empty string when no persisted statusbar visibility is defined.
		values[id] = ""
	}
	return values, nil
}

// createUIView creates the ui view handled by this package.
func createUIView(ctx context.Context, model *orm.ModelMetadata, _ []string, args []any, kwargs map[string]any) (any, error) {
	valuesList, err := recordMapListFromArgs(args, kwargs, "values")
	if err != nil {
		return nil, err
	}
	if len(valuesList) == 0 {
		log.Errorf("ui.view.create requires values")
		return nil, fmt.Errorf("ui.view.create requires values")
	}

	// Accept both one-record and multi-record create payloads so the ui.view
	// port keeps the older view-RPC transport contract intact.
	created := make([]orm.RecordMap, 0, len(valuesList))
	for _, values := range valuesList {
		normalized, err := normalizeUIViewValues(values, "", true)
		if err != nil {
			return nil, err
		}
		// Raw insert on purpose: the form view's create posts every field, including the
		// non-stored computed statusbar_visible, which CreateRecord rejects. ui.view has no
		// company_id, and the T15 dispatcher guard covers record rules.
		record, err := orm.RuntimeRepositoryCreate(ctx, orm.RuntimeModelRef(UIViewModelName, model), normalized)
		if err != nil {
			return nil, err
		}
		created = append(created, record)
	}
	if len(created) == 1 {
		return created[0], nil
	}
	return created, nil
}

// readUIView reads the ui view handled by this package.
func readUIView(ctx context.Context, model *orm.ModelMetadata, ids []string, args []any, kwargs map[string]any) (any, error) {
	resolvedIDs, err := runtimeIDList(ids, args, kwargs)
	if err != nil {
		return nil, err
	}
	fields := stringSlice(firstNonNil(argValue(args, 1), mapValue(kwargs, "fields")))
	return orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(UIViewModelName, model), resolvedIDs, fields)
}

func searchReadUIView(ctx context.Context, model *orm.ModelMetadata, _ []string, args []any, kwargs map[string]any) (any, error) {
	filter := orm.RecordMap{}
	if kwargs != nil {
		filter = searchFilterFromKwargs(kwargs)
	}
	if len(args) > 0 {
		// Positional domain callers (legacy args[0] form) AND onto any kwargs
		// domain instead of overwriting it.
		if err := mergeUIViewDomain(filter, args[0]); err != nil {
			return nil, err
		}
	}
	fields := stringSlice(firstNonNil(argValue(args, 1), mapValue(kwargs, "fields")))
	// A client call cannot list past server.maxReadLimit (plan T36).
	if limit := orm.ClampClientReadLimit(ctx, model, int(int64Value(filter["__limit"]))); limit > 0 {
		filter["__limit"] = limit
	}
	return orm.RuntimeRepositorySearchRead(ctx, orm.RuntimeModelRef(UIViewModelName, model), filter, fields)
}

func writeUIView(ctx context.Context, model *orm.ModelMetadata, ids []string, args []any, kwargs map[string]any) (any, error) {
	values, err := recordMapFromArgs(args, kwargs, "values")
	if err != nil {
		return nil, err
	}
	if values == nil {
		log.Errorf("ui.view.write requires values")
		return nil, fmt.Errorf("ui.view.write requires values")
	}
	if len(ids) == 0 {
		log.Errorf("ui.view.write requires ids")
		return nil, fmt.Errorf("ui.view.write requires ids")
	}

	// Mirror the legacy view editor behavior: changing any arch source marks the
	// row as customized unless the caller already set arch_updated explicitly.
	if _, ok := values["arch_updated"]; !ok {
		if _, hasArch := values["arch"]; hasArch {
			values["arch_updated"] = true
		}
		if _, hasArchBase := values["arch_base"]; hasArchBase {
			values["arch_updated"] = true
		}
	}

	currentArch, err := currentUIViewArch(ctx, ids[0])
	if err != nil {
		return nil, err
	}
	if _, hasArchDB := values["arch_db"]; hasArchDB && currentArch != "" && !boolMapValue(kwargs, "no_save_prev") {
		values["arch_prev"] = currentArch
	}
	if err := unlinkUIViewCustomizations(ctx, ids); err != nil {
		return nil, err
	}
	normalized, err := normalizeUIViewValues(values, currentArch, false)
	if err != nil {
		return nil, err
	}
	return orm.WriteRecords(ctx, orm.RuntimeModelRef(UIViewModelName, model), ids, normalized)
}

func unlinkUIView(ctx context.Context, model *orm.ModelMetadata, ids []string, _ []any, kwargs map[string]any) (any, error) {
	if len(ids) == 0 {
		log.Errorf("ui.view.unlink requires ids")
		return nil, fmt.Errorf("ui.view.unlink requires ids")
	}

	// Preserve the recursive forced-uninstall behavior so inherited extension
	// trees are deleted together only when callers opt into it explicitly.
	if boolMapValue(kwargs, "_force_unlink") {
		children, err := orm.RuntimeRepositorySearchRead(ctx, orm.RuntimeModelRef(UIViewModelName, model), orm.RecordMap{"inherit_id": idsToAny(ids)}, []string{"id"})
		if err != nil {
			return nil, err
		}
		childIDs := recordIDs(children)
		if len(childIDs) > 0 {
			if _, err := unlinkUIView(ctx, nil, childIDs, nil, kwargs); err != nil {
				return nil, err
			}
		}
	}
	return orm.RuntimeRepositoryUnlink(ctx, orm.RuntimeModelRef(UIViewModelName, model), ids)
}

// copyDataUIView copies the data ui view handled by this package.
func copyDataUIView(ctx context.Context, model *orm.ModelMetadata, ids []string, args []any, kwargs map[string]any) (any, error) {
	resolvedIDs, err := runtimeIDList(ids, args, kwargs)
	if err != nil {
		return nil, err
	}
	if len(resolvedIDs) == 0 {
		log.Errorf("ui.view.copy_data requires ids")
		return nil, fmt.Errorf("ui.view.copy_data requires ids")
	}

	// Clone the stored rows into create payloads and re-key copied templated
	// views so the duplicate can coexist with the original.
	defaults := orm.RecordMap{}
	if len(args) > 0 {
		if rawDefaults, ok := args[0].(map[string]any); ok {
			defaults = cloneRecordMap(orm.RecordMap(rawDefaults))
		}
		if rawDefaults, ok := args[0].(orm.RecordMap); ok {
			defaults = cloneRecordMap(rawDefaults)
		}
	}
	hasDefaultWithoutKey := len(defaults) > 0
	_, hasDefaultKey := defaults["key"]
	hasDefaultWithoutKey = hasDefaultWithoutKey && !hasDefaultKey

	rows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(UIViewModelName, model), resolvedIDs, nil)
	if err != nil {
		return nil, err
	}
	out := make([]orm.RecordMap, 0, len(rows))
	for _, row := range rows {
		values := cloneRecordMap(row)
		delete(values, "id")
		for key, value := range defaults {
			values[key] = value
		}
		if key := strings.TrimSpace(stringValue(values["key"])); key != "" && hasDefaultWithoutKey {
			values["key"] = key + "_" + generatedUIViewKeySuffix()
		}
		out = append(out, values)
	}
	return out, nil
}

// compareArchUIView compares the arch ui view handled by this package.
func compareArchUIView(ctx context.Context, _ *orm.ModelMetadata, ids []string, args []any, kwargs map[string]any) (any, error) {
	request, err := parseUIViewCompareRequest(ids, args, kwargs)
	if err != nil {
		return nil, err
	}
	current, err := loadUIViewRowByID(ctx, request.ViewID)
	if err != nil {
		return nil, err
	}
	return buildUIViewArchComparison(ctx, current, request)
}

// resetArchUIView resets the arch ui view handled by this package.
func resetArchUIView(ctx context.Context, model *orm.ModelMetadata, ids []string, args []any, kwargs map[string]any) (any, error) {
	resolvedIDs, err := runtimeIDList(ids, nil, kwargs)
	if err != nil {
		return nil, err
	}
	if len(resolvedIDs) == 0 {
		log.Errorf("ui.view.reset_arch requires ids")
		return nil, fmt.Errorf("ui.view.reset_arch requires ids")
	}

	mode := "soft"
	if value, ok := stringFromAny(argValue(args, 0)); ok && value != "" {
		mode = value
	}
	for _, id := range resolvedIDs {
		rows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(UIViewModelName, model), []string{id}, []string{"arch_prev", "arch_fs"})
		if err != nil {
			return nil, err
		}
		if len(rows) == 0 {
			continue
		}

		values := orm.RecordMap{}
		switch mode {
		case "soft":
			archPrev := strings.TrimSpace(stringValue(rows[0]["arch_prev"]))
			if archPrev == "" {
				continue
			}
			values["arch_db"] = archPrev
		case "other_view":
			compareViewID, ok := int64FromAny(firstNonNil(argValue(args, 1), mapValue(kwargs, "compare_view_id")))
			if !ok || compareViewID <= 0 {
				log.Errorf("ui.view.reset_arch mode other_view requires compare_view_id")
				return nil, fmt.Errorf("ui.view.reset_arch mode other_view requires compare_view_id")
			}
			compareView, err := loadUIViewRowByID(ctx, compareViewID)
			if err != nil {
				return nil, err
			}
			compareArch := rowArch(compareView)
			if compareArch == "" {
				continue
			}
			values["arch_db"] = compareArch
		case "hard":
			archFS := strings.TrimSpace(stringValue(rows[0]["arch_fs"]))
			if archFS == "" {
				continue
			}
			arch, err := readUIViewArchFS(archFS)
			if err != nil || strings.TrimSpace(arch) == "" {
				continue
			}
			values["arch_db"] = arch
			values["arch_prev"] = nil
			values["arch_updated"] = false
		default:
			log.Errorf("ui.view.reset_arch mode must be soft, hard, or other_view")
			return nil, fmt.Errorf("ui.view.reset_arch mode must be soft, hard, or other_view")
		}
		writeKwargs := map[string]any{}
		if mode != "other_view" {
			writeKwargs["no_save_prev"] = true
		}
		if _, err := writeUIView(ctx, nil, []string{id}, []any{values}, writeKwargs); err != nil {
			return nil, err
		}
	}
	return true, nil
}

// defaultUIView selects the ui view handled by this package.
func defaultUIView(ctx context.Context, _ *orm.ModelMetadata, _ []string, args []any, kwargs map[string]any) (any, error) {
	request, err := parseUIViewRequest(args, kwargs)
	if err != nil {
		return nil, err
	}
	row, err := loadDefaultUIView(ctx, request.Model, request.ViewType)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return nil, err
	}
	return row.ID, nil
}

// getCombinedArchUIView retrieves the combined arch ui view handled by this package.
func getCombinedArchUIView(ctx context.Context, _ *orm.ModelMetadata, ids []string, args []any, kwargs map[string]any) (any, error) {
	viewID, err := resolveUIViewID(ids, args, kwargs)
	if err != nil {
		return nil, err
	}
	row, err := loadUIViewRowByID(ctx, viewID)
	if err != nil {
		return nil, err
	}
	resolved, err := resolveStoredUIView(ctx, row)
	if err != nil {
		return nil, err
	}
	return resolved.Arch, nil
}

// getViewUIView retrieves the view ui view handled by this package.
func getViewUIView(ctx context.Context, _ *orm.ModelMetadata, _ []string, args []any, kwargs map[string]any) (any, error) {
	request, err := parseUIViewRequest(args, kwargs)
	if err != nil {
		return nil, err
	}
	return buildUIViewResponse(ctx, request)
}

// listInstalledUIViewDashboards lists every type="dashboard" ui.view row
// belonging to a currently installed module, ordered by sequence then module
// so the frontend's master /web dashboard renders its tab list
// deterministically. This is the ui.view-hosted successor to the retired
// sys.module.dashboard model's list_installed method — dashboards are not
// model-bound, so they bypass get_view/get_views (which hard-require a
// target runtime model) and go through this dedicated method instead.
func listInstalledUIViewDashboards(ctx context.Context, model *orm.ModelMetadata, _ []string, _ []any, _ map[string]any) (any, error) {
	// Resolve the pgx pool the same way sys_module.go's lifecycle actions do:
	// prefer the context-scoped pool (multi-DB aware), fall back to the
	// process default only when the context carries none.
	pool := orm.PGXPoolFromContext(ctx)
	if pool == nil {
		pool = orm.GetPGXPool()
	}
	installed, err := queries.InstalledModuleNames(ctx, pool)
	if err != nil {
		log.Errorf("ui.view.list_installed_dashboards: resolve installed modules: %v", err)
		return nil, err
	}
	if len(installed) == 0 {
		return []map[string]any{}, nil
	}

	// Build the IN-list filter: a slice value on RuntimeRepositorySearchRead's
	// RecordMap filter is matched as "column IN (...)", same convention
	// ui_menu.go uses for parent_id.
	moduleNames := make([]any, 0, len(installed))
	for name := range installed {
		moduleNames = append(moduleNames, name)
	}
	rows, err := orm.RuntimeRepositorySearchRead(
		ctx, orm.RuntimeModelRef(UIViewModelName, model),
		orm.RecordMap{"type": "dashboard", "module": moduleNames, "__order": "sequence asc, module asc"},
		// The raw "arch" column is always blank on stored rows: the write
		// normalizer moves any incoming arch into arch_db/arch_base and
		// deletes "arch". Read all three and resolve like rowArch does.
		[]string{"module", "name", "sequence", "arch", "arch_db", "arch_base"},
	)
	if err != nil {
		log.Errorf("ui.view.list_installed_dashboards: search_read: %v", err)
		return nil, err
	}

	// Shape the response to exactly {module, name, arch} per the RPC contract
	// the frontend master dashboard expects — sequence is only used for
	// ordering server-side, not needed by the client once sorted.
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		out = append(out, map[string]any{
			"module": strings.TrimSpace(stringValue(row["module"])),
			"name":   strings.TrimSpace(stringValue(row["name"])),
			"arch":   strings.TrimSpace(firstNonEmpty(stringValue(row["arch_db"]), stringValue(row["arch"]), stringValue(row["arch_base"]))),
		})
	}
	// Ordering is fully delegated to the __order clause above (sequence asc,
	// module asc) — do not re-sort here, that would discard sequence order.
	return out, nil
}

// getViewsUIView retrieves the views ui view handled by this package.
func getViewsUIView(ctx context.Context, _ *orm.ModelMetadata, _ []string, args []any, kwargs map[string]any) (any, error) {
	request, err := parseUIViewsRequest(args, kwargs)
	if err != nil {
		return nil, err
	}
	modelMeta, ok := orm.LookupModel(request.Model)
	if !ok {
		log.Errorf("ui.view.get_views unknown model %q", request.Model)
		return nil, fmt.Errorf("ui.view.get_views unknown model %q", request.Model)
	}

	views := make(map[string]any, len(request.ViewTypes))
	for _, viewType := range request.ViewTypes {
		payload, err := buildUIViewResponse(ctx, uiViewRequest{
			Model:    request.Model,
			ViewType: viewType,
			ViewID:   request.ViewIDs[viewType],
		})
		if err != nil {
			return nil, err
		}
		views[viewType] = payload
	}
	return map[string]any{
		"model":  request.Model,
		"fields": uiViewFieldMap(modelMeta),
		"views":  views,
	}, nil
}

// requireUIViewDB requires the ui view db handled by this package.
func requireUIViewDB(ctx context.Context) (bun.IDB, error) {
	env := orm.EnvironmentFromContext(ctx)
	if env == nil || env.DB() == nil {
		log.Errorf("ui.view requires an initialized runtime database")
		return nil, fmt.Errorf("ui.view requires an initialized runtime database")
	}
	return env.DB(), nil
}

func currentUIViewArch(ctx context.Context, id string) (string, error) {
	viewID, err := parsePositiveInt64(id)
	if err != nil {
		return "", err
	}
	row, err := loadUIViewRowByID(ctx, viewID)
	if err != nil {
		return "", err
	}
	return rowArch(row), nil
}

func normalizeUIViewValues(values orm.RecordMap, existingArch string, creating bool) (orm.RecordMap, error) {
	working := cloneRecordMap(values)
	if inheritRaw, ok := working["inherit_id"]; ok {
		if inheritBool, isBool := inheritRaw.(bool); isBool && !inheritBool {
			working["inherit_id"] = nil
		}
	}
	if arch, ok := stringFromAny(working["arch"]); ok {
		normalized, err := normalizeArchText(arch)
		if err != nil {
			return nil, err
		}
		working["arch_db"] = normalized
		if _, ok := working["arch_base"]; !ok {
			working["arch_base"] = normalized
		}
		delete(working, "arch")
	}
	if archBase, ok := stringFromAny(working["arch_base"]); ok {
		normalized, err := normalizeArchText(archBase)
		if err != nil {
			return nil, err
		}
		working["arch_base"] = normalized
		if _, ok := working["arch_db"]; !ok {
			working["arch_db"] = normalized
		}
	}
	if archDB, ok := stringFromAny(working["arch_db"]); ok {
		normalized, err := normalizeArchText(archDB)
		if err != nil {
			return nil, err
		}
		working["arch_db"] = normalized
		if _, ok := working["arch_base"]; !ok {
			working["arch_base"] = normalized
		}
		if !creating && existingArch != "" {
			working["arch_prev"] = existingArch
		}
	}
	if _, ok := working["priority"]; !ok && creating {
		working["priority"] = int64(16)
	}
	if _, ok := working["active"]; !ok && creating {
		working["active"] = true
	}
	if _, ok := working["mode"]; !ok {
		if inheritID, ok := int64FromAny(working["inherit_id"]); ok && inheritID > 0 {
			working["mode"] = "extension"
		} else if creating {
			working["mode"] = "primary"
		}
	}
	if _, ok := working["type"]; !ok || strings.TrimSpace(fmt.Sprint(working["type"])) == "" {
		if inferred, ok := inferArchType(firstNonEmpty(stringValue(working["arch_db"]), stringValue(working["arch_base"]))); ok {
			working["type"] = inferred
		}
	}
	if viewType := strings.TrimSpace(stringValue(working["type"])); viewType == "qweb" {
		if key := strings.TrimSpace(stringValue(working["key"])); key == "" {
			working["key"] = "sys__custom__gen_key_" + generatedUIViewKeySuffix()
		}
	}
	if _, ok := working["name"]; !ok && creating {
		working["name"] = strings.TrimSpace(firstNonEmpty(stringValue(working["model"]), "view") + " " + firstNonEmpty(stringValue(working["type"]), "form"))
	}
	if _, ok := working["arch_prev"]; !ok && creating {
		if archPrev := firstNonEmpty(stringValue(working["arch_db"]), stringValue(working["arch_base"])); archPrev != "" {
			working["arch_prev"] = archPrev
		}
	}
	return working, nil
}

func unlinkUIViewCustomizations(ctx context.Context, ids []string) error {
	customRows, err := orm.RuntimeRepositorySearchRead(ctx, orm.RuntimeModelRef(UIViewCustomModelName, nil), orm.RecordMap{"ref_id": idsToAny(ids)}, []string{"id"})
	if err != nil {
		return err
	}
	customIDs := recordIDs(customRows)
	if len(customIDs) == 0 {
		return nil
	}
	_, err = orm.RuntimeRepositoryUnlink(ctx, orm.RuntimeModelRef(UIViewCustomModelName, nil), customIDs)
	return err
}

func generatedUIViewKeySuffix() string {
	var raw [3]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "000000"
	}
	return hex.EncodeToString(raw[:])
}

// readUIViewArchFS reads the ui view arch fs handled by this package.
func readUIViewArchFS(archFS string) (string, error) {
	archFS = strings.TrimPrefix(strings.TrimSpace(archFS), "/")
	if archFS == "" {
		log.Errorf("ui.view.arch_fs is empty")
		return "", fmt.Errorf("ui.view.arch_fs is empty")
	}
	// Walk each split(archfs, "/") entry in order so validation and side effects stay scoped to the current element.
	for _, part := range strings.Split(archFS, "/") {
		if part == ".." {
			log.Errorf("ui.view.arch_fs path traversal rejected: %q", archFS)
			return "", fmt.Errorf("ui.view.arch_fs path traversal rejected: %q", archFS)
		}
	}
	raw, err := kernelmodules.ReadModuleResource(archFS)
	if err != nil {
		log.Errorf("ui.view.arch_fs %q not found", archFS)
		return "", fmt.Errorf("ui.view.arch_fs %q not found", archFS)
	}
	return string(raw), nil
}
