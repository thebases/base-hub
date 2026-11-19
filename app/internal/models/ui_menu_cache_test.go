package models

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"git.thebase.vn/base_hub/internal/config"
	"git.thebase.vn/base_hub/internal/orm"
)

func TestUIMenuCacheEntryRoundTripKeepsGatingFields(t *testing.T) {
	parent := int64(1)
	entry := uiMenuCacheEntry{
		Version:     uiMenuCacheSchemaVersion,
		Fingerprint: "1@a|2@b",
		Rows: []uiMenuTreeRow{{
			ID: 2, Name: "Partners", ParentID: &parent, Sequence: 5, CompleteName: "Admin / Partners",
			RoutePath: "/web/tbl.partner", Icon: "users",
			Action:    map[string]any{"type": UIActionWindowModelName, "id": int64(9), "sys_model": "tbl.partner"},
			UpdatedAt: time.Unix(1700000000, 0).UTC(), GroupBIDs: []string{"base.group_system"},
		}},
		ParentsWithChildren: []int64{1},
	}
	raw, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	var back uiMenuCacheEntry
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	got := back.Rows[0]
	if len(got.GroupBIDs) != 1 || got.GroupBIDs[0] != "base.group_system" {
		t.Fatalf("group_bids lost in cache round trip: %v", got.GroupBIDs)
	}
	// JSON turns int64 into float64; the gates must still read the model and id.
	if model := stringValue(got.Action["sys_model"]); model != "tbl.partner" {
		t.Fatalf("sys_model = %q", model)
	}
	if id, ok := int64FromAny(got.Action["id"]); !ok || id != 9 {
		t.Fatalf("action id = %v", got.Action["id"])
	}
	if got.ParentID == nil || *got.ParentID != 1 || !got.UpdatedAt.Equal(entry.Rows[0].UpdatedAt) {
		t.Fatalf("parent/updated_at lost: %+v", got)
	}
	if !hadChildrenFromParents(back.ParentsWithChildren)[1] {
		t.Fatal("parents_with_children lost")
	}
}

func TestUIMenuCacheKvtNameIsPerLanguage(t *testing.T) {
	envFor := func(lang string) *orm.Environment {
		return orm.NewEnvironment(nil, 1, map[string]any{"lang": lang}, false)
	}
	if got := uiMenuCacheKvtName(envFor("vi_VN")); got != "ui.menu.resolved:vi_VN" {
		t.Fatalf("name = %q", got)
	}
	if got := uiMenuCacheKvtName(envFor("x'; drop")); got != "ui.menu.resolved:en" {
		t.Fatalf("invalid lang must fall back to en, got %q", got)
	}
}

func TestReadUIMenuCacheEntryDecodesJSONValue(t *testing.T) {
	restore := orm.StubRuntimeRepository(uiMenuKvtModelName, orm.RuntimeRepositoryStub{
		SearchRead: func(_ context.Context, _ orm.RecordMap, _ []string) ([]orm.RecordMap, error) {
			return []orm.RecordMap{{"id": float64(7), "value": map[string]any{"version": float64(uiMenuCacheSchemaVersion), "fingerprint": "f", "rows": []any{}}}}, nil
		},
	})
	defer restore()
	id, entry, err := readUIMenuCacheEntry(context.Background(), "k")
	if err != nil || id != 7 || entry == nil || entry.Fingerprint != "f" {
		t.Fatalf("id=%d entry=%+v err=%v", id, entry, err)
	}
}

func TestReadUIMenuCacheEntryTreatsCorruptValueAsMiss(t *testing.T) {
	restore := orm.StubRuntimeRepository(uiMenuKvtModelName, orm.RuntimeRepositoryStub{
		SearchRead: func(_ context.Context, _ orm.RecordMap, _ []string) ([]orm.RecordMap, error) {
			return []orm.RecordMap{{"id": int64(3), "value": "{not json"}}, nil
		},
	})
	defer restore()
	id, entry, err := readUIMenuCacheEntry(context.Background(), "k")
	if err != nil || id != 3 || entry != nil {
		t.Fatalf("corrupt entry must be a miss that keeps the row id: id=%d entry=%+v err=%v", id, entry, err)
	}
}

func TestHasRecordRuleHandlerDetectsModelSpecificProvider(t *testing.T) {
	const model = "test.ui_menu_cache_rule_probe"
	if orm.HasRecordRuleHandler(model) {
		t.Fatal("unregistered model must not report a rule handler")
	}
	orm.RegisterRecordRule(model, func(context.Context, *orm.Environment, *orm.ModelMetadata, string) (orm.DomainNode, error) {
		return orm.TrueDomain(), nil
	})
	if !orm.HasRecordRuleHandler(model) {
		t.Fatal("registered model-specific handler must be reported")
	}
}

// The no-cache path must not pay for action resolution on rows the cheap gates
// already drop (here: the base.database menu on a non-home DB).
func TestBuildUIMenuCallerRowsSkipsResolutionForGatedRows(t *testing.T) {
	previous := *config.Get()
	t.Cleanup(func() { config.Set(previous) })
	config.Update(func(c *config.AppConfig) { c.PostgresqlDb.HomeDBName = "home" })

	original := uiMenuLoadWindowAction
	defer func() { uiMenuLoadWindowAction = original }()
	resolvedIDs := []int64{}
	uiMenuLoadWindowAction = func(_ context.Context, actionID int64) (map[string]any, error) {
		resolvedIDs = append(resolvedIDs, actionID)
		return map[string]any{"type": UIActionWindowModelName, "id": actionID, "sys_model": "x.y"}, nil
	}
	restore := orm.StubRuntimeRepository(UIMenuModelName, orm.RuntimeRepositoryStub{
		SearchRead: func(context.Context, orm.RecordMap, []string) ([]orm.RecordMap, error) {
			return []orm.RecordMap{
				{"id": int64(1), "name": "Databases", "active": true, "route_path": "/web/base.database",
					"action": map[string]any{"type": UIActionWindowModelName, "id": int64(11)}},
				{"id": int64(2), "name": "Other", "active": true, "route_path": "/web/x.y",
					"action": map[string]any{"type": UIActionWindowModelName, "id": int64(22)}},
			}, nil
		},
	})
	defer restore()

	ctx := orm.ContextWithEnvironment(context.Background(), orm.NewEnvironmentWithDBName(nil, "tenant_a", 1, nil, false))
	rows, _, err := buildUIMenuCallerRows(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ID != 2 {
		t.Fatalf("rows = %+v, want only menu 2", rows)
	}
	if len(resolvedIDs) != 1 || resolvedIDs[0] != 22 {
		t.Fatalf("resolved action ids = %v, want only [22]", resolvedIDs)
	}
}
