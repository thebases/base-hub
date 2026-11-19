package models

// File overview:
// - Responsibility: defines built-in runtime model metadata and server-side behavior for ui action window inside the kernel model catalog.
// - Key symbols: `init`, `createUIActionWindow`, `UIActionWindowRecord`, `uiActionViewTuple`.
// - Pattern: Leans on metadata-driven registration so storage mapping, runtime methods, and behavior hooks stay centralized instead of being scattered across handlers.
// - Pattern: Threads `context.Context` through the main execution paths so cancellation, auth scope, and transaction state can follow the work end to end.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
)

// UIActionWindowRecord stores one window action definition using the repo's
// metadata-driven runtime model layer instead of a separate hard-coded API.
type UIActionWindowRecord struct {
	orm.BaseModel
	Name         string         `bun:"name" json:"name" meta:"index,label:Name,help:Stores the human readable name for this record."`
	Type         string         `bun:"type" json:"type" meta:"index,label:Type,help:Stores the type value for this record."`
	ResModel     string         `bun:"sys_model" json:"sys_model" meta:"index,label:Res Model,help:Stores the res model value for this record."`
	Target       string         `bun:"target" json:"target" meta:"index,label:Target,help:Stores the target value for this record."`
	Context      map[string]any `bun:"context,type:jsonb,nullzero" json:"context" meta:"kind:json,label:Context,help:Stores the context payload in JSON format."`
	Domain       []any          `bun:"domain,type:jsonb,nullzero" json:"domain" meta:"kind:json,label:Domain,help:Stores the domain payload in JSON format."`
	ViewMode     string         `bun:"view_mode" json:"view_mode" meta:"label:View Mode,help:Stores the view mode value for this record."`
	ViewID       *int64         `bun:"view_id" json:"view_id" meta:"kind:many2one,model:ui.view,ondelete:SET NULL,index,label:View ID,help:Links this record to the related ui.view record for view id."`
	Views        []any          `bun:"views,type:jsonb,nullzero" json:"views" meta:"kind:json,label:Views,help:Stores the views payload in JSON format."`
	SearchViewID *int64         `bun:"search_view_id" json:"search_view_id" meta:"kind:many2one,model:ui.view,ondelete:SET NULL,index,label:Search View ID,help:Links this record to the related ui.view record for search view id."`
	Help         string         `bun:"help" json:"help" meta:"label:Help,help:Stores the helper text shown to users for this record."`
	Limit        int64          `bun:"limit" json:"limit" meta:"label:Limit,help:Stores the numeric value for limit."`
}

const UIActionWindowModelName = "ui.actions.window"

var UIActionWindowDefinition = orm.ModelDefinition(UIActionWindowModelName, (*UIActionWindowRecord)(nil))

// UIActionWindowModel owns persisted act_window-like metadata and exposes a
// conservative runtime surface for callers.
// Keep future customization in composition style by composing validators,
// runtime wrappers, and handler options instead of one large customize
// function.
var UIActionWindowModel = orm.Define[UIActionWindowRecord](&Models, 170, orm.ModelSpec{
	Name:        UIActionWindowModelName,
	Label:       "UI Window Action",
	Description: "Window actions modeled after ir.actions.act_window",
	Definition:  UIActionWindowDefinition,
	RuntimeMethods: []orm.RuntimeMethod{
		{Name: "create", Public: true, ModelOnly: true, Call: createUIActionWindow},
		{Name: "write", Public: true, UsesPathID: true, Call: writeUIActionWindow},
		{Name: "_get_action_dict", Public: false, ReadOnly: true, Call: getActionDictUIActionWindow},
		{Name: "_get_readable_fields", Public: false, ModelOnly: true, ReadOnly: true, Call: getReadableFieldsUIActionWindow},
	},
})

var (
	uiActionWindowLookupDefaultViewID = lookupDefaultUIViewID
	uiActionWindowLoadViewByID        = loadUIViewRowByID
)

var uiActionWindowReadableFields = []string{
	"context",
	"domain",
	"help",
	"id",
	"limit",
	"name",
	"sys_model",
	"search_view_id",
	"target",
	"type",
	"view_id",
	"view_mode",
	"views",
}

type uiActionViewTuple struct {
	ID   int64
	Type string
}

// createUIActionWindow creates the ui action window handled by this package.
func createUIActionWindow(ctx context.Context, model *orm.ModelMetadata, _ []string, args []any, kwargs map[string]any) (any, error) {
	values, err := recordMapFromArgs(args, kwargs, "values")
	if err != nil {
		return nil, err
	}
	if values == nil {
		log.Errorf("ui.actions.window.create requires values")
		return nil, fmt.Errorf("ui.actions.window.create requires values")
	}
	normalized, err := normalizeUIActionWindowValues(ctx, values, true)
	if err != nil {
		return nil, err
	}
	return orm.CreateRecord(ctx, orm.RuntimeModelRef(UIActionWindowModelName, model), normalized)
}

func writeUIActionWindow(ctx context.Context, model *orm.ModelMetadata, ids []string, args []any, kwargs map[string]any) (any, error) {
	values, err := recordMapFromArgs(args, kwargs, "values")
	if err != nil {
		return nil, err
	}
	if values == nil {
		log.Errorf("ui.actions.window.write requires values")
		return nil, fmt.Errorf("ui.actions.window.write requires values")
	}
	if len(ids) == 0 {
		log.Errorf("ui.actions.window.write requires ids")
		return nil, fmt.Errorf("ui.actions.window.write requires ids")
	}
	normalized, err := normalizeUIActionWindowValues(ctx, values, false)
	if err != nil {
		return nil, err
	}
	return orm.WriteRecords(ctx, orm.RuntimeModelRef(UIActionWindowModelName, model), ids, normalized)
}

// getActionDictUIActionWindow retrieves the action dict ui action window handled by this package.
func getActionDictUIActionWindow(ctx context.Context, _ *orm.ModelMetadata, ids []string, args []any, kwargs map[string]any) (any, error) {
	actionID, err := resolveUIActionID(ids, args, kwargs)
	if err != nil {
		return nil, err
	}
	row, err := loadUIActionWindowRow(ctx, actionID)
	if err != nil {
		return nil, err
	}
	return buildUIActionWindowDict(ctx, row)
}

// getReadableFieldsUIActionWindow retrieves the readable fields ui action window handled by this package.
func getReadableFieldsUIActionWindow(_ context.Context, _ *orm.ModelMetadata, _ []string, _ []any, _ map[string]any) (any, error) {
	fields := append([]string(nil), uiActionWindowReadableFields...)
	sort.Strings(fields)
	return fields, nil
}

func normalizeUIActionWindowValues(ctx context.Context, values orm.RecordMap, creating bool) (orm.RecordMap, error) {
	// Clone the caller-owned map so normalization does not leak mutations back
	// across runtime boundaries or tests.
	normalized := cloneRecordMap(values)

	// Persist the canonical action type explicitly so read/search_read payloads
	// remain self-describing across transports.
	normalized["type"] = UIActionWindowModelName

	// Require a destination model on create and whenever one is explicitly set
	// because the action dict depends on the model's default view resolution.
	if resModel, ok := stringFromAny(normalized["sys_model"]); ok {
		if strings.TrimSpace(resModel) == "" {
			log.Errorf("ui.actions.window sys_model is required")
			return nil, fmt.Errorf("ui.actions.window sys_model is required")
		}
		if _, exists := orm.LookupModel(strings.TrimSpace(resModel)); !exists {
			log.Errorf("ui.actions.window unknown sys_model %q", strings.TrimSpace(resModel))
			return nil, fmt.Errorf("ui.actions.window unknown sys_model %q", strings.TrimSpace(resModel))
		}
		normalized["sys_model"] = strings.TrimSpace(resModel)
	} else if creating {
		log.Errorf("ui.actions.window sys_model is required")
		return nil, fmt.Errorf("ui.actions.window sys_model is required")
	}

	// Keep the display name stable and human-friendly, defaulting to the model
	// name when callers omit it during create.
	if name, ok := stringFromAny(normalized["name"]); ok {
		name = strings.TrimSpace(name)
		if name == "" {
			log.Errorf("ui.actions.window name is required")
			return nil, fmt.Errorf("ui.actions.window name is required")
		}
		normalized["name"] = name
	} else if creating {
		modelName := strings.TrimSpace(stringValue(normalized["sys_model"]))
		if modelName == "" {
			log.Errorf("ui.actions.window name is required")
			return nil, fmt.Errorf("ui.actions.window name is required")
		}
		normalized["name"] = humanizeModelNameUIAction(modelName)
	}

	// Normalize target against the frontend-supported modes so action dispatch
	// stays consistent with the current shell behavior.
	if target, ok := stringFromAny(normalized["target"]); ok {
		target = strings.TrimSpace(target)
		if target == "" {
			target = "current"
		}
		if !isSupportedUIActionTarget(target) {
			log.Errorf("ui.actions.window invalid target %q", target)
			return nil, fmt.Errorf("ui.actions.window invalid target %q", target)
		}
		normalized["target"] = target
	} else if creating {
		normalized["target"] = "current"
	}

	// Default context and domain to empty JSON values so every action dict keeps
	// a stable object/array contract for callers.
	if rawContext, ok := normalized["context"]; ok {
		contextValue, err := normalizeUIActionContext(rawContext)
		if err != nil {
			return nil, err
		}
		normalized["context"] = contextValue
	} else if creating {
		normalized["context"] = map[string]any{}
	}
	if rawDomain, ok := normalized["domain"]; ok {
		domainValue, err := normalizeUIActionDomain(rawDomain)
		if err != nil {
			return nil, err
		}
		normalized["domain"] = domainValue
	} else if creating {
		normalized["domain"] = []any{}
	}

	// Preserve a deterministic view-mode order because the frontend uses it to
	// decide the initial renderer.
	resModel := strings.TrimSpace(stringValue(normalized["sys_model"]))
	if rawViewMode, ok := stringFromAny(normalized["view_mode"]); ok {
		viewMode, err := normalizeUIActionViewMode(rawViewMode, resModel)
		if err != nil {
			return nil, err
		}
		normalized["view_mode"] = viewMode
	} else if creating {
		normalized["view_mode"] = defaultUIActionViewMode(resModel)
	}

	// Validate optional view references early so broken action metadata is
	// rejected before it reaches runtime navigation.
	if viewID, changed, err := optionalInt64FromRecord(normalized, "view_id"); err != nil {
		return nil, err
	} else if changed && viewID != nil {
		if err := validateUIViewReference(ctx, *viewID, resModel); err != nil {
			return nil, err
		}
	}
	if searchViewID, changed, err := optionalInt64FromRecord(normalized, "search_view_id"); err != nil {
		return nil, err
	} else if changed && searchViewID != nil {
		if err := validateUIViewReference(ctx, *searchViewID, resModel); err != nil {
			return nil, err
		}
	}

	// Normalize the explicit tuple list used by window action payloads.
	if rawViews, ok := normalized["views"]; ok {
		viewTuples, err := normalizeUIActionViews(ctx, rawViews, resModel)
		if err != nil {
			return nil, err
		}
		normalized["views"] = encodeUIActionViews(viewTuples)
	}

	// Keep list-page sizing deterministic for consumers that rely on the action
	// contract rather than ad-hoc frontend defaults.
	if rawLimit, ok := normalized["limit"]; ok {
		limit, ok := int64FromAny(rawLimit)
		if !ok || limit < 0 {
			log.Errorf("ui.actions.window invalid limit %#v", rawLimit)
			return nil, fmt.Errorf("ui.actions.window invalid limit %#v", rawLimit)
		}
		normalized["limit"] = limit
	} else if creating {
		normalized["limit"] = int64(80)
	}

	if rawHelp, ok := normalized["help"]; ok {
		normalized["help"] = strings.TrimSpace(stringValue(rawHelp))
	} else if creating {
		normalized["help"] = ""
	}
	return normalized, nil
}

// loadUIActionWindowRow loads the ui action window row handled by this package.
func loadUIActionWindowRow(ctx context.Context, actionID int64) (orm.RecordMap, error) {
	rows, err := orm.RuntimeRepositoryRead(
		ctx, orm.RuntimeModelRef(UIActionWindowModelName, nil),
		[]string{fmt.Sprintf("%d", actionID)},
		[]string{"id", "name", "type", "sys_model", "target", "context", "domain", "view_mode", "view_id", "views", "search_view_id", "help", "limit"},
	)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, sql.ErrNoRows
	}
	return rows[0], nil
}

// buildUIActionWindowDict builds the ui action window dict handled by this package.
func buildUIActionWindowDict(ctx context.Context, row orm.RecordMap) (map[string]any, error) {
	resModel := strings.TrimSpace(stringValue(row["sys_model"]))
	if resModel == "" {
		log.Errorf("ui.actions.window %v has empty sys_model", row["id"])
		return nil, fmt.Errorf("ui.actions.window %v has empty sys_model", row["id"])
	}
	viewMode, err := normalizeUIActionViewMode(stringValue(row["view_mode"]), resModel)
	if err != nil {
		return nil, err
	}
	preferredView, err := resolveUIActionPreferredView(ctx, optionalInt64Value(row["view_id"]), resModel)
	if err != nil {
		return nil, err
	}
	viewMode = prioritizeUIActionViewMode(viewMode, preferredView.Type)
	explicitViews, err := decodeUIActionViews(row["views"], resModel)
	if err != nil {
		return nil, err
	}
	resolvedViews, err := resolveUIActionWindowViews(ctx, resModel, viewMode, preferredView, explicitViews)
	if err != nil {
		return nil, err
	}
	searchViewTuple, err := resolveUIActionSearchView(ctx, resModel, optionalInt64Value(row["search_view_id"]))
	if err != nil {
		return nil, err
	}
	actionID, _ := int64FromAny(row["id"])
	return map[string]any{
		"type":           UIActionWindowModelName,
		"id":             actionID,
		"name":           firstNonEmpty(strings.TrimSpace(stringValue(row["name"])), humanizeModelNameUIAction(resModel)),
		"sys_model":      resModel,
		"target":         firstNonEmpty(strings.TrimSpace(stringValue(row["target"])), "current"),
		"context":        normalizeUIActionContextForOutput(row["context"]),
		"domain":         normalizeUIActionDomainForOutput(row["domain"]),
		"views":          encodeUIActionViews(resolvedViews),
		"view_mode":      viewMode,
		"search_view_id": []any{searchViewTuple.ID, searchViewTuple.Type},
		"help":           strings.TrimSpace(stringValue(row["help"])),
		"limit":          int64Value(row["limit"]),
	}, nil
}

// resolveUIActionWindowViews resolves the ui action window views handled by this package.
func resolveUIActionWindowViews(ctx context.Context, modelName, viewMode string, preferredView uiActionViewTuple, explicit []uiActionViewTuple) ([]uiActionViewTuple, error) {
	orderedModes := splitNonEmptyUIAction(viewMode, ",")
	seen := make(map[string]struct{}, len(orderedModes))
	explicitByType := make(map[string]uiActionViewTuple, len(explicit))
	for _, tuple := range explicit {
		explicitByType[tuple.Type] = tuple
	}
	out := make([]uiActionViewTuple, 0, len(orderedModes))
	for _, rawMode := range orderedModes {
		mode := normalizeViewType(rawMode)
		if mode == "" {
			continue
		}
		seen[mode] = struct{}{}
		if tuple, ok := explicitByType[mode]; ok {
			out = append(out, tuple)
			continue
		}
		if preferredView.ID > 0 && preferredView.Type == mode {
			out = append(out, preferredView)
			continue
		}
		defaultID, err := uiActionWindowLookupDefaultViewID(ctx, modelName, mode)
		if err != nil {
			return nil, err
		}
		out = append(out, uiActionViewTuple{ID: defaultID, Type: mode})
	}
	for _, tuple := range explicit {
		if _, ok := seen[tuple.Type]; ok {
			continue
		}
		out = append(out, tuple)
	}
	return out, nil
}

func resolveUIActionPreferredView(ctx context.Context, viewID *int64, modelName string) (uiActionViewTuple, error) {
	if viewID == nil || *viewID <= 0 {
		return uiActionViewTuple{}, nil
	}
	row, err := uiActionWindowLoadViewByID(ctx, *viewID)
	if err != nil {
		return uiActionViewTuple{}, err
	}
	if strings.TrimSpace(modelName) != "" && strings.TrimSpace(row.Model) != "" && strings.TrimSpace(row.Model) != strings.TrimSpace(modelName) {
		log.Errorf("ui.action view %d belongs to model %q, expected %q", *viewID, row.Model, modelName)
		return uiActionViewTuple{}, fmt.Errorf("ui.action view %d belongs to model %q, expected %q", *viewID, row.Model, modelName)
	}
	viewType := normalizeViewType(row.Type)
	if viewType == "" {
		log.Errorf("ui.action view %d has unsupported type %q", *viewID, row.Type)
		return uiActionViewTuple{}, fmt.Errorf("ui.action view %d has unsupported type %q", *viewID, row.Type)
	}
	return uiActionViewTuple{ID: *viewID, Type: viewType}, nil
}

func prioritizeUIActionViewMode(viewMode string, preferredType string) string {
	preferredType = normalizeViewType(preferredType)
	if preferredType == "" {
		return viewMode
	}
	modes := splitNonEmptyUIAction(viewMode, ",")
	if len(modes) == 0 {
		return preferredType
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(modes)+1)
	appendMode := func(mode string) {
		mode = normalizeViewType(mode)
		if mode == "" {
			return
		}
		if _, ok := seen[mode]; ok {
			return
		}
		seen[mode] = struct{}{}
		out = append(out, mode)
	}
	appendMode(preferredType)
	for _, mode := range modes {
		appendMode(mode)
	}
	return strings.Join(out, ",")
}

// resolveUIActionSearchView resolves the ui action search view handled by this package.
func resolveUIActionSearchView(ctx context.Context, modelName string, searchViewID *int64) (uiActionViewTuple, error) {
	if searchViewID != nil {
		return uiActionViewTuple{ID: *searchViewID, Type: "search"}, nil
	}
	defaultID, err := uiActionWindowLookupDefaultViewID(ctx, modelName, "search")
	if err != nil {
		return uiActionViewTuple{}, err
	}
	return uiActionViewTuple{ID: defaultID, Type: "search"}, nil
}

// lookupDefaultUIViewID looks up the default ui view id handled by this package.
func lookupDefaultUIViewID(ctx context.Context, modelName, viewType string) (int64, error) {
	row, err := loadDefaultUIView(ctx, modelName, viewType)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, nil
		}
		return 0, err
	}
	return row.ID, nil
}

// validateUIViewReference validates the ui view reference handled by this package.
func validateUIViewReference(ctx context.Context, viewID int64, modelName string) error {
	row, err := loadUIViewRowByID(ctx, viewID)
	if err != nil {
		return err
	}
	if strings.TrimSpace(modelName) != "" && strings.TrimSpace(row.Model) != "" && strings.TrimSpace(row.Model) != strings.TrimSpace(modelName) {
		log.Errorf("ui.action view %d belongs to model %q, expected %q", viewID, row.Model, modelName)
		return fmt.Errorf("ui.action view %d belongs to model %q, expected %q", viewID, row.Model, modelName)
	}
	return nil
}

func defaultUIActionViewMode(modelName string) string {
	if strings.TrimSpace(modelName) == "base.workflow" {
		return "list,kanban,canvas,form"
	}
	return "list,kanban,form"
}

func normalizeUIActionViewMode(raw, modelName string) (string, error) {
	modes := splitNonEmptyUIAction(raw, ",")
	if len(modes) == 0 {
		return defaultUIActionViewMode(modelName), nil
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(modes)+4)
	appendMode := func(mode string) {
		if _, ok := seen[mode]; ok {
			return
		}
		seen[mode] = struct{}{}
		out = append(out, mode)
	}
	for _, mode := range modes {
		normalized := normalizeViewType(mode)
		if normalized == "" {
			log.Errorf("ui.actions.window invalid view_mode %q", mode)
			return "", fmt.Errorf("ui.actions.window invalid view_mode %q", mode)
		}
		appendMode(normalized)
	}
	appendMode("list")
	appendMode("kanban")
	if strings.TrimSpace(modelName) == "base.workflow" {
		appendMode("canvas")
	}
	appendMode("form")
	return strings.Join(out, ","), nil
}

func normalizeUIActionViews(ctx context.Context, raw any, modelName string) ([]uiActionViewTuple, error) {
	tuples, err := decodeUIActionViews(raw, modelName)
	if err != nil {
		return nil, err
	}
	seen := map[string]struct{}{}
	out := make([]uiActionViewTuple, 0, len(tuples))
	for _, tuple := range tuples {
		if tuple.ID > 0 && strings.TrimSpace(modelName) != "" {
			if err := validateUIViewReference(ctx, tuple.ID, modelName); err != nil {
				return nil, err
			}
		}
		if _, ok := seen[tuple.Type]; ok {
			continue
		}
		seen[tuple.Type] = struct{}{}
		out = append(out, tuple)
	}
	return out, nil
}

// decodeUIActionViews decodes the ui action views handled by this package.
func decodeUIActionViews(raw any, modelName string) ([]uiActionViewTuple, error) {
	if raw == nil {
		return nil, nil
	}
	items, ok := raw.([]any)
	if !ok {
		if typed, ok := raw.([][]any); ok {
			items = make([]any, 0, len(typed))
			for _, item := range typed {
				items = append(items, item)
			}
		} else {
			log.Errorf("ui.actions.window views must be an array")
			return nil, fmt.Errorf("ui.actions.window views must be an array")
		}
	}
	out := make([]uiActionViewTuple, 0, len(items))
	for _, item := range items {
		tuple, err := decodeUIActionViewTuple(item, modelName)
		if err != nil {
			return nil, err
		}
		out = append(out, tuple)
	}
	return out, nil
}

// decodeUIActionViewTuple decodes the ui action view tuple handled by this package.
func decodeUIActionViewTuple(raw any, modelName string) (uiActionViewTuple, error) {
	values, ok := raw.([]any)
	if !ok || len(values) < 2 {
		log.Errorf("ui.actions.window view tuple must be [id, type]")
		return uiActionViewTuple{}, fmt.Errorf("ui.actions.window view tuple must be [id, type]")
	}
	viewID, ok := int64FromAny(values[0])
	if !ok || viewID < 0 {
		log.Errorf("ui.actions.window invalid view id %#v", values[0])
		return uiActionViewTuple{}, fmt.Errorf("ui.actions.window invalid view id %#v", values[0])
	}
	viewType := normalizeViewType(stringValue(values[1]))
	if viewType == "" {
		log.Errorf("ui.actions.window invalid view type %#v", values[1])
		return uiActionViewTuple{}, fmt.Errorf("ui.actions.window invalid view type %#v", values[1])
	}
	if viewID > 0 && strings.TrimSpace(modelName) != "" {
	}
	return uiActionViewTuple{ID: viewID, Type: viewType}, nil
}

// encodeUIActionViews encodes the ui action views handled by this package.
func encodeUIActionViews(tuples []uiActionViewTuple) []any {
	out := make([]any, 0, len(tuples))
	for _, tuple := range tuples {
		out = append(out, []any{tuple.ID, tuple.Type})
	}
	return out
}

func normalizeUIActionContext(raw any) (map[string]any, error) {
	switch typed := raw.(type) {
	case nil:
		return map[string]any{}, nil
	case map[string]any:
		return typed, nil
	case orm.RecordMap:
		return map[string]any(typed), nil
	default:
		log.Errorf("ui.actions.window context must be an object")
		return nil, fmt.Errorf("ui.actions.window context must be an object")
	}
}

func normalizeUIActionDomain(raw any) ([]any, error) {
	switch typed := raw.(type) {
	case nil:
		return []any{}, nil
	case []any:
		return typed, nil
	default:
		log.Errorf("ui.actions.window domain must be an array")
		return nil, fmt.Errorf("ui.actions.window domain must be an array")
	}
}

func normalizeUIActionContextForOutput(raw any) map[string]any {
	value, err := normalizeUIActionContext(raw)
	if err != nil {
		return map[string]any{}
	}
	return value
}

func normalizeUIActionDomainForOutput(raw any) []any {
	value, err := normalizeUIActionDomain(raw)
	if err != nil {
		return []any{}
	}
	return value
}

func isSupportedUIActionTarget(target string) bool {
	switch target {
	case "current", "new", "fullscreen", "main":
		return true
	default:
		return false
	}
}

func optionalInt64Value(raw any) *int64 {
	value, _, err := optionalInt64FromAny(raw)
	if err != nil {
		return nil
	}
	return value
}

func humanizeModelNameUIAction(modelName string) string {
	parts := splitNonEmptyUIAction(modelName, ".")
	if len(parts) == 0 {
		return "Window Action"
	}
	for index, part := range parts {
		parts[index] = humanizeUIActionToken(part)
	}
	return strings.Join(parts, " ")
}

func humanizeUIActionToken(raw string) string {
	raw = strings.ReplaceAll(strings.TrimSpace(raw), "_", " ")
	if raw == "" {
		return ""
	}
	return strings.ToUpper(raw[:1]) + raw[1:]
}

func splitNonEmptyUIAction(value, separator string) []string {
	parts := strings.Split(value, separator)
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// resolveUIActionID resolves the ui action id handled by this package.
func resolveUIActionID(ids []string, args []any, kwargs map[string]any) (int64, error) {
	if len(ids) == 1 {
		actionID, ok := int64FromAny(ids[0])
		if !ok || actionID <= 0 {
			log.Errorf("ui.actions action id must be positive")
			return 0, fmt.Errorf("ui.actions action id must be positive")
		}
		return actionID, nil
	}
	if len(args) > 0 {
		actionID, ok := int64FromAny(args[0])
		if ok && actionID > 0 {
			return actionID, nil
		}
	}
	if kwargs != nil {
		actionID, ok := int64FromAny(kwargs["id"])
		if ok && actionID > 0 {
			return actionID, nil
		}
	}
	log.Errorf("ui.actions action id is required")
	return 0, fmt.Errorf("ui.actions action id is required")
}
