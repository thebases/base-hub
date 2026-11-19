package models

// File overview:
// - Responsibility: verifies kernel model metadata, storage helpers, and registry-backed behavior for ui view.
// - Key symbols: focused Test cases plus file-local fixtures and assertion helpers.
// - Pattern: Locks down metadata and persistence contracts so schema and runtime helpers stay compatible with the rest of the kernel.

import (
	"context"
	"strings"
	"testing"

	"git.thebase.vn/base_hub/internal/orm"
)

type uiViewTargetTestRecord struct {
	orm.BaseModel
	Name   string `bun:"name" json:"name"`
	Email  string `bun:"email" json:"email"`
	Active bool   `bun:"active" json:"active"`
}

func TestNormalizeUIViewValuesInfersTypeAndArchFields(t *testing.T) {
	values, err := normalizeUIViewValues(orm.RecordMap{
		"model": "test.ui_view_target",
		"arch":  `<?xml version="1.0" encoding="UTF-8"?><form string="Partner"><field name="name"/></form>`,
	}, "", true)
	if err != nil {
		t.Fatalf("normalizeUIViewValues() error = %v", err)
	}
	if got := values["type"]; got != "form" {
		t.Fatalf("type = %#v, want form", got)
	}
	if got := values["arch_db"]; got != `<form string="Partner"><field name="name"/></form>` {
		t.Fatalf("arch_db = %#v", got)
	}
	if got := values["arch_base"]; got != `<form string="Partner"><field name="name"/></form>` {
		t.Fatalf("arch_base = %#v", got)
	}
	if _, ok := values["arch"]; ok {
		t.Fatalf("expected helper arch key to be removed, got %#v", values["arch"])
	}
}

func TestNormalizeUIViewValuesInfersDashboardTypeFromArch(t *testing.T) {
	values, err := normalizeUIViewValues(orm.RecordMap{
		"module": "base",
		"arch":   `<?xml version="1.0" encoding="UTF-8"?><dashboard><cards/><quicklinks/></dashboard>`,
	}, "", true)
	if err != nil {
		t.Fatalf("normalizeUIViewValues() error = %v", err)
	}
	if got := values["type"]; got != "dashboard" {
		t.Fatalf("type = %#v, want dashboard", got)
	}
	if got := values["arch_db"]; got != `<dashboard><cards/><quicklinks/></dashboard>` {
		t.Fatalf("arch_db = %#v", got)
	}
}

func TestMergeUIViewArchAppliesXPathOperations(t *testing.T) {
	baseArch := `<form string="Partner"><sheet><group><field name="name"/></group></sheet></form>`
	extensionArch := `<data><xpath expr="//field[@name='name']" position="after"><field name="email"/></xpath><xpath expr="//form" position="attributes"><attribute name="string">Partner Card</attribute></xpath></data>`

	got, err := mergeUIViewArch(baseArch, extensionArch)
	if err != nil {
		t.Fatalf("mergeUIViewArch() error = %v", err)
	}
	want := `<form string="Partner Card"><sheet><group><field name="name"/><field name="email"/></group></sheet></form>`
	if got != want {
		t.Fatalf("mergeUIViewArch() = %q, want %q", got, want)
	}
}

func TestGetViewUIViewBuildsGeneratedPattern(t *testing.T) {
	const modelName = "test.ui_view_target"
	if _, err := orm.RegisterModelType[uiViewTargetTestRecord](orm.ModelSpec{
		Name:       modelName,
		Label:      "Test View Target",
		Definition: orm.ModelDefinition(modelName, (*uiViewTargetTestRecord)(nil)),
	}.WithStorageName("test_ui_view_target")); err != nil {
		t.Fatalf("register model: %v", err)
	}

	result, err := getViewUIView(context.Background(), nil, nil, []any{modelName, "form"}, nil)
	if err != nil {
		t.Fatalf("getViewUIView() error = %v", err)
	}
	payload, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("expected payload map, got %T", result)
	}
	if got := payload["model"]; got != modelName {
		t.Fatalf("model = %#v, want %q", got, modelName)
	}
	if got := payload["type"]; got != "form" {
		t.Fatalf("type = %#v, want form", got)
	}
	meta, ok := payload["meta"].(map[string]any)
	if !ok {
		t.Fatalf("expected meta map, got %#v", payload["meta"])
	}
	if got := meta["source"]; got != "generated" {
		t.Fatalf("source = %#v, want generated", got)
	}
	pattern, ok := payload["pattern"].(map[string]any)
	if !ok {
		t.Fatalf("expected pattern map, got %#v", payload["pattern"])
	}
	if got := pattern["tag"]; got != "form" {
		t.Fatalf("pattern tag = %#v, want form", got)
	}
}

func TestUIViewMetadataRegistersStatusbarVisibleCompatibilityField(t *testing.T) {
	field, err := UIViewDefinition.Field("statusbar_visible")
	if err != nil {
		t.Fatalf("expected statusbar_visible compatibility field on definition: %v", err)
	}
	if field.Stored {
		t.Fatalf("expected statusbar_visible to stay non-stored, got stored=%v", field.Stored)
	}
	if field.Compute != "_compute_statusbar_visible" {
		t.Fatalf("expected compute hook to back statusbar_visible, got %q", field.Compute)
	}

	meta, err := orm.RegisterModelType[UIViewRecord](UIViewModelSpec)
	if err != nil {
		t.Fatalf("register ui.view metadata: %v", err)
	}
	registeredField, err := meta.Field("statusbar_visible")
	if err != nil {
		t.Fatalf("expected registered ui.view metadata to expose statusbar_visible: %v", err)
	}
	if registeredField.Compute != "_compute_statusbar_visible" {
		t.Fatalf("expected registered compute hook to survive metadata projection, got %q", registeredField.Compute)
	}
}

func TestResolveUIViewIDSupportsLegacyArgsShape(t *testing.T) {
	viewID, err := resolveUIViewID(nil, []any{[]any{117}}, nil)
	if err != nil {
		t.Fatalf("resolveUIViewID() error = %v", err)
	}
	if viewID != 117 {
		t.Fatalf("view id = %d, want 117", viewID)
	}
}

func TestComputeUIViewStatusbarVisibleDefaultsToEmptyString(t *testing.T) {
	values, err := computeUIViewStatusbarVisible(context.Background(), orm.NewRecordSet(&orm.ModelMetadata{Name: UIViewModelName}, nil, []string{"117"}), orm.FieldMetadata{Name: "statusbar_visible"})
	if err != nil {
		t.Fatalf("computeUIViewStatusbarVisible() error = %v", err)
	}
	if got := values["117"]; got != "" {
		t.Fatalf("statusbar_visible = %#v, want empty string", got)
	}
}

func TestParseUIViewCompareRequestDefaultsToSoft(t *testing.T) {
	request, err := parseUIViewCompareRequest([]string{"42"}, nil, nil)
	if err != nil {
		t.Fatalf("parseUIViewCompareRequest() error = %v", err)
	}
	if request.ViewID != 42 {
		t.Fatalf("view id = %d, want 42", request.ViewID)
	}
	if request.Mode != "soft" {
		t.Fatalf("mode = %q, want soft", request.Mode)
	}
}

func TestBuildUIViewArchComparisonSoftMode(t *testing.T) {
	payload, err := buildUIViewArchComparison(context.Background(), sysUIViewRow{
		ID:       17,
		Name:     "Partner Form",
		Key:      "base.view_partner_form",
		ArchDB:   `<form string="Partner"><field name="email"/></form>`,
		ArchPrev: `<form string="Partner"><field name="name"/></form>`,
	}, uiViewCompareRequest{ViewID: 17, Mode: "soft"})
	if err != nil {
		t.Fatalf("buildUIViewArchComparison() error = %v", err)
	}
	if got := payload["compare_name"]; got != "Previous Arch" {
		t.Fatalf("compare_name = %#v, want Previous Arch", got)
	}
	if got := payload["arch_to_compare"]; got != `<form string="Partner"><field name="name"/></form>` {
		t.Fatalf("arch_to_compare = %#v", got)
	}
	if got := payload["has_diff"]; got != true {
		t.Fatalf("has_diff = %#v, want true", got)
	}
	diff, ok := payload["arch_diff"].(string)
	if !ok {
		t.Fatalf("arch_diff type = %T, want string", payload["arch_diff"])
	}
	if !strings.Contains(diff, "&lt;pre&gt;") && !strings.Contains(diff, "<pre>") {
		t.Fatalf("arch_diff = %q, want preformatted HTML", diff)
	}
	if !strings.Contains(diff, "field name=&#34;email&#34;") || !strings.Contains(diff, "field name=&#34;name&#34;") {
		t.Fatalf("arch_diff = %q, want both changed field names", diff)
	}
}

func TestBuildUIViewArchComparisonOtherViewRequiresCompareViewID(t *testing.T) {
	_, err := buildUIViewArchComparison(context.Background(), sysUIViewRow{
		ID:     17,
		Name:   "Partner Form",
		ArchDB: `<form string="Partner"><field name="email"/></form>`,
	}, uiViewCompareRequest{ViewID: 17, Mode: "other_view"})
	if err == nil || !strings.Contains(err.Error(), "compare_view_id") {
		t.Fatalf("expected compare_view_id error, got %v", err)
	}
}

func TestBuildUIViewArchDiffHTMLEmptyWhenNoDiff(t *testing.T) {
	diff := buildUIViewArchDiffHTML(`<form><field name="name"/></form>`, `<form><field name="name"/></form>`)
	if diff != "" {
		t.Fatalf("buildUIViewArchDiffHTML() = %q, want empty string", diff)
	}
}
