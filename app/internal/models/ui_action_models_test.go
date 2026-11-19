package models

// File overview:
// - Responsibility: verifies kernel model metadata, storage helpers, and registry-backed behavior for ui action models.
// - Key symbols: focused Test cases plus file-local fixtures and assertion helpers.
// - Pattern: Locks down metadata and persistence contracts so schema and runtime helpers stay compatible with the rest of the kernel.

import (
	"context"
	"strings"
	"testing"

	"git.thebase.vn/base_hub/internal/orm"
)

type uiActionTargetTestRecord struct {
	orm.BaseModel
	Name string `bun:"name" json:"name"`
}

type uiActionWorkflowTargetTestRecord struct {
	orm.BaseModel
	Name string `bun:"name" json:"name"`
}

func ensureUIActionTargetModel(t *testing.T, modelName string) {
	t.Helper()
	if _, ok := orm.LookupModel(modelName); ok {
		return
	}
	if _, err := orm.RegisterModelType[uiActionTargetTestRecord](orm.ModelSpec{
		Name:       modelName,
		Label:      "UI Action Target",
		Definition: orm.ModelDefinition(modelName, (*uiActionTargetTestRecord)(nil)),
	}.WithStorageName("test_" + strings.ReplaceAll(modelName, ".", "_"))); err != nil {
		t.Fatalf("register model %q: %v", modelName, err)
	}
}

func ensureUIActionWorkflowTargetModel(t *testing.T, modelName string) {
	t.Helper()
	if _, ok := orm.LookupModel(modelName); ok {
		return
	}
	if _, err := orm.RegisterModelType[uiActionWorkflowTargetTestRecord](orm.ModelSpec{
		Name:       modelName,
		Label:      "Workflow UI Action Target",
		Definition: orm.ModelDefinition(modelName, (*uiActionWorkflowTargetTestRecord)(nil)),
	}.WithStorageName("test_" + strings.ReplaceAll(modelName, ".", "_"))); err != nil {
		t.Fatalf("register model %q: %v", modelName, err)
	}
}

func TestNormalizeUIActionWindowValuesDefaults(t *testing.T) {
	const modelName = "test.ui_action_window_target"
	ensureUIActionTargetModel(t, modelName)

	values, err := normalizeUIActionWindowValues(context.Background(), orm.RecordMap{
		"sys_model": modelName,
	}, true)
	if err != nil {
		t.Fatalf("normalizeUIActionWindowValues() error = %v", err)
	}
	if got := values["type"]; got != UIActionWindowModelName {
		t.Fatalf("type = %#v, want %q", got, UIActionWindowModelName)
	}
	if got := values["target"]; got != "current" {
		t.Fatalf("target = %#v, want current", got)
	}
	if got := values["view_mode"]; got != "list,kanban,form" {
		t.Fatalf("view_mode = %#v, want list,kanban,form", got)
	}
	if got := values["limit"]; got != int64(80) {
		t.Fatalf("limit = %#v, want 80", got)
	}
}

func TestNormalizeUIActionWindowValuesWorkflowAddsCanvasDefault(t *testing.T) {
	ensureUIActionWorkflowTargetModel(t, "base.workflow")
	values, err := normalizeUIActionWindowValues(context.Background(), orm.RecordMap{
		"sys_model": "base.workflow",
	}, true)
	if err != nil {
		t.Fatalf("normalizeUIActionWindowValues() error = %v", err)
	}
	if got := values["view_mode"]; got != "list,kanban,canvas,form" {
		t.Fatalf("view_mode = %#v, want list,kanban,canvas,form", got)
	}
}

func TestNormalizeUIActionViewModeForcesListKanban(t *testing.T) {
	got, err := normalizeUIActionViewMode("form", "tbl.partner")
	if err != nil {
		t.Fatalf("normalizeUIActionViewMode() error = %v", err)
	}
	if got != "form,list,kanban" && got != "list,kanban,form" {
		t.Fatalf("view_mode = %q, want form+list+kanban", got)
	}

	got, err = normalizeUIActionViewMode("form", "base.workflow")
	if err != nil {
		t.Fatalf("normalizeUIActionViewMode() error = %v", err)
	}
	if !strings.HasPrefix(got, "form") || !strings.Contains(got, "list") || !strings.Contains(got, "kanban") || !strings.Contains(got, "canvas") {
		t.Fatalf("view_mode = %q, want form+list+kanban+canvas", got)
	}
}

func TestNormalizeUIActionViewModePreservesDeclaredOrder(t *testing.T) {
	got, err := normalizeUIActionViewMode("canvas,list,kanban", "base.workflow")
	if err != nil {
		t.Fatalf("normalizeUIActionViewMode() error = %v", err)
	}
	if got != "canvas,list,kanban,form" {
		t.Fatalf("view_mode = %q, want canvas,list,kanban,form", got)
	}
}

func TestBuildUIActionWindowDictUsesStoredViewPayload(t *testing.T) {
	originalLookup := uiActionWindowLookupDefaultViewID
	defer func() {
		uiActionWindowLookupDefaultViewID = originalLookup
	}()
	uiActionWindowLookupDefaultViewID = func(context.Context, string, string) (int64, error) {
		return 0, nil
	}

	result, err := buildUIActionWindowDict(context.Background(), orm.RecordMap{
		"id":        int64(12),
		"name":      "CRM",
		"sys_model": "crm.lead",
		"target":    "current",
		"context":   map[string]any{"lang": "en_US"},
		"domain":    []any{[]any{"active", "=", true}},
		"view_mode": "list,form",
		"views": []any{
			[]any{int64(11), "list"},
			[]any{int64(22), "form"},
		},
		"search_view_id": int64(33),
		"help":           "Open leads",
		"limit":          int64(40),
	})
	if err != nil {
		t.Fatalf("buildUIActionWindowDict() error = %v", err)
	}
	if got := result["type"]; got != UIActionWindowModelName {
		t.Fatalf("type = %#v, want %q", got, UIActionWindowModelName)
	}
	views, ok := result["views"].([]any)
	if !ok || len(views) < 2 {
		t.Fatalf("views = %#v, want at least 2 tuples", result["views"])
	}
	first, ok := views[0].([]any)
	if !ok || len(first) != 2 || first[0] != int64(11) || first[1] != "list" {
		t.Fatalf("first view tuple = %#v, want [11 list]", views[0])
	}
	second, ok := views[1].([]any)
	if !ok || len(second) != 2 || second[0] != int64(22) || second[1] != "form" {
		t.Fatalf("second view tuple = %#v, want [22 form]", views[1])
	}
	searchView, ok := result["search_view_id"].([]any)
	if !ok || len(searchView) != 2 || searchView[0] != int64(33) || searchView[1] != "search" {
		t.Fatalf("search_view_id = %#v, want [33 search]", result["search_view_id"])
	}
}

func TestBuildUIActionWindowDictPrefersConcreteViewIDType(t *testing.T) {
	originalLookup := uiActionWindowLookupDefaultViewID
	originalRead := uiActionWindowLoadViewByID
	defer func() {
		uiActionWindowLookupDefaultViewID = originalLookup
		uiActionWindowLoadViewByID = originalRead
	}()
	uiActionWindowLookupDefaultViewID = func(context.Context, string, string) (int64, error) {
		return 0, nil
	}
	uiActionWindowLoadViewByID = func(_ context.Context, viewID int64) (sysUIViewRow, error) {
		if viewID == 91 {
			return sysUIViewRow{
				ID:    91,
				Model: "base.workflow",
				Type:  "canvas",
				Name:  "base.workflow.canvas",
			}, nil
		}
		return sysUIViewRow{}, nil
	}

	result, err := buildUIActionWindowDict(context.Background(), orm.RecordMap{
		"id":        int64(12),
		"name":      "Workflow",
		"sys_model": "base.workflow",
		"target":    "current",
		"view_mode": "list,kanban",
		"view_id":   int64(91),
		"limit":     int64(80),
	})
	if err != nil {
		t.Fatalf("buildUIActionWindowDict() error = %v", err)
	}
	if got := result["view_mode"]; got != "canvas,list,kanban,form" {
		t.Fatalf("view_mode = %#v, want canvas,list,kanban,form", got)
	}
	views, ok := result["views"].([]any)
	if !ok || len(views) == 0 {
		t.Fatalf("views = %#v, want tuples", result["views"])
	}
	first, ok := views[0].([]any)
	if !ok || len(first) != 2 || first[0] != int64(91) || first[1] != "canvas" {
		t.Fatalf("first view tuple = %#v, want [91 canvas]", views[0])
	}
}

func TestNormalizeUIActionClientValuesDefaults(t *testing.T) {
	values, err := normalizeUIActionClientValues(orm.RecordMap{
		"tag": "demo.client.action",
	}, true)
	if err != nil {
		t.Fatalf("normalizeUIActionClientValues() error = %v", err)
	}
	if got := values["type"]; got != UIActionClientModelName {
		t.Fatalf("type = %#v, want %q", got, UIActionClientModelName)
	}
	if got := values["name"]; got != "demo.client.action" {
		t.Fatalf("name = %#v, want demo.client.action", got)
	}
	if got := values["target"]; got != "current" {
		t.Fatalf("target = %#v, want current", got)
	}
}

func TestGetActionDictUIActionClientReturnsCanonicalPayload(t *testing.T) {
	// Stub the shared ORM runtime repository seam so the action-dict payload is
	// produced without a registered ui.actions.client executor.
	restore := orm.StubRuntimeRepository(UIActionClientModelName, orm.RuntimeRepositoryStub{
		Read: func(context.Context, []string, []string) ([]orm.RecordMap, error) {
			return []orm.RecordMap{{
				"id":        int64(7),
				"name":      "Open Inbox",
				"tag":       "mail.inbox",
				"target":    "current",
				"sys_model": "mail.message",
				"context":   map[string]any{"lang": "en_US"},
				"params":    map[string]any{"folder": "inbox"},
			}}, nil
		},
	})
	defer restore()

	result, err := getActionDictUIActionClient(context.Background(), nil, []string{"7"}, nil, nil)
	if err != nil {
		t.Fatalf("getActionDictUIActionClient() error = %v", err)
	}
	payload, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("expected payload map, got %T", result)
	}
	if got := payload["type"]; got != UIActionClientModelName {
		t.Fatalf("type = %#v, want %q", got, UIActionClientModelName)
	}
	if got := payload["tag"]; got != "mail.inbox" {
		t.Fatalf("tag = %#v, want mail.inbox", got)
	}
}
