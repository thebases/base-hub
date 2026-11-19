package models

// File overview:
// - Responsibility: shared "create UI" scaffolding logic for a registered model — one list
//   ui.view, one form ui.view, and one ui.menu entry always nested under menu.main.
// - Key symbols: `CreateUIForModel`.
// - Pattern: called both by the sys.model "Create UI" object button (createUISysModel in
//   sys_model.go) and by the base.model.create_ui MCP tool (base-mcp/routes/mcp_route.go), so
//   the scaffold + menu.main enforcement + has_ui stamping only lives in one place.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"git.thebase.vn/base_hub/internal/orm"
)

// CreateUIForModel scaffolds a default admin screen for modelName: a list
// ui.view and a form ui.view (both auto-generated from field metadata via
// SynthesizeViewArch), plus one ui.menu entry that opens "/web/<model>".
// The menu is always parented under menu.main regardless of caller intent —
// there is deliberately no parent_id parameter here — so scaffolded screens
// can never be created as orphan roots or nested under an arbitrary menu.
// On success it stamps the model's sys.model row has_ui=true so the button/
// tool becomes a no-op-safe "already scaffolded" marker for custom models
// (Go-coded models are already true from registry sync, see
// orm.newSystemModelRow).
func CreateUIForModel(ctx context.Context, modelName, label string) (menu orm.RecordMap, listView orm.RecordMap, formView orm.RecordMap, err error) {
	modelName = strings.TrimSpace(modelName)
	targetMeta, ok := orm.LookupModel(modelName)
	if !ok {
		return nil, nil, nil, fmt.Errorf("model %q is not registered", modelName)
	}
	if strings.TrimSpace(label) == "" {
		label = ViewLabelForModel(targetMeta)
	}

	listView, err = createUIScaffoldView(ctx, targetMeta, modelName, "list")
	if err != nil {
		return nil, nil, nil, err
	}
	formView, err = createUIScaffoldView(ctx, targetMeta, modelName, "form")
	if err != nil {
		return nil, nil, nil, err
	}

	mainMenuID, err := LookupMenuMainID(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	if mainMenuID <= 0 {
		return nil, nil, nil, fmt.Errorf("main menu (menu.main) is not registered; cannot scaffold ui.menu")
	}

	menuResult, err := orm.ExecuteRuntimeMethod(ctx, UIMenuModelName, "create", nil, []any{map[string]any{
		"name":       label,
		"route_path": "/web/" + modelName,
		"parent_id":  mainMenuID,
	}}, nil)
	if err != nil {
		return nil, nil, nil, err
	}
	menu, _ = menuResult.(orm.RecordMap)

	if err := MarkSysModelHasUI(ctx, modelName); err != nil {
		return nil, nil, nil, err
	}
	return menu, listView, formView, nil
}

// LookupMenuMainID resolves the root "Main" menu's live row id from its
// seeded external id menu.main (modules/base/data/12_init_ui_menu.json) via
// the sys.model.data catalog. Exported so every "create UI" entry point —
// this package's CreateUIForModel and the base.model.create_ui MCP tool in
// base-mcp/routes/mcp_route.go — forces the same target instead of trusting
// a caller-supplied parent_id, so a scaffolded menu can never end up orphaned
// or nested somewhere unexpected.
func LookupMenuMainID(ctx context.Context) (int64, error) {
	return lookupUIMenuActionTargetID(ctx, "menu.main", UIMenuModelName)
}

// createUIScaffoldView creates one ui.view row of viewType for modelName via
// the model's own registered "create" runtime method, so ui.view's own
// normalization (normalizeUIViewValues) still runs — a direct repository
// insert would bypass it.
func createUIScaffoldView(ctx context.Context, targetMeta *orm.ModelMetadata, modelName, viewType string) (orm.RecordMap, error) {
	result, err := orm.ExecuteRuntimeMethod(ctx, UIViewModelName, "create", nil, []any{map[string]any{
		"name":  fmt.Sprintf("%s.%s", modelName, viewType),
		"model": modelName,
		"type":  viewType,
		"arch":  SynthesizeViewArch(targetMeta, viewType),
	}}, nil)
	if err != nil {
		return nil, err
	}
	view, _ := result.(orm.RecordMap)
	return view, nil
}

// MarkSysModelHasUI flips has_ui=true on modelName's sys.model row. Scoped
// by name rather than id since callers here (both create_ui paths) only
// have the model name in hand at this point. Exported for the same reason as
// LookupMenuMainID — the base.model.create_ui MCP tool needs to stamp this
// too after its own (ACL-checked) view/menu creation succeeds.
func MarkSysModelHasUI(ctx context.Context, modelName string) error {
	db := sysModelMetadataDB(ctx)
	if db == nil {
		return fmt.Errorf("sys.model database is not initialized")
	}
	_, err := db.NewUpdate().
		Model((*SysModelRecord)(nil)).
		Set("has_ui = ?", true).
		Set("updated_at = ?", time.Now().UTC()).
		Where("name = ?", modelName).
		Exec(ctx)
	return err
}
