package orm

// File overview:
// - Responsibility: verifies ORM runtime semantics, repository behavior, and registry coordination for runtime foundation.
// - Key symbols: focused Test cases plus file-local fixtures and assertion helpers.
// - Pattern: Locks down runtime invariants so refactors preserve transaction, RBAC, registry, and storage behavior.

// These foundation tests protect the small shared helpers that many runtime
// paths depend on, making regressions easier to isolate.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestEnvironmentWithContextKeepsCallerStateIsolated(t *testing.T) {
	env := NewEnvironment(nil, 7, map[string]any{"active_test": true}, false)
	next := env.WithContext(map[string]any{"lang": "vi_VN"})

	if env.UserID() != 7 {
		t.Fatalf("expected original user id to remain 7, got %d", env.UserID())
	}
	if _, ok := env.ContextValue("lang"); ok {
		t.Fatalf("expected original context to stay unchanged")
	}
	if value, ok := next.ContextValue("lang"); !ok || value != "vi_VN" {
		t.Fatalf("expected merged context value, got %#v", value)
	}
}

func TestRuntimeContextWithEnvironmentMergesContextIntoExistingEnvironment(t *testing.T) {
	baseEnv := NewEnvironment(nil, 7, map[string]any{
		ContextKeyAllowedCompanyIDs:    []int64{10, 20},
		ContextKeyAuthorizedCompanyIDs: []int64{10, 20},
		ContextKeyCurrentCompanyID:     int64(10),
	}, false)
	ctx := ContextWithEnvironment(context.Background(), baseEnv)

	nextCtx := runtimeContextWithEnvironment(ctx, nil, map[string]any{
		"context": map[string]any{
			ContextKeyAllowedCompanyIDs: []int64{20, 10},
			ContextKeyCurrentCompanyID:  int64(20),
		},
	})
	nextEnv := EnvironmentFromContext(nextCtx)
	if nextEnv == nil {
		t.Fatalf("expected derived environment")
	}

	allowed, err := nextEnv.AllowedCompanyIDs()
	if err != nil {
		t.Fatalf("AllowedCompanyIDs() error = %v", err)
	}
	if len(allowed) != 2 || allowed[0] != 20 || allowed[1] != 10 {
		t.Fatalf("allowed companies = %#v, want [20 10]", allowed)
	}
	companyID, err := nextEnv.CompanyID()
	if err != nil {
		t.Fatalf("CompanyID() error = %v", err)
	}
	if companyID != 20 {
		t.Fatalf("CompanyID() = %d, want 20", companyID)
	}

	originalAllowed, err := baseEnv.AllowedCompanyIDs()
	if err != nil {
		t.Fatalf("base AllowedCompanyIDs() error = %v", err)
	}
	if len(originalAllowed) != 2 || originalAllowed[0] != 10 || originalAllowed[1] != 20 {
		t.Fatalf("base allowed companies mutated = %#v", originalAllowed)
	}
}

func TestEnvironmentCompanyIDPrefersExplicitCurrentCompany(t *testing.T) {
	env := NewEnvironment(nil, 7, map[string]any{
		ContextKeyAllowedCompanyIDs:    []int64{10, 20},
		ContextKeyAuthorizedCompanyIDs: []int64{10, 20},
		ContextKeyCurrentCompanyID:     int64(20),
	}, false)

	companyID, err := env.CompanyID()
	if err != nil {
		t.Fatalf("CompanyID() error = %v", err)
	}
	if companyID != 20 {
		t.Fatalf("CompanyID() = %d, want 20", companyID)
	}
}

// An explicit positive company is kept (validateImplicitCompanyAccess checks it); only an absent
// company_id is pinned to the active company.
func TestApplyImplicitCreateCompanyValueKeepsExplicitPayloadCompany(t *testing.T) {
	model := &ModelMetadata{
		Name: "test.company.scoped",
		Fields: []FieldMetadata{
			{Name: "company_id", GoName: "CompanyID", Kind: FieldKindManyToOne},
		},
	}
	env := NewEnvironment(nil, 7, map[string]any{
		ContextKeyAllowedCompanyIDs:    []int64{10, 20},
		ContextKeyAuthorizedCompanyIDs: []int64{10, 20},
		ContextKeyCurrentCompanyID:     int64(20),
	}, false)
	ctx := ContextWithEnvironment(context.Background(), env)
	values := RecordMap{"company_id": int64(10)}

	if err := applyImplicitCreateCompanyValue(ctx, model, values); err != nil {
		t.Fatalf("applyImplicitCreateCompanyValue() error = %v", err)
	}
	if got := toInt64(values["company_id"]); got != 10 {
		t.Fatalf("company_id = %d, want explicit 10 kept", got)
	}
	absent := RecordMap{}
	if err := applyImplicitCreateCompanyValue(ctx, model, absent); err != nil {
		t.Fatalf("applyImplicitCreateCompanyValue() error = %v", err)
	}
	if got := toInt64(absent["company_id"]); got != 20 {
		t.Fatalf("absent company_id pinned to %d, want active 20", got)
	}
}

func TestApplyImplicitCreateCompanyValuePreservesExplicitSharedCreateForMainCompany(t *testing.T) {
	model := &ModelMetadata{
		Name: "test.company.scoped",
		Fields: []FieldMetadata{
			{Name: "company_id", GoName: "CompanyID", Kind: FieldKindManyToOne},
		},
	}
	env := NewEnvironment(nil, 7, map[string]any{
		ContextKeyAllowedCompanyIDs:    []int64{1, 20},
		ContextKeyAuthorizedCompanyIDs: []int64{1, 20},
		ContextKeyCurrentCompanyID:     int64(1),
		RBACContextAuthModeKey:         "basic",
	}, false)
	ctx := ContextWithEnvironment(context.Background(), env)
	values := RecordMap{"company_id": nil}

	if err := applyImplicitCreateCompanyValue(ctx, model, values); err != nil {
		t.Fatalf("applyImplicitCreateCompanyValue() error = %v", err)
	}
	if got := toInt64(values["company_id"]); got != 0 {
		t.Fatalf("company_id = %d, want shared nil/0", got)
	}
	if err := validateImplicitCompanyAccess(ctx, model, values, "create"); err != nil {
		t.Fatalf("validateImplicitCompanyAccess(create shared) error = %v", err)
	}
}

func TestValidateImplicitCompanyAccessBlocksSharedWriteOutsideMainCompany(t *testing.T) {
	model := &ModelMetadata{
		Name: "test.company.scoped",
		Fields: []FieldMetadata{
			{Name: "company_id", GoName: "CompanyID", Kind: FieldKindManyToOne},
		},
	}
	env := NewEnvironment(nil, 7, map[string]any{
		ContextKeyAllowedCompanyIDs:    []int64{10, 20},
		ContextKeyAuthorizedCompanyIDs: []int64{10, 20},
		ContextKeyCurrentCompanyID:     int64(20),
		RBACContextAuthModeKey:         "basic",
	}, false)
	ctx := ContextWithEnvironment(context.Background(), env)

	if err := validateImplicitCompanyAccess(ctx, model, RecordMap{"company_id": nil}, "write"); err == nil {
		t.Fatalf("expected shared write to be blocked outside company.main")
	}
}

func TestImplicitCompanyRuleDomainBlocksSharedWritesOutsideMainCompany(t *testing.T) {
	model := &ModelMetadata{
		Name: "test.company.scoped",
		Fields: []FieldMetadata{
			{Name: "company_id", GoName: "CompanyID", Kind: FieldKindManyToOne},
		},
	}
	env := NewEnvironment(nil, 7, map[string]any{
		ContextKeyAllowedCompanyIDs:    []int64{10, 20},
		ContextKeyAuthorizedCompanyIDs: []int64{10, 20},
		ContextKeyCurrentCompanyID:     int64(20),
		RBACContextAuthModeKey:         "basic",
	}, false)

	domain, err := implicitCompanyRuleDomain(env, model, "write")
	if err != nil {
		t.Fatalf("implicitCompanyRuleDomain(write) error = %v", err)
	}
	sql, args, err := domain.CompileWithBaseAlias(model, quoteIdent("test_company_scoped"))
	if err != nil {
		t.Fatalf("compile domain: %v", err)
	}
	if strings.Contains(sql, "IS NULL") {
		t.Fatalf("expected shared rows to be excluded from non-main write domain, got %s", sql)
	}
	if len(args) != 2 || args[0] != int64(10) || args[1] != int64(20) {
		t.Fatalf("unexpected write-domain args %#v", args)
	}
}

func TestParseDomainSupportsPrefixLogic(t *testing.T) {
	domain, err := ParseDomain([]any{
		"|",
		[]any{"name", "ilike", "bean"},
		[]any{"id", "in", []any{1, 2}},
	})
	if err != nil {
		t.Fatalf("parse domain: %v", err)
	}
	if domain.Kind != DomainKindOr {
		t.Fatalf("expected OR domain, got %s", domain.Kind)
	}
	if len(domain.Children) != 2 {
		t.Fatalf("expected two children, got %d", len(domain.Children))
	}
}

func TestDomainCompileWithBaseAliasUsesQueryAlias(t *testing.T) {
	meta := &ModelMetadata{
		Name:        "ui.view",
		StorageName: "ui_view",
		Fields: []FieldMetadata{
			{Name: "model", BunColumn: "model"},
			{Name: "type", BunColumn: "type"},
		},
	}
	domain := And(
		Where("model", "=", "ui.actions.window"),
		Where("type", "=", "list"),
	)

	sql, args, err := domain.CompileWithBaseAlias(meta, quoteIdent("ui_view_record"))
	if err != nil {
		t.Fatalf("compile domain: %v", err)
	}
	if sql != `("ui_view_record"."model" = ?) AND ("ui_view_record"."type" = ?)` {
		t.Fatalf("expected stable predicate skeleton, got %s", sql)
	}
	if len(args) != 2 {
		t.Fatalf("expected two bind arguments, got %#v", args)
	}
	if strings.Contains(sql, `"ui_view"."model"`) || strings.Contains(sql, `"ui_view"."type"`) {
		t.Fatalf("expected compiled identifiers to avoid storage table name once alias is active, got %s", sql)
	}
	if args[0] != "ui.actions.window" || args[1] != "list" {
		t.Fatalf("unexpected bind arguments %#v", args)
	}
}

func TestTranslateBunPlaceholdersToPGX(t *testing.T) {
	sql, err := translateBunPlaceholdersToPGX(`("ui_view_record"."model" = ?) AND LOWER("ui_view_record"."type") LIKE LOWER(?)`, 2)
	if err != nil {
		t.Fatalf("translate placeholders: %v", err)
	}
	if sql != `("ui_view_record"."model" = $3) AND LOWER("ui_view_record"."type") LIKE LOWER($4)` {
		t.Fatalf("unexpected translated SQL %q", sql)
	}
}

type jsonbScalarUpdateDocument struct {
	BaseModel
	Value any            `bun:"value,type:jsonb,nullzero" json:"value"`
	Flags map[string]any `bun:"flags,type:jsonb,nullzero" json:"flags"`
	Name  string         `bun:"name" json:"name"`
}

func TestIsJSONBUpdateColumnDetectsScalarAnyJSONBField(t *testing.T) {
	if !isJSONBUpdateColumn(goTypeOf[jsonbScalarUpdateDocument](), "value") {
		t.Fatalf("expected value column to be treated as jsonb")
	}
	if !isJSONBUpdateColumn(goTypeOf[jsonbScalarUpdateDocument](), "flags") {
		t.Fatalf("expected flags column to be treated as jsonb")
	}
	if isJSONBUpdateColumn(goTypeOf[jsonbScalarUpdateDocument](), "name") {
		t.Fatalf("expected plain text column to stay non-jsonb")
	}
}

func TestMarshalJSONBUpdateValuePreservesScalarBooleans(t *testing.T) {
	raw, err := marshalJSONBUpdateValue(true)
	if err != nil {
		t.Fatalf("marshalJSONBUpdateValue(true) error = %v", err)
	}
	if string(raw) != "true" {
		t.Fatalf("expected boolean jsonb payload true, got %q", string(raw))
	}

	raw, err = marshalJSONBUpdateValue(map[string]any{"enabled": true})
	if err != nil {
		t.Fatalf("marshalJSONBUpdateValue(map) error = %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("json.Unmarshal(map payload) error = %v", err)
	}
	if decoded["enabled"] != true {
		t.Fatalf("expected object payload to round-trip, got %#v", decoded)
	}

	raw, err = marshalJSONBUpdateValue(nil)
	if err != nil {
		t.Fatalf("marshalJSONBUpdateValue(nil) error = %v", err)
	}
	if raw != nil {
		t.Fatalf("expected nil payload to stay nil, got %q", string(raw))
	}
}

func TestDomainCompileInExpandsPlainValuePlaceholders(t *testing.T) {
	meta := &ModelMetadata{
		Name:        "tbl.country.state",
		StorageName: "tbl_country_state",
		Fields: []FieldMetadata{
			{Name: "country_id", BunColumn: "country_id"},
		},
	}
	domain := Where("country_id", "in", []any{241, 242, 243})

	sql, args, err := domain.CompileWithBaseAlias(meta, quoteIdent("tbl_country_state_record"))
	if err != nil {
		t.Fatalf("compile in domain: %v", err)
	}
	if sql != `"tbl_country_state_record"."country_id" IN (?, ?, ?)` {
		t.Fatalf("unexpected IN sql %q", sql)
	}
	if len(args) != 3 || args[0] != 241 || args[1] != 242 || args[2] != 243 {
		t.Fatalf("unexpected IN args %#v", args)
	}
}

func TestDomainCompileLikeAndILike(t *testing.T) {
	meta := &ModelMetadata{
		Name:        "tbl.country.state",
		StorageName: "tbl_country_state",
		Fields: []FieldMetadata{
			{Name: "name", BunColumn: "name"},
		},
	}

	likeSQL, likeArgs, err := Where("name", "like", "Ha").CompileWithBaseAlias(meta, quoteIdent("tbl_country_state_record"))
	if err != nil {
		t.Fatalf("compile like domain: %v", err)
	}
	if likeSQL != `CAST("tbl_country_state_record"."name" AS text) LIKE ?` {
		t.Fatalf("unexpected LIKE sql %q", likeSQL)
	}
	if len(likeArgs) != 1 || likeArgs[0] != "%Ha%" {
		t.Fatalf("unexpected LIKE args %#v", likeArgs)
	}

	ilikeSQL, ilikeArgs, err := Where("name", "ilike", "ha").CompileWithBaseAlias(meta, quoteIdent("tbl_country_state_record"))
	if err != nil {
		t.Fatalf("compile ilike domain: %v", err)
	}
	if ilikeSQL != `LOWER(CAST("tbl_country_state_record"."name" AS text)) LIKE LOWER(?)` {
		t.Fatalf("unexpected ILIKE sql %q", ilikeSQL)
	}
	if len(ilikeArgs) != 1 || ilikeArgs[0] != "%ha%" {
		t.Fatalf("unexpected ILIKE args %#v", ilikeArgs)
	}
}

func TestCompareRuntimeValueLikeAndILike(t *testing.T) {
	if !compareRuntimeValue("Ha Noi", "like", "Ha") {
		t.Fatalf("expected case-sensitive like match")
	}
	if compareRuntimeValue("Ha Noi", "like", "ha") {
		t.Fatalf("expected like to stay case-sensitive")
	}
	if !compareRuntimeValue("Ha Noi", "ilike", "ha") {
		t.Fatalf("expected ilike to ignore case")
	}
}

func TestNormalizeDomainForModelCoercesRelationIntegerAndBooleanValues(t *testing.T) {
	meta := &ModelMetadata{
		Name:        "test.domain.normalize",
		StorageName: "test_domain_normalize",
		Fields: []FieldMetadata{
			{Name: "partner_id", BunColumn: "partner_id", Kind: FieldKindManyToOne},
			{Name: "child_ids", BunColumn: "child_ids", Kind: FieldKindOneToMany},
			{Name: "sequence", BunColumn: "sequence", Kind: FieldKindInteger},
			{Name: "active", BunColumn: "active", Kind: FieldKindBoolean},
		},
	}
	domain := And(
		Where("partner_id", "=", false),
		Where("child_ids", "=", false),
		Where("sequence", "=", "7"),
		Where("active", "=", "true"),
	)

	normalized, err := normalizeDomainForModel(meta, domain)
	if err != nil {
		t.Fatalf("normalize domain: %v", err)
	}
	if got := normalized.Children[0].Value; got != nil {
		t.Fatalf("expected many2one false to normalize to nil, got %#v", got)
	}
	if got := normalized.Children[1].Value; got != nil {
		t.Fatalf("expected one2many false to normalize to nil, got %#v", got)
	}
	if got := normalized.Children[2].Value; got != int64(7) {
		t.Fatalf("expected integer string to normalize to int64, got %#v", got)
	}
	if got := normalized.Children[3].Value; got != true {
		t.Fatalf("expected boolean string to normalize to bool, got %#v", got)
	}
}

type runtimeDomainNormalizeParent struct {
	BaseModel
	Name string `bun:"name" json:"name"`
}

type runtimeDomainNormalizeContainer struct {
	BaseModel
	ChildIDs []int64 `bun:"child_ids,type:jsonb,nullzero" json:"child_ids" meta:"kind:one2many,model:test.domain.normalize.child,inverse:parent_id,stored:false"`
}

type runtimeDomainNormalizeChild struct {
	BaseModel
	ParentID *int64 `bun:"parent_id" json:"parent_id" meta:"kind:many2one,model:test.domain.normalize.parent"`
	Active   bool   `bun:"active" json:"active"`
	Sequence int64  `bun:"sequence" json:"sequence"`
}

func TestNormalizeDomainForModelNormalizesNestedAnyDomains(t *testing.T) {
	const parentModelName = "test.domain.normalize.parent"
	const childModelName = "test.domain.normalize.child"

	if _, err := RegisterModelType[runtimeDomainNormalizeParent](ModelSpec{
		Name:       parentModelName,
		Label:      "Test Domain Normalize Parent",
		Definition: ModelDefinition(parentModelName, (*runtimeDomainNormalizeParent)(nil)),
	}.WithStorageName("test_domain_normalize_parent")); err != nil {
		t.Fatalf("register parent model: %v", err)
	}
	parentMeta, err := RegisterModelType[runtimeDomainNormalizeContainer](ModelSpec{
		Name:       "test.domain.normalize.container",
		Label:      "Test Domain Normalize Container",
		Definition: ModelDefinition("test.domain.normalize.container", (*runtimeDomainNormalizeContainer)(nil)),
	}.WithStorageName("test_domain_normalize_container"))
	if err != nil {
		t.Fatalf("register container model: %v", err)
	}
	if _, err := RegisterModelType[runtimeDomainNormalizeChild](ModelSpec{
		Name:       childModelName,
		Label:      "Test Domain Normalize Child",
		Definition: ModelDefinition(childModelName, (*runtimeDomainNormalizeChild)(nil)),
	}.WithStorageName("test_domain_normalize_child")); err != nil {
		t.Fatalf("register child model: %v", err)
	}

	normalized, err := normalizeDomainForModel(parentMeta, Where("child_ids", "any", And(
		Where("parent_id", "=", false),
		Where("active", "=", "1"),
		Where("sequence", "=", "9"),
	)))
	if err != nil {
		t.Fatalf("normalize nested any domain: %v", err)
	}

	childDomain, ok := normalized.Value.(DomainNode)
	if !ok {
		t.Fatalf("expected nested child domain, got %T", normalized.Value)
	}
	if got := childDomain.Children[0].Value; got != nil {
		t.Fatalf("expected nested many2one false to normalize to nil, got %#v", got)
	}
	if got := childDomain.Children[1].Value; got != true {
		t.Fatalf("expected nested boolean string to normalize to true, got %#v", got)
	}
	if got := childDomain.Children[2].Value; got != int64(9) {
		t.Fatalf("expected nested integer string to normalize to int64, got %#v", got)
	}
}

func TestRuntimeOrderSQLUsesQuotedColumns(t *testing.T) {
	type sampleRecord struct {
		ID   int64  `bun:"id"`
		Name string `bun:"name"`
	}
	meta := &ModelMetadata{
		Name: "test.sample",
		Fields: []FieldMetadata{
			{Name: "id", BunColumn: "id"},
			{Name: "name", BunColumn: "name"},
		},
	}
	sql, err := runtimeOrderSQL(meta, goTypeOf[sampleRecord](), `"sample_record"`, "name desc, id asc", "en")
	if err != nil {
		t.Fatalf("runtime order sql: %v", err)
	}
	if sql != `"sample_record"."name" DESC, "sample_record"."id" ASC` {
		t.Fatalf("unexpected order SQL %q", sql)
	}
}

func TestRecordSetEnsureOne(t *testing.T) {
	meta := &ModelMetadata{Name: "test.model"}
	if err := NewRecordSet(meta, nil, []string{"1", "2"}).EnsureOne(); err == nil {
		t.Fatalf("expected EnsureOne to fail for multi recordset")
	}
	if err := NewRecordSet(meta, nil, []string{"1"}).EnsureOne(); err != nil {
		t.Fatalf("expected EnsureOne to accept singleton: %v", err)
	}
}

func TestModelMetadataAppliesRuntimeDefaults(t *testing.T) {
	meta := &ModelMetadata{
		Name:   "test.model",
		Fields: []FieldMetadata{{Name: "id", BunColumn: "id"}, {Name: "name", BunColumn: "name"}},
	}
	meta.applyRuntimeDefaults()

	if meta.RecName != "name" {
		t.Fatalf("expected rec name to default to name, got %q", meta.RecName)
	}
	if meta.DefaultOrder != "id asc" {
		t.Fatalf("expected default order to use id asc, got %q", meta.DefaultOrder)
	}
}

func TestRuntimeOnchangeReturnsHookValues(t *testing.T) {
	modelName := "test.onchange"
	RegisterOnchange(modelName, []string{"name"}, func(_ context.Context, _ RecordSet, _ RecordMap, _ []string, _ map[string]any) (RecordMap, error) {
		return RecordMap{"display_name": "computed"}, nil
	})

	meta := &ModelMetadata{Name: modelName}
	result, err := runtimeOnchange(context.Background(), meta, nil, []any{RecordMap{"name": "x"}, []any{"name"}}, nil)
	if err != nil {
		t.Fatalf("runtime onchange: %v", err)
	}

	payload, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("expected onchange payload map, got %T", result)
	}
	values, _ := payload["value"].(RecordMap)
	if values["display_name"] != "computed" {
		t.Fatalf("expected onchange result to include computed display_name, got %#v", values)
	}
}

func TestNormalizeX2ManyCommandsSupportsCreateAndSetShorthand(t *testing.T) {
	created := normalizeX2ManyCommands(RecordMap{"name": "child"})
	if len(created) != 1 || created[0].op != 0 || created[0].values["name"] != "child" {
		t.Fatalf("unexpected create shorthand normalization %#v", created)
	}

	set := normalizeX2ManyCommands([]string{"10", "20"})
	if len(set) != 1 || set[0].op != 6 || len(set[0].ids) != 2 {
		t.Fatalf("unexpected set shorthand normalization %#v", set)
	}
}

func TestNormalizeX2ManyCommandsForFieldSupportsManyToManyDisplayTuples(t *testing.T) {
	field := FieldMetadata{Name: "group_ids", Kind: FieldKindManyToMany}

	commands := normalizeX2ManyCommandsForField(field, []any{
		[]any{6, "System"},
		[]any{9, "Admin"},
	})
	if len(commands) != 1 || commands[0].op != 6 {
		t.Fatalf("expected many2many shorthand to normalize into one set command, got %#v", commands)
	}
	if len(commands[0].ids) != 2 || commands[0].ids[0] != "6" || commands[0].ids[1] != "9" {
		t.Fatalf("expected many2many shorthand ids to be preserved, got %#v", commands[0].ids)
	}
}

func TestNormalizeX2ManyCommandsForFieldSkipsPlainOneToManySnapshots(t *testing.T) {
	field := FieldMetadata{Name: "child_ids", Kind: FieldKindOneToMany}

	commands := normalizeX2ManyCommandsForField(field, []any{
		[]any{6, "Child A"},
		[]any{7, "Child B"},
	})
	if len(commands) != 0 {
		t.Fatalf("expected one2many snapshots to remain ignored, got %#v", commands)
	}
}

func TestNormalizeRuntimeFieldValueHandlesManyToOneTuplesAndEmptyStrings(t *testing.T) {
	field := FieldMetadata{Name: "group_id", Kind: FieldKindManyToOne}

	if got := normalizeRuntimeFieldValue(field, []any{6, "System"}); got != int64(6) {
		t.Fatalf("expected many2one tuple to normalize to int64 id, got %#v", got)
	}
	if got := normalizeRuntimeFieldValue(field, ""); got != nil {
		t.Fatalf("expected empty many2one string to normalize to nil, got %#v", got)
	}
	if got := normalizeRuntimeFieldValue(field, false); got != nil {
		t.Fatalf("expected false many2one value to normalize to nil, got %#v", got)
	}
}

func TestNormalizeRuntimeFieldValueAcceptsDateOnlyStrings(t *testing.T) {
	field := FieldMetadata{Name: "end_date", Kind: FieldKindDate}

	want := time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)
	if got := normalizeRuntimeFieldValue(field, " 2026-12-31 "); got != want {
		t.Fatalf("expected date-only string to become UTC midnight, got %#v", got)
	}
	if got := normalizeRuntimeFieldValue(field, "2026-12-31T10:00:00Z"); got != "2026-12-31T10:00:00Z" {
		t.Fatalf("expected RFC3339 string to pass through, got %#v", got)
	}
	if got := normalizeRuntimeFieldValue(field, "  "); got != nil {
		t.Fatalf("expected blank date to normalize to nil, got %#v", got)
	}
}

func TestNormalizeRuntimeFieldValueCoercesIntegerInputs(t *testing.T) {
	field := FieldMetadata{Name: "sequence", Kind: FieldKindInteger}

	if got := normalizeRuntimeFieldValue(field, "7"); got != int64(7) {
		t.Fatalf("expected integer string to normalize to int64, got %#v", got)
	}
	if got := normalizeRuntimeFieldValue(field, float64(9)); got != int64(9) {
		t.Fatalf("expected whole float to normalize to int64, got %#v", got)
	}
	if got := normalizeRuntimeFieldValue(field, float64(9.5)); got != float64(9.5) {
		t.Fatalf("expected decimal float to remain unchanged, got %#v", got)
	}
}

func TestNormalizeRuntimeFieldValueCoercesBooleanInputs(t *testing.T) {
	field := FieldMetadata{Name: "active", Kind: FieldKindBoolean}

	if got := normalizeRuntimeFieldValue(field, "true"); got != true {
		t.Fatalf("expected boolean string true to normalize to bool, got %#v", got)
	}
	if got := normalizeRuntimeFieldValue(field, "0"); got != false {
		t.Fatalf("expected boolean string zero to normalize to false, got %#v", got)
	}
	if got := normalizeRuntimeFieldValue(field, 1); got != true {
		t.Fatalf("expected integer one to normalize to true, got %#v", got)
	}
	if got := normalizeRuntimeFieldValue(field, "maybe"); got != "maybe" {
		t.Fatalf("expected non-boolean string to remain unchanged, got %#v", got)
	}
}

func TestDomainCompileJSONPathExtractsText(t *testing.T) {
	meta := &ModelMetadata{
		Name:        "edc.device.status",
		StorageName: "edc_device_status",
		Fields: []FieldMetadata{
			{Name: "metadata", BunColumn: "metadata", Kind: FieldKindJSON},
		},
	}
	domain, err := normalizeDomainForModel(meta, Where("metadata.batteryDetail.health", "in", []any{"good", "GOOD"}))
	if err != nil {
		t.Fatalf("normalize json path domain: %v", err)
	}
	sql, args, err := domain.CompileWithBaseAlias(meta, quoteIdent("s"))
	if err != nil {
		t.Fatalf("compile json path domain: %v", err)
	}
	if sql != `("s"."metadata" #>> '{batteryDetail,health}') IN (?, ?)` {
		t.Fatalf("unexpected json path sql %q", sql)
	}
	if len(args) != 2 || args[0] != "good" || args[1] != "GOOD" {
		t.Fatalf("unexpected json path args %#v", args)
	}
	if _, _, err := Where("metadata.a'b", "=", "x").CompileWithBaseAlias(meta, quoteIdent("s")); err == nil {
		t.Fatalf("expected quoted json key to be rejected")
	}
}
