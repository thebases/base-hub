package models

// File overview:
// - Responsibility: verifies kernel model metadata, storage helpers, and registry-backed behavior for ui menu.
// - Key symbols: focused Test cases plus file-local fixtures and assertion helpers.
// - Pattern: Locks down metadata and persistence contracts so schema and runtime helpers stay compatible with the rest of the kernel.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"git.thebase.vn/base_hub/internal/config"
	"git.thebase.vn/base_hub/internal/orm"
)

func TestNormalizeUIMenuValuesBuildsCanonicalURLAction(t *testing.T) {
	// Stub the shared ORM runtime repository seam so menu normalization runs
	// without a registered ui.menu executor.
	restore := orm.StubRuntimeRepository(UIMenuModelName, orm.RuntimeRepositoryStub{
		Read: func(context.Context, []string, []string) ([]orm.RecordMap, error) {
			return nil, nil
		},
	})
	defer restore()

	values, err := normalizeUIMenuValues(context.Background(), orm.RecordMap{
		"name":       "Dashboard",
		"route_path": "/dashboard",
	}, "", true)
	if err != nil {
		t.Fatalf("normalizeUIMenuValues() error = %v", err)
	}
	if got := values["complete_name"]; got != "Dashboard" {
		t.Fatalf("complete_name = %#v, want Dashboard", got)
	}
	action, ok := values["action"].(map[string]any)
	if !ok {
		t.Fatalf("expected action map, got %T", values["action"])
	}
	if got := action["type"]; got != "ui.actions.url" {
		t.Fatalf("action.type = %#v, want ui.actions.url", got)
	}
	if got := action["url"]; got != "/dashboard" {
		t.Fatalf("action.url = %#v, want /dashboard", got)
	}
}

func TestNormalizeUIMenuValuesAcceptsWindowActionReferences(t *testing.T) {
	// Stub the shared ORM runtime repository seam so menu normalization runs
	// without a registered ui.menu executor.
	restore := orm.StubRuntimeRepository(UIMenuModelName, orm.RuntimeRepositoryStub{
		Read: func(context.Context, []string, []string) ([]orm.RecordMap, error) {
			return nil, nil
		},
	})
	defer restore()

	values, err := normalizeUIMenuValues(context.Background(), orm.RecordMap{
		"name": "Contacts",
		"action": map[string]any{
			"type":      UIActionWindowModelName,
			"bid":       "base.action_tbl_partner",
			"sys_model": "tbl.partner",
		},
	}, "", true)
	if err != nil {
		t.Fatalf("normalizeUIMenuValues() error = %v", err)
	}
	action, ok := values["action"].(map[string]any)
	if !ok {
		t.Fatalf("expected action map, got %T", values["action"])
	}
	if got := action["type"]; got != UIActionWindowModelName {
		t.Fatalf("action.type = %#v", got)
	}
	if got := values["route_path"]; got != "/web/tbl.partner" {
		t.Fatalf("route_path = %#v, want /web/tbl.partner", got)
	}
}

func TestComputeUIMenuCompleteNameUsesParentChain(t *testing.T) {
	// Stub the shared ORM runtime repository seam so the parent-chain read is
	// served in-memory.
	restore := orm.StubRuntimeRepository(UIMenuModelName, orm.RuntimeRepositoryStub{
		Read: func(_ context.Context, ids []string, _ []string) ([]orm.RecordMap, error) {
			if len(ids) == 1 && ids[0] == "7" {
				return []orm.RecordMap{{
					"id":            int64(7),
					"complete_name": "Settings",
				}}, nil
			}
			return nil, nil
		},
	})
	defer restore()

	parentID := int64(7)
	got, err := computeUIMenuCompleteName(context.Background(), "Users", &parentID)
	if err != nil {
		t.Fatalf("computeUIMenuCompleteName() error = %v", err)
	}
	if got != "Settings / Users" {
		t.Fatalf("computeUIMenuCompleteName() = %q, want %q", got, "Settings / Users")
	}
}

func TestBuildUIMenuSidebarTreeBuildsNestedRows(t *testing.T) {
	rootID := int64(10)
	rows := []uiMenuTreeRow{
		{
			ID:        rootID,
			Name:      "Settings",
			Sequence:  10,
			Icon:      "settings-2",
			RoutePath: "/settings",
			Action: map[string]any{
				"type": "ui.actions.url",
				"url":  "/settings",
			},
		},
		{
			ID:       20,
			Name:     "Users",
			Sequence: 20,
			ParentID: &rootID,
			Icon:     "users",
			Action: map[string]any{
				"type": "ui.actions.url",
				"url":  "/settings/users",
			},
		},
	}

	tree := buildUIMenuSidebarTree(rows, map[int64]bool{})
	if len(tree) != 1 {
		t.Fatalf("root count = %d, want 1", len(tree))
	}
	if got := tree[0]["name"]; got != "Settings" {
		t.Fatalf("root name = %#v, want Settings", got)
	}
	children, ok := tree[0]["child_ids"].([]map[string]any)
	if !ok {
		t.Fatalf("child_ids type = %T, want []map[string]any", tree[0]["child_ids"])
	}
	if len(children) != 1 {
		t.Fatalf("child count = %d, want 1", len(children))
	}
	if got := children[0]["url"]; got != "/settings/users" {
		t.Fatalf("child url = %#v, want /settings/users", got)
	}
}

func TestOptionalInt64FromAnyAcceptsMany2OneTuple(t *testing.T) {
	value, ok, err := optionalInt64FromAny([]any{int64(7), "Settings"})
	if err != nil {
		t.Fatalf("optionalInt64FromAny() error = %v", err)
	}
	if !ok || value == nil || *value != 7 {
		t.Fatalf("optionalInt64FromAny() = %#v, %v, want 7", value, ok)
	}
}

func TestOptionalInt64FromAnyTreatsFalseAsClearedRelation(t *testing.T) {
	value, ok, err := optionalInt64FromAny(false)
	if err != nil {
		t.Fatalf("optionalInt64FromAny(false) error = %v", err)
	}
	if ok || value != nil {
		t.Fatalf("optionalInt64FromAny(false) = %#v, %v, want nil, false", value, ok)
	}
}

func TestLoadMenusUIMenuReturnsSidebarPayload(t *testing.T) {
	ensureUIMenuAllowedTargetModel(t, "tbl.partner")
	originalLoadWindowAction := uiMenuLoadWindowAction
	defer func() {
		uiMenuLoadWindowAction = originalLoadWindowAction
	}()
	// Stub the shared ORM runtime repository seam for the menu rows and the
	// sys.model.data xml-id resolution feeding the sidebar payload.
	restoreMenu := orm.StubRuntimeRepository(UIMenuModelName, orm.RuntimeRepositoryStub{
		SearchRead: func(_ context.Context, _ orm.RecordMap, _ []string) ([]orm.RecordMap, error) {
			return []orm.RecordMap{
				{
					"id":         int64(1),
					"name":       "Home",
					"active":     true,
					"sequence":   int64(5),
					"route_path": "",
					"icon":       "home",
					"updated_at": time.Unix(0, 1234).UTC(),
					"action": map[string]any{
						"type": UIActionWindowModelName,
						"bid":  "base.action_home",
					},
				},
			}, nil
		},
	})
	defer restoreMenu()
	restoreBID := orm.StubRuntimeRepository(SysModelDataModelName, orm.RuntimeRepositoryStub{
		SearchRead: func(_ context.Context, _ orm.RecordMap, _ []string) ([]orm.RecordMap, error) {
			return []orm.RecordMap{{
				"model":    UIActionWindowModelName,
				"model_id": int64(42),
			}}, nil
		},
	})
	defer restoreBID()
	uiMenuLoadWindowAction = func(_ context.Context, actionID int64) (map[string]any, error) {
		return map[string]any{
			"type":           UIActionWindowModelName,
			"id":             actionID,
			"name":           "Home",
			"sys_model":      "tbl.partner",
			"target":         "current",
			"context":        map[string]any{},
			"domain":         []any{},
			"views":          []any{},
			"view_mode":      "list,form",
			"url":            "/web/tbl.partner",
			"help":           "",
			"limit":          int64(80),
			"search_view_id": []any{0, "search"},
		}, nil
	}

	ctx := orm.ContextWithEnvironment(
		context.Background(),
		orm.NewEnvironmentWithDBName(
			nil,
			"tenant_a",
			7,
			map[string]any{
				orm.ContextKeyCurrentCompanyID:     int64(20),
				orm.ContextKeyAllowedCompanyIDs:    []int64{20, 10},
				orm.ContextKeyAuthorizedCompanyIDs: []int64{20, 10},
			},
			false,
		),
	)
	result, err := loadMenusUIMenu(ctx, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("loadMenusUIMenu() error = %v", err)
	}
	payload, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("expected payload map, got %T", result)
	}
	scopeKey, ok := payload["menu_scope_key"].(string)
	if !ok {
		t.Fatalf("menu_scope_key type = %T, want string", payload["menu_scope_key"])
	}
	if !strings.Contains(scopeKey, "db=tenant_a") || !strings.Contains(scopeKey, "user=7") {
		t.Fatalf("menu_scope_key = %q, want db/user scope markers", scopeKey)
	}
	if got := payload["menu_version"]; got != int64(1234) {
		t.Fatalf("menu_version = %#v, want 1234", got)
	}
	cacheKey, ok := payload["menu_cache_key"].(string)
	if !ok {
		t.Fatalf("menu_cache_key type = %T, want string", payload["menu_cache_key"])
	}
	if !strings.Contains(cacheKey, scopeKey) {
		t.Fatalf("menu_cache_key = %q, want embedded scope %q", cacheKey, scopeKey)
	}
	records, ok := payload["records"].([]map[string]any)
	if !ok {
		t.Fatalf("records type = %T, want []map[string]any", payload["records"])
	}
	if len(records) != 1 {
		t.Fatalf("records len = %d, want 1", len(records))
	}
	if got := records[0]["url"]; got != "/web/tbl.partner" {
		t.Fatalf("records[0].url = %#v, want /web/tbl.partner", got)
	}
	action, ok := records[0]["action"].(map[string]any)
	if !ok {
		t.Fatalf("records[0].action type = %T", records[0]["action"])
	}
	if got := action["id"]; got != int64(42) {
		t.Fatalf("records[0].action.id = %#v, want 42", got)
	}
}

func TestAllowUIMenuRowForCurrentDBHidesDatabaseMenuOnServiceDB(t *testing.T) {
	previous := *config.Get()
	t.Cleanup(func() {
		config.Set(previous)
	})
	config.Update(func(c *config.AppConfig) { c.PostgresqlDb.HomeDBName = "home" })

	ctx := orm.ContextWithEnvironment(context.Background(), orm.NewEnvironmentWithDBName(nil, "tenant_a", 1, nil, false))
	allowed := allowUIMenuRowForCurrentDB(ctx, uiMenuTreeRow{
		RoutePath: "/web/base.database",
		Action: map[string]any{
			"sys_model": "base.database",
		},
	})
	if allowed {
		t.Fatalf("allowUIMenuRowForCurrentDB() expected database menu to be hidden on service DB")
	}
}

func TestAllowUIMenuRowForCurrentDBKeepsDatabaseMenuOnHomeDB(t *testing.T) {
	previous := *config.Get()
	t.Cleanup(func() {
		config.Set(previous)
	})
	config.Update(func(c *config.AppConfig) { c.PostgresqlDb.HomeDBName = "home" })

	ctx := orm.ContextWithEnvironment(context.Background(), orm.NewEnvironmentWithDBName(nil, "home", 1, nil, false))
	allowed := allowUIMenuRowForCurrentDB(ctx, uiMenuTreeRow{
		RoutePath: "/web/base.database",
		Action: map[string]any{
			"sys_model": "base.database",
		},
	})
	if !allowed {
		t.Fatalf("allowUIMenuRowForCurrentDB() expected database menu to stay visible on home DB")
	}
}

func TestAllowUIMenuRowForCurrentDBHidesHealthCheckMenuOnServiceDB(t *testing.T) {
	previous := *config.Get()
	t.Cleanup(func() {
		config.Set(previous)
	})
	config.Update(func(c *config.AppConfig) { c.PostgresqlDb.HomeDBName = "home" })

	ctx := orm.ContextWithEnvironment(context.Background(), orm.NewEnvironmentWithDBName(nil, "tenant_a", 1, nil, false))
	allowed := allowUIMenuRowForCurrentDB(ctx, uiMenuTreeRow{
		RoutePath: "/web/system-health",
	})
	if allowed {
		t.Fatalf("allowUIMenuRowForCurrentDB() expected health check menu to be hidden on service DB")
	}
}

func TestAllowUIMenuRowForCurrentDBKeepsHealthCheckMenuOnHomeDB(t *testing.T) {
	previous := *config.Get()
	t.Cleanup(func() {
		config.Set(previous)
	})
	config.Update(func(c *config.AppConfig) { c.PostgresqlDb.HomeDBName = "home" })

	ctx := orm.ContextWithEnvironment(context.Background(), orm.NewEnvironmentWithDBName(nil, "home", 1, nil, false))
	allowed := allowUIMenuRowForCurrentDB(ctx, uiMenuTreeRow{
		RoutePath: "/web/system-health",
	})
	if !allowed {
		t.Fatalf("allowUIMenuRowForCurrentDB() expected health check menu to stay visible on home DB")
	}
}

type uiMenuDeniedTargetTestRecord struct {
	orm.BaseModel
	Name string `bun:"name" json:"name"`
}

type uiMenuAllowedTargetTestRecord struct {
	orm.BaseModel
	Name string `bun:"name" json:"name"`
}

func ensureUIMenuDeniedTargetModel(t *testing.T, modelName string) {
	t.Helper()
	if _, ok := orm.LookupModel(modelName); ok {
		return
	}
	if _, err := orm.RegisterModelType[uiMenuDeniedTargetTestRecord](orm.ModelSpec{
		Name:       modelName,
		Label:      "UI Menu Denied Target",
		Definition: orm.ModelDefinition(modelName, (*uiMenuDeniedTargetTestRecord)(nil)),
	}.WithStorageName("test_" + strings.ReplaceAll(modelName, ".", "_"))); err != nil {
		t.Fatalf("register model %q: %v", modelName, err)
	}
}

func ensureUIMenuAllowedTargetModel(t *testing.T, modelName string) {
	t.Helper()
	if _, ok := orm.LookupModel(modelName); ok {
		return
	}
	if _, err := orm.RegisterModelType[uiMenuAllowedTargetTestRecord](orm.ModelSpec{
		Name:       modelName,
		Label:      "UI Menu Allowed Target",
		Definition: orm.ModelDefinition(modelName, (*uiMenuAllowedTargetTestRecord)(nil)),
	}.WithStorageName("test_" + strings.ReplaceAll(modelName, ".", "_"))); err != nil {
		t.Fatalf("register model %q: %v", modelName, err)
	}
}

func TestAllowUIMenuRowForCurrentUserHidesUnreadableModel(t *testing.T) {
	const modelName = "test.ui_menu_denied_target"
	ensureUIMenuDeniedTargetModel(t, modelName)
	orm.RegisterAccessHandler(modelName, func(context.Context, *orm.Environment, *orm.ModelMetadata, string, []string, orm.RecordMap) error {
		return errors.New("permission denied: user 5 cannot read model \"" + modelName + "\"")
	})
	t.Cleanup(func() {
		orm.RegisterAccessHandler(modelName, func(context.Context, *orm.Environment, *orm.ModelMetadata, string, []string, orm.RecordMap) error {
			return nil
		})
	})

	ctx := orm.ContextWithEnvironment(context.Background(), orm.NewEnvironmentWithDBName(nil, "home", 5, nil, false))
	row := uiMenuTreeRow{Action: map[string]any{"sys_model": modelName}}
	if allowUIMenuRowForCurrentUser(ctx, row) {
		t.Fatalf("allowUIMenuRowForCurrentUser() expected menu targeting an unreadable model to be hidden")
	}
}

func TestAllowUIMenuRowForCurrentUserKeepsReadableModel(t *testing.T) {
	const modelName = "test.ui_menu_allowed_target"
	ensureUIMenuAllowedTargetModel(t, modelName)
	orm.RegisterAccessHandler(modelName, func(context.Context, *orm.Environment, *orm.ModelMetadata, string, []string, orm.RecordMap) error {
		return nil
	})

	ctx := orm.ContextWithEnvironment(context.Background(), orm.NewEnvironmentWithDBName(nil, "home", 5, nil, false))
	row := uiMenuTreeRow{Action: map[string]any{"sys_model": modelName}}
	if !allowUIMenuRowForCurrentUser(ctx, row) {
		t.Fatalf("allowUIMenuRowForCurrentUser() expected menu targeting a readable model to stay visible")
	}
}

func TestAllowUIMenuRowForCurrentUserKeepsURLOnlyMenu(t *testing.T) {
	ctx := orm.ContextWithEnvironment(context.Background(), orm.NewEnvironmentWithDBName(nil, "home", 5, nil, false))
	if !allowUIMenuRowForCurrentUser(ctx, uiMenuTreeRow{RoutePath: "/web/system-health"}) {
		t.Fatalf("allowUIMenuRowForCurrentUser() expected URL-only menu without a sys_model to stay visible")
	}
}

func TestAllowUIMenuRowForCurrentUserHidesUnregisteredModel(t *testing.T) {
	ctx := orm.ContextWithEnvironment(context.Background(), orm.NewEnvironmentWithDBName(nil, "home", 5, nil, false))
	row := uiMenuTreeRow{Action: map[string]any{"sys_model": "edc.device.group"}}
	if allowUIMenuRowForCurrentUser(ctx, row) {
		t.Fatalf("allowUIMenuRowForCurrentUser() expected menu targeting an unregistered model to be hidden")
	}
}
