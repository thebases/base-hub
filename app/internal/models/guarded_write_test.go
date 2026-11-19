package models

// File overview:
// - Responsibility: pins the T87 switch of kernel create/write overrides to orm.CreateRecord/WriteRecords.
// - Key symbols: `TestWriteUIMenuRejectsComputedField`, `TestCreateUIActionClientRunsGuardedCreate`.

import (
	"context"
	"strings"
	"testing"

	"git.thebase.vn/base_hub/internal/orm"
)

// ensureKernelModel returns the registered metadata for name, registering T's spec only when absent.
func ensureKernelModel[T any](t *testing.T, name string, spec orm.ModelSpec) *orm.ModelMetadata {
	t.Helper()
	if meta, ok := orm.LookupModel(name); ok {
		return meta
	}
	meta, err := orm.RegisterModelType[T](spec)
	if err != nil {
		t.Fatalf("register %s: %v", name, err)
	}
	return meta
}

// The ui.menu write override now goes through orm.WriteRecords, so a payload touching the
// non-stored computed statusbar_visible is refused before the repository write runs.
func TestWriteUIMenuRejectsComputedField(t *testing.T) {
	meta := ensureKernelModel[UIMenuRecord](t, UIMenuModelName, UIMenuModelSpec)
	wrote := false
	restore := orm.StubRuntimeRepository(UIMenuModelName, orm.RuntimeRepositoryStub{
		Read: func(context.Context, []string, []string) ([]orm.RecordMap, error) { return nil, nil },
		Write: func(context.Context, []string, orm.RecordMap) (bool, error) {
			wrote = true
			return true, nil
		},
	})
	defer restore()

	_, err := writeUIMenu(context.Background(), meta, []string{"1"}, []any{map[string]any{"statusbar_visible": "draft"}}, nil)
	if err == nil || !strings.Contains(err.Error(), "computed") {
		t.Fatalf("writeUIMenu() error = %v, want computed-field rejection", err)
	}
	if wrote {
		t.Fatal("repository write ran despite the computed-field rejection")
	}
}

// The ui.actions.client create override now goes through orm.CreateRecord: the override's own
// normalisation still applies, and the guarded path canonicalises the payload (unknown keys dropped)
// before the repository insert.
func TestCreateUIActionClientRunsGuardedCreate(t *testing.T) {
	meta := ensureKernelModel[UIActionClientRecord](t, UIActionClientModelName, orm.ModelSpec{Name: UIActionClientModelName, Definition: UIActionClientDefinition})
	var inserted orm.RecordMap
	restore := orm.StubRuntimeRepository(UIActionClientModelName, orm.RuntimeRepositoryStub{
		Create: func(_ context.Context, values orm.RecordMap) (orm.RecordMap, error) {
			inserted = values
			return orm.RecordMap{"id": int64(1)}, nil
		},
	})
	defer restore()

	_, err := createUIActionClient(context.Background(), meta, nil, []any{map[string]any{"tag": " mail.inbox ", "not_a_field": 1}}, nil)
	if err != nil {
		t.Fatalf("createUIActionClient() error = %v", err)
	}
	if got := inserted["tag"]; got != "mail.inbox" {
		t.Fatalf("tag = %#v, want trimmed mail.inbox", got)
	}
	if got := inserted["name"]; got != "mail.inbox" {
		t.Fatalf("name = %#v, want default from tag", got)
	}
	if _, ok := inserted["not_a_field"]; ok {
		t.Fatalf("unknown key reached the repository: %#v", inserted)
	}
}
