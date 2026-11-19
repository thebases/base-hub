package mcp

// File overview:
// - Responsibility: argument-validation coverage for the base.* generic
//   model/record MCP tools, moved verbatim (dispatchMCPTool(...) calls
//   become direct Dispatch(...) calls) out of
//   modules/base-mcp/routes/mcp_route_test.go, per the mcp-module-scaffold
//   plan's Phase 5.

import (
	"strings"
	"testing"

	"git.thebase.vn/base_hub/internal/orm"
	runtest "git.thebase.vn/base_hub/internal/test/runtime"
)

// mcpRBACDocument backs a couple of these tests' model-registration fixtures.
// Distinct Go type per test that registers its own model name in parallel
// (orm's registry enforces one Go type -> one model name), mirroring the
// original file's own fixture-isolation comment.
type mcpRBACDocument struct {
	orm.BaseModel
	Name string `bun:"name" json:"name"`
}

type mcpGuardPublishedDocument struct {
	orm.BaseModel
	Name string `bun:"name" json:"name"`
}

// TestMCPModelCreateCustomValidation covers base.model.create_custom's
// argument validation, which runs before any DB access (mcpkit.ExecuteRuntimeMethod
// is only reached once name/model are validated), so no Postgres fixture is
// needed here.
func TestMCPModelCreateCustomValidation(t *testing.T) {
	t.Parallel()

	if _, err := Dispatch(t.Context(), nil, "base.model.create_custom", map[string]any{}); err == nil {
		t.Fatal("expected error when arguments.name is missing")
	} else if !strings.Contains(err.Error(), "arguments.name is required") {
		t.Fatalf("unexpected error: %v", err)
	}

	// Register a model and confirm create_custom refuses to shadow it rather
	// than silently writing a conflicting sys.model row. Named already in
	// normalized form (tbl. prefix, <=24 chars) so mcpNormalizeCustomModelName
	// is a no-op and the "already exists" check is what actually fires.
	const existingModelName = "tbl.mcp_existing"
	runtest.RegisterModelType[mcpRBACDocument](t, orm.ModelSpec{
		Name:       existingModelName,
		Label:      "MCP Create-Custom Existing Model Test",
		Definition: orm.ModelDefinition(existingModelName, (*mcpRBACDocument)(nil)),
	})
	if _, err := Dispatch(t.Context(), nil, "base.model.create_custom", map[string]any{"name": existingModelName}); err == nil {
		t.Fatal("expected error when the model name already exists")
	} else if !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestMcpNormalizeCustomModelName covers space normalization, the automatic
// tbl. prefix, and the 24-char cap on the final normalized name.
func TestMcpNormalizeCustomModelName(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{"already prefixed, unchanged", "tbl.account", "tbl.account", false},
		{"spaces become dots", "invoice items", "tbl.invoice.items", false},
		{"multiple spaces become multiple dots", "a  b", "tbl.a..b", false},
		{"missing prefix gets added", "product", "tbl.product", false},
		{"other prefix still gets tbl. prepended", "sys.custom", "tbl.sys.custom", false},
		{"exactly at the cap is fine", "tbl.11111111111111111111", "tbl.11111111111111111111", false}, // 24 chars total
		{"over the cap after prefixing errors", "222222222222222222222", "", true},                    // 21 chars raw + "tbl." (4) = 25
	}
	for _, tc := range cases {
		got, err := mcpNormalizeCustomModelName(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("%s: expected an error, got name %q", tc.name, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: unexpected error: %v", tc.name, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s: mcpNormalizeCustomModelName(%q) = %q, want %q", tc.name, tc.in, got, tc.want)
		}
	}
}

// TestMCPModelAddFieldValidation covers base.model.add_field's argument
// validation, same DB-free scope as TestMCPModelCreateCustomValidation.
func TestMCPModelAddFieldValidation(t *testing.T) {
	t.Parallel()

	if _, err := Dispatch(t.Context(), nil, "base.model.add_field", map[string]any{"field_name": "street1"}); err == nil {
		t.Fatal("expected error when arguments.model is missing")
	} else if !strings.Contains(err.Error(), "arguments.model is required") {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := Dispatch(t.Context(), nil, "base.model.add_field", map[string]any{"model": "tbl.partner"}); err == nil {
		t.Fatal("expected error when arguments.field_name is missing")
	} else if !strings.Contains(err.Error(), "arguments.field_name is required") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestMCPModelAddFieldRelationValidation covers base.model.add_field's
// relation-metadata argument handling: a relation data_type (direct or via
// relation_type/many2one) requires a relation_model, accepted under any of
// its alias keys.
func TestMCPModelAddFieldRelationValidation(t *testing.T) {
	t.Parallel()

	if _, err := Dispatch(t.Context(), nil, "base.model.add_field", map[string]any{
		"model": "tbl.partner", "field_name": "owner_id", "data_type": "many2one",
	}); err == nil {
		t.Fatal("expected an error when a many2one field has no relation_model")
	} else if !strings.Contains(err.Error(), "requires a relation target model") {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := Dispatch(t.Context(), nil, "base.model.add_field", map[string]any{
		"model": "tbl.partner", "field_name": "owner_id", "many2one": true,
	}); err == nil {
		t.Fatal("expected an error when the many2one shorthand has no relation_model")
	} else if !strings.Contains(err.Error(), "requires a relation target model") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestMCPModelDeleteFieldValidation and TestMCPModelRenameFieldValidation
// cover the two field-mutation wrapper tools' argument validation, same
// DB-free scope as TestMCPModelAddFieldValidation.
func TestMCPModelDeleteFieldValidation(t *testing.T) {
	t.Parallel()

	if _, err := Dispatch(t.Context(), nil, "base.model.delete_field", map[string]any{"field_name": "street1"}); err == nil {
		t.Fatal("expected an error when arguments.model is missing")
	} else if !strings.Contains(err.Error(), "arguments.model is required") {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := Dispatch(t.Context(), nil, "base.model.delete_field", map[string]any{"model": "tbl.partner"}); err == nil {
		t.Fatal("expected an error when arguments.field_name is missing")
	} else if !strings.Contains(err.Error(), "arguments.field_name is required") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestMCPModelRenameFieldValidation(t *testing.T) {
	t.Parallel()

	if _, err := Dispatch(t.Context(), nil, "base.model.rename_field", map[string]any{"field_name": "street1", "new_field_name": "street_1"}); err == nil {
		t.Fatal("expected an error when arguments.model is missing")
	} else if !strings.Contains(err.Error(), "arguments.model is required") {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := Dispatch(t.Context(), nil, "base.model.rename_field", map[string]any{"model": "tbl.partner", "new_field_name": "street_1"}); err == nil {
		t.Fatal("expected an error when arguments.field_name is missing")
	} else if !strings.Contains(err.Error(), "arguments.field_name is required") {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := Dispatch(t.Context(), nil, "base.model.rename_field", map[string]any{"model": "tbl.partner", "field_name": "street1"}); err == nil {
		t.Fatal("expected an error when neither new_field_name nor new_label is given")
	} else if !strings.Contains(err.Error(), "new_field_name or arguments.new_label is required") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestMCPGuardModelNotPublishedRejectsPublishedModel covers
// mcpGuardModelNotPublished directly: add_field/delete_field/rename_field
// must all refuse to run while the target model is a live registered model,
// since editing the catalog afterward would silently drift from the already
// -synthesized live struct/table (see PublishModel's doc comment).
func TestMCPGuardModelNotPublishedRejectsPublishedModel(t *testing.T) {
	t.Parallel()

	const publishedModelName = "test.mcp_guard_published"
	runtest.RegisterModelType[mcpGuardPublishedDocument](t, orm.ModelSpec{
		Name:       publishedModelName,
		Label:      "MCP Guard Published Model Test",
		Definition: orm.ModelDefinition(publishedModelName, (*mcpGuardPublishedDocument)(nil)),
	})

	for _, tc := range []struct {
		tool string
		args map[string]any
	}{
		{"base.model.add_field", map[string]any{"model": publishedModelName, "field_name": "extra"}},
		{"base.model.delete_field", map[string]any{"model": publishedModelName, "field_name": "extra"}},
		{"base.model.rename_field", map[string]any{"model": publishedModelName, "field_name": "extra", "new_field_name": "extra2"}},
	} {
		if _, err := Dispatch(t.Context(), nil, tc.tool, tc.args); err == nil {
			t.Fatalf("%s: expected an error against an already-published model", tc.tool)
		} else if !strings.Contains(err.Error(), "call base.model.unpublish first") {
			t.Fatalf("%s: unexpected error: %v", tc.tool, err)
		}
	}
}

// TestMCPModelCreateUIValidation covers base.model.create_ui's argument
// validation, same DB-free scope as TestMCPModelCreateCustomValidation (the
// success path needs a live view/menu table so it's exercised only via
// base-mcp's HTTP-level RBAC-parity integration test, not here).
func TestMCPModelCreateUIValidation(t *testing.T) {
	t.Parallel()

	if _, err := Dispatch(t.Context(), nil, "base.model.create_ui", map[string]any{}); err == nil {
		t.Fatal("expected error when arguments.model is missing")
	} else if !strings.Contains(err.Error(), "arguments.model is required") {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := Dispatch(t.Context(), nil, "base.model.create_ui", map[string]any{"model": "test.mcp_create_ui_unknown"}); err == nil {
		t.Fatal("expected error when the model is not registered")
	} else if !strings.Contains(err.Error(), "is not registered") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestToolHandlersMatchToolsCatalog(t *testing.T) {
	names := map[string]bool{}
	for _, tool := range Tools() {
		names[tool.Name] = true
	}
	// base.batch.execute is handled by the transport, not this provider.
	delete(names, "base.batch.execute")
	for name := range toolHandlers {
		if !names[name] {
			t.Errorf("handler %q has no Tools() entry", name)
		}
		delete(names, name)
	}
	for name := range names {
		t.Errorf("Tools() entry %q has no handler", name)
	}
}
