package orm

// File overview:
// - Responsibility: verifies ORM runtime semantics, repository behavior, and registry coordination for model registry.
// - Key symbols: focused Test cases plus file-local fixtures and assertion helpers.
// - Pattern: Locks down runtime invariants so refactors preserve transaction, RBAC, registry, and storage behavior.

// These tests protect the core registry invariants: uniqueness, stable lookup,
// and deterministic defaults for registered model metadata.

import (
	"context"
	"strings"
	"testing"
)

type optTaggedFilterDocument struct {
	BaseModel
	BankBIC  string `bun:"bank_bic" json:"bank_bic" meta:"filter:bic,label:Bank BIC,help:Used for external lookup."`
	ProxyRef string `bun:"proxy_ref" json:"proxy_ref" meta:"filter"`
	Hidden   string `bun:"hidden_value" json:"hidden_value" meta:"filter:false"`
}

type registryBankDocument struct {
	BaseModel
	ProxyValue string `bun:"proxy_value" json:"proxy_value"`
}

type registryWebhookSchemaDocument struct {
	BaseModel
	InputPath string `bun:"inputpath" json:"inputpath"`
}

type runtimeDefaultsDocument struct {
	BaseModel
	Name      string `bun:"name" json:"name" default:"Draft"`
	IsActive  bool   `bun:"is_active" json:"is_active" default:"true"`
	RetrySeed int    `bun:"retry_seed" json:"retry_seed" default:"3"`
}

type runtimeJSONDefaultsDocument struct {
	BaseModel
	Settings map[string]any `bun:"settings,type:jsonb,nullzero" json:"settings" meta:"kind:json"`
	Tags     []string       `bun:"tags,type:jsonb,nullzero" json:"tags" meta:"kind:json"`
}

type runtimeArchiveDocument struct {
	BaseModel
	Name     string `bun:"name" json:"name"`
	IsActive bool   `bun:"is_active" json:"is_active"`
}

type runtimeToggleHookDocument struct {
	BaseModel
	ToggleCalls *int `bun:"-" json:"-"`
}

func (d *runtimeToggleHookDocument) ToggleActive() bool {
	if d.ToggleCalls != nil {
		*d.ToggleCalls = *d.ToggleCalls + 1
	}
	return d.BaseModel.ToggleActive()
}

type runtimeNoActiveDocument struct {
	ID   int64  `bun:"id,pk,autoincrement" json:"id"`
	Name string `bun:"name" json:"name"`
}

type runtimeExecutorStub struct {
	searchResult          []any
	searchCountResult     int64
	readResult            []RecordMap
	searchReadResult      []RecordMap
	createResult          RecordMap
	writeResult           bool
	unlinkResult          bool
	lastSearchFilter      RecordMap
	lastSearchCountFilter RecordMap
	lastReadFields        []string
	lastSearchReadFilter  RecordMap
	lastSearchReadFields  []string
}

func (s *runtimeExecutorStub) Metadata() *ModelMetadata { return nil }
func (s *runtimeExecutorStub) ExecuteAction(_ context.Context, _ string, _ RecordMap, _ any) (any, error) {
	return nil, nil
}
func (s *runtimeExecutorStub) RuntimeSearch(_ context.Context, _ RecordMap) ([]any, error) {
	return s.searchResult, nil
}
func (s *runtimeExecutorStub) RuntimeSearchCount(_ context.Context, filter RecordMap) (int64, error) {
	s.lastSearchCountFilter = cloneRecordMap(filter)
	return s.searchCountResult, nil
}
func (s *runtimeExecutorStub) RuntimeRead(_ context.Context, _ []string, fields []string) ([]RecordMap, error) {
	s.lastReadFields = append([]string(nil), fields...)
	return s.readResult, nil
}
func (s *runtimeExecutorStub) RuntimeSearchRead(_ context.Context, filter RecordMap, fields []string) ([]RecordMap, error) {
	s.lastSearchReadFilter = cloneRecordMap(filter)
	s.lastSearchReadFields = append([]string(nil), fields...)
	return s.searchReadResult, nil
}
func (s *runtimeExecutorStub) RuntimeCreate(_ context.Context, _ RecordMap) (RecordMap, error) {
	return s.createResult, nil
}
func (s *runtimeExecutorStub) RuntimeWrite(_ context.Context, _ []string, _ RecordMap) (bool, error) {
	return s.writeResult, nil
}
func (s *runtimeExecutorStub) RuntimeUnlink(_ context.Context, _ []string) (bool, error) {
	return s.unlinkResult, nil
}

type runtimeToggleExecutorStub struct {
	toggleResult bool
	toggleIDs    []string
	calls        int
}

func (s *runtimeToggleExecutorStub) Metadata() *ModelMetadata { return nil }
func (s *runtimeToggleExecutorStub) ExecuteAction(_ context.Context, _ string, _ RecordMap, _ any) (any, error) {
	return nil, nil
}
func (s *runtimeToggleExecutorStub) RuntimeSearch(_ context.Context, _ RecordMap) ([]any, error) {
	return nil, nil
}

func TestRegisterModelTypeInfersObjectJSONBDefaultOnlyForMaps(t *testing.T) {
	meta, err := RegisterModelType[runtimeJSONDefaultsDocument](ModelSpec{
		Name:       "test.runtime_json_defaults",
		Definition: ModelDefinition("test.runtime_json_defaults", (*runtimeJSONDefaultsDocument)(nil)),
	})
	if err != nil {
		t.Fatalf("RegisterModelType() error = %v", err)
	}

	settings, ok := runtimeLookupField(meta, "settings")
	if !ok {
		t.Fatalf("expected settings field metadata")
	}
	if settings.Default != "{}" {
		t.Fatalf("expected settings default {}, got %q", settings.Default)
	}

	tags, ok := runtimeLookupField(meta, "tags")
	if !ok {
		t.Fatalf("expected tags field metadata")
	}
	if tags.Default != "[]" {
		t.Fatalf("expected slice-backed jsonb field to default to [], got %q", tags.Default)
	}
}

func TestParseRuntimeDefaultValueDecodesJSONDefaults(t *testing.T) {
	decoded := parseRuntimeDefaultValue(FieldMetadata{
		Name:    "settings",
		Type:    "map[string]interface {}",
		Kind:    FieldKindJSON,
		Default: "{}",
	})
	value, ok := decoded.(map[string]any)
	if !ok {
		t.Fatalf("expected json default to decode into map, got %T", decoded)
	}
	if len(value) != 0 {
		t.Fatalf("expected empty json default map, got %#v", value)
	}

	decodedSlice := parseRuntimeDefaultValue(FieldMetadata{
		Name:    "tags",
		Type:    "[]string",
		Kind:    FieldKindJSON,
		Default: "[]",
	})
	values, ok := decodedSlice.([]any)
	if !ok {
		t.Fatalf("expected json array default to decode into []any, got %T", decodedSlice)
	}
	if len(values) != 0 {
		t.Fatalf("expected empty json default slice, got %#v", values)
	}
}
func (s *runtimeToggleExecutorStub) RuntimeSearchCount(_ context.Context, _ RecordMap) (int64, error) {
	return 0, nil
}
func (s *runtimeToggleExecutorStub) RuntimeRead(_ context.Context, _ []string, _ []string) ([]RecordMap, error) {
	return nil, nil
}
func (s *runtimeToggleExecutorStub) RuntimeSearchRead(_ context.Context, _ RecordMap, _ []string) ([]RecordMap, error) {
	return nil, nil
}
func (s *runtimeToggleExecutorStub) RuntimeCreate(_ context.Context, _ RecordMap) (RecordMap, error) {
	return nil, nil
}
func (s *runtimeToggleExecutorStub) RuntimeWrite(_ context.Context, _ []string, _ RecordMap) (bool, error) {
	return false, nil
}
func (s *runtimeToggleExecutorStub) RuntimeUnlink(_ context.Context, _ []string) (bool, error) {
	return false, nil
}
func (s *runtimeToggleExecutorStub) RuntimeToggleActive(_ context.Context, ids []string) (bool, error) {
	s.calls++
	s.toggleIDs = append([]string(nil), ids...)
	return s.toggleResult, nil
}

type definitionBackedDocument struct {
	BaseModel
	Channels string  `bun:"channels" json:"channels" meta:"kind:json,help:Last prepared channel list for a websocket client."`
	UserID   *int64  `bun:"user_id" json:"user_id" meta:"kind:many2one,model:base.users,ondelete:SET NULL,index"`
	Role     string  `bun:"role" json:"role" selection:"(admin,Admin),(user,User)"`
	Total    float64 `bun:"total" json:"total"`
}

func TestModelRegistryRegisterExtractsMetadata(t *testing.T) {
	registry := NewModelRegistry()

	meta, err := registry.Register(goTypeOf[registryBankDocument](), ModelSpec{
		Name:  "bank.account",
		Label: "Bank Account",
	}.WithStorageName("bank_accounts"))
	if err != nil {
		t.Fatalf("register bank model: %v", err)
	}

	if meta.GoType == "" {
		t.Fatalf("expected go type to be populated")
	}

	if meta.StorageName != "bank_accounts" {
		t.Fatalf("expected storage name to be bank_accounts, got %q", meta.StorageName)
	}

	found := false
	for _, field := range meta.Fields {
		if field.GoName != "ProxyValue" {
			continue
		}
		found = true
		if field.Name != "proxy_value" {
			t.Fatalf("expected proxy_value field name, got %q", field.Name)
		}
		if field.BunColumn != "proxy_value" {
			t.Fatalf("expected bun column proxy_value, got %q", field.BunColumn)
		}
	}

	if !found {
		t.Fatalf("expected ProxyValue field metadata to be present")
	}
}

func TestModelRegistryRegisterUpdatesStorageNameForSameModel(t *testing.T) {
	registry := NewModelRegistry()

	_, err := registry.Register(goTypeOf[registryWebhookSchemaDocument](), ModelSpec{
		Name:  "webhook.schema",
		Label: "Webhook Schema",
	}.WithStorageName("schema_dev"))
	if err != nil {
		t.Fatalf("first register: %v", err)
	}

	meta, err := registry.Register(goTypeOf[registryWebhookSchemaDocument](), ModelSpec{
		Name:  "webhook.schema",
		Label: "Webhook Schema",
	}.WithStorageName("schema_prod"))
	if err != nil {
		t.Fatalf("second register: %v", err)
	}

	if meta.StorageName != "schema_prod" {
		t.Fatalf("expected storage name to update to schema_prod, got %q", meta.StorageName)
	}
}

func TestModelSpecResolveStorageName(t *testing.T) {
	spec := ModelSpec{
		Name: "bank.account",
		StorageNameFunc: func() string {
			return "bank_accounts_runtime"
		},
	}

	if got := spec.ResolveStorageName(); got != "bank_accounts_runtime" {
		t.Fatalf("expected runtime storage name, got %q", got)
	}

	override := spec.WithStorageName("bank_accounts_override")
	if got := override.ResolveStorageName(); got != "bank_accounts_override" {
		t.Fatalf("expected override storage name, got %q", got)
	}
}

func TestModelRegistryRejectsDuplicateNameForDifferentType(t *testing.T) {
	registry := NewModelRegistry()

	_, err := registry.Register(goTypeOf[registryBankDocument](), ModelSpec{
		Name:  "shared.name",
		Label: "Bank Account",
	})
	if err != nil {
		t.Fatalf("first register: %v", err)
	}

	_, err = registry.Register(goTypeOf[registryWebhookSchemaDocument](), ModelSpec{
		Name:  "shared.name",
		Label: "Webhook Schema",
	})
	if err == nil {
		t.Fatalf("expected duplicate name conflict")
	}
}

func TestModelRegistryExtractsOptFilterMetadata(t *testing.T) {
	registry := NewModelRegistry()

	meta, err := registry.Register(goTypeOf[optTaggedFilterDocument](), ModelSpec{
		Name:  "test.opt_filter",
		Label: "Opt Filter Test",
	}.WithStorageName("opt_filter_docs"))
	if err != nil {
		t.Fatalf("register opt filter model: %v", err)
	}

	fields := make(map[string]FieldMetadata, len(meta.Fields))
	for _, field := range meta.Fields {
		fields[field.GoName] = field
	}

	if fields["BankBIC"].FilterKey != "bic" {
		t.Fatalf("expected custom filter key bic, got %q", fields["BankBIC"].FilterKey)
	}
	if fields["BankBIC"].Label != "Bank BIC" {
		t.Fatalf("expected label to come from meta tag, got %q", fields["BankBIC"].Label)
	}
	if fields["BankBIC"].Help != "Used for external lookup." {
		t.Fatalf("expected help to come from meta tag, got %q", fields["BankBIC"].Help)
	}
	if fields["ProxyRef"].FilterKey != "proxy_ref" {
		t.Fatalf("expected bare filter flag to default to field name, got %q", fields["ProxyRef"].FilterKey)
	}
	if fields["Hidden"].FilterKey != "" {
		t.Fatalf("expected filter:false to suppress filter key, got %q", fields["Hidden"].FilterKey)
	}
}

func TestModelRegistryRegistersDefinitionBackedMetadata(t *testing.T) {
	registry := NewModelRegistry()

	meta, err := registry.Register(goTypeOf[definitionBackedDocument](), ModelSpec{
		Name:       "test.definition_backed",
		Label:      "Definition Backed",
		Definition: ModelDefinition("test.definition_backed", (*definitionBackedDocument)(nil)),
	}.WithStorageName("definition_backed_docs"))
	if err != nil {
		t.Fatalf("register definition-backed model: %v", err)
	}

	fields := make(map[string]FieldMetadata, len(meta.Fields))
	for _, field := range meta.Fields {
		fields[field.Name] = field
	}

	if fields["channels"].Kind != FieldKindJSON {
		t.Fatalf("expected channels kind json, got %s", fields["channels"].Kind)
	}
	if fields["channels"].Help != "Last prepared channel list for a websocket client." {
		t.Fatalf("expected channels help to survive definition registration, got %q", fields["channels"].Help)
	}
	if fields["user_id"].Relation != "base.users" || fields["user_id"].OnDelete != "SET NULL" {
		t.Fatalf("expected user_id relation metadata, got relation=%q ondelete=%q", fields["user_id"].Relation, fields["user_id"].OnDelete)
	}
	if !fields["user_id"].Indexed {
		t.Fatalf("expected user_id to be indexed from meta tag")
	}
	if fields["role"].Kind != FieldKindSelection || len(fields["role"].Selection) != 2 {
		t.Fatalf("expected role selection metadata, got kind=%s selection=%#v", fields["role"].Kind, fields["role"].Selection)
	}
}

func TestRuntimeSearchOptionsToleratesNilMetadata(t *testing.T) {
	handler := &Handler[registryBankDocument]{}

	direct, domain, order, offset, limit, nameNeedle, ids, err := handler.runtimeSearchOptions(RecordMap{
		"name":     "bank",
		"__offset": 3,
		"__limit":  7,
		"__name":   "needle",
		"__ids":    []string{"11", "22"},
	})
	if err != nil {
		t.Fatalf("runtime search options: %v", err)
	}

	if domain.Kind == "" {
		t.Fatal("expected default true domain when metadata is nil")
	}
	if order != "" {
		t.Fatalf("order = %q, want empty string when metadata is nil", order)
	}
	if offset != 3 || limit != 7 {
		t.Fatalf("offset/limit = %d/%d, want 3/7", offset, limit)
	}
	if nameNeedle != "needle" {
		t.Fatalf("nameNeedle = %q, want needle", nameNeedle)
	}
	if len(ids) != 2 || ids[0] != "11" || ids[1] != "22" {
		t.Fatalf("ids = %#v, want [11 22]", ids)
	}
	if direct["name"] != "bank" {
		t.Fatalf("direct filter = %#v, want name preserved", direct)
	}
}

func TestDefaultRuntimeMethodsAreRegistered(t *testing.T) {
	required := []string{"fields_get", "default_get", "create", "write", "unlink", "get_helper", "get_json_dto"}
	for _, name := range required {
		method, ok := LookupRuntimeMethod(name)
		if !ok {
			t.Fatalf("expected runtime method %q to be registered", name)
		}
		if method.Name != name {
			t.Fatalf("expected runtime method name %q, got %q", name, method.Name)
		}
	}
}

func TestModelRegistryIncludesDefaultRuntimeMethods(t *testing.T) {
	registry := NewModelRegistry()

	meta, err := registry.Register(goTypeOf[runtimeDefaultsDocument](), ModelSpec{
		Name:  "test.runtime_defaults",
		Label: "Runtime Defaults",
	}.WithStorageName("runtime_defaults"))
	if err != nil {
		t.Fatalf("register runtime defaults model: %v", err)
	}

	if len(meta.RuntimeMethods) == 0 {
		t.Fatalf("expected runtime methods to be attached to model metadata")
	}

	method, ok := meta.LookupRuntimeMethod("fields_get")
	if !ok {
		t.Fatalf("expected fields_get to be available on model metadata")
	}
	fieldsRaw, err := method.Call(context.Background(), meta, nil, []any{[]string{"name"}}, nil)
	if err != nil {
		t.Fatalf("fields_get call: %v", err)
	}

	fields, ok := fieldsRaw.(map[string]FieldMetadata)
	if !ok {
		t.Fatalf("expected fields_get result map, got %T", fieldsRaw)
	}
	if _, ok := fields["name"]; !ok {
		t.Fatalf("expected fields_get to resolve requested field metadata")
	}
}

func TestModelRegistryAllowsRuntimeMethodOverride(t *testing.T) {
	registry := NewModelRegistry()

	meta, err := registry.Register(goTypeOf[runtimeDefaultsDocument](), ModelSpec{
		Name:  "test.runtime_override",
		Label: "Runtime Override",
		RuntimeMethods: []RuntimeMethod{
			{
				Name:      "default_get",
				Public:    true,
				ModelOnly: true,
				ReadOnly:  true,
				Call: func(_ context.Context, _ *ModelMetadata, _ []string, _ []any, _ map[string]any) (any, error) {
					return map[string]any{"name": "override"}, nil
				},
			},
		},
	}.WithStorageName("runtime_override"))
	if err != nil {
		t.Fatalf("register runtime override model: %v", err)
	}

	method, ok := meta.LookupRuntimeMethod("default_get")
	if !ok {
		t.Fatalf("expected default_get override to be available")
	}

	result, err := method.Call(context.Background(), meta, nil, nil, nil)
	if err != nil {
		t.Fatalf("default_get override call: %v", err)
	}

	values, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("expected map result from override, got %T", result)
	}
	if values["name"] != "override" {
		t.Fatalf("expected override result, got %#v", values)
	}
}

func TestModelRegistryAppliesStandardRuntimeHooks(t *testing.T) {
	registry := NewModelRegistry()

	meta, err := registry.Register(goTypeOf[runtimeDefaultsDocument](), ModelSpec{
		Name:  "test.standard_runtime_hooks",
		Label: "Standard Runtime Hooks",
		StandardHooks: StandardRuntimeHooks{
			Create: func(_ context.Context, _ *ModelMetadata, _ []string, _ []any, _ map[string]any) (any, error) {
				return RecordMap{"id": int64(77), "name": "hooked"}, nil
			},
			ViewHeaderGet: func(_ context.Context, _ *ModelMetadata, _ []string, _ []any, _ map[string]any) (any, error) {
				return "hooked header", nil
			},
		},
	}.WithStorageName("standard_runtime_hooks"))
	if err != nil {
		t.Fatalf("register standard runtime hooks model: %v", err)
	}

	createMethod, ok := meta.LookupRuntimeMethod("create")
	if !ok {
		t.Fatalf("expected create method")
	}
	if !createMethod.Public || createMethod.ReadOnly || !createMethod.ModelOnly {
		t.Fatalf("create metadata mismatch: %#v", createMethod)
	}

	created, err := createMethod.Call(context.Background(), meta, nil, nil, nil)
	if err != nil {
		t.Fatalf("create hook call: %v", err)
	}
	if created.(RecordMap)["name"] != "hooked" {
		t.Fatalf("unexpected create hook result %#v", created)
	}

	headerMethod, ok := meta.LookupRuntimeMethod("view_header_get")
	if !ok {
		t.Fatalf("expected view_header_get method")
	}
	if !headerMethod.Public || !headerMethod.ReadOnly || !headerMethod.ModelOnly {
		t.Fatalf("view_header_get metadata mismatch: %#v", headerMethod)
	}
	header, err := headerMethod.Call(context.Background(), meta, nil, nil, nil)
	if err != nil {
		t.Fatalf("view_header_get hook call: %v", err)
	}
	if header.(string) != "hooked header" {
		t.Fatalf("unexpected view_header_get result %#v", header)
	}
}

func TestExplicitRuntimeMethodOverrideWinsOverStandardHook(t *testing.T) {
	registry := NewModelRegistry()

	meta, err := registry.Register(goTypeOf[runtimeDefaultsDocument](), ModelSpec{
		Name:  "test.standard_runtime_hooks_explicit_override",
		Label: "Standard Runtime Hooks Explicit Override",
		StandardHooks: StandardRuntimeHooks{
			DefaultGet: func(_ context.Context, _ *ModelMetadata, _ []string, _ []any, _ map[string]any) (any, error) {
				return map[string]any{"name": "hook"}, nil
			},
		},
		RuntimeMethods: []RuntimeMethod{
			{
				Name:      "default_get",
				Public:    true,
				ModelOnly: true,
				ReadOnly:  true,
				Call: func(_ context.Context, _ *ModelMetadata, _ []string, _ []any, _ map[string]any) (any, error) {
					return map[string]any{"name": "explicit"}, nil
				},
			},
		},
	}.WithStorageName("standard_runtime_hooks_explicit_override"))
	if err != nil {
		t.Fatalf("register model: %v", err)
	}

	method, ok := meta.LookupRuntimeMethod("default_get")
	if !ok {
		t.Fatalf("expected default_get method")
	}
	result, err := method.Call(context.Background(), meta, nil, nil, nil)
	if err != nil {
		t.Fatalf("default_get call: %v", err)
	}
	if result.(map[string]any)["name"] != "explicit" {
		t.Fatalf("unexpected default_get result %#v", result)
	}
}

func TestDefaultGetUsesFieldDefaults(t *testing.T) {
	registry := NewModelRegistry()

	meta, err := registry.Register(goTypeOf[runtimeDefaultsDocument](), ModelSpec{
		Name:  "test.default_get",
		Label: "Default Get",
	}.WithStorageName("default_get_docs"))
	if err != nil {
		t.Fatalf("register default get model: %v", err)
	}

	method, ok := meta.LookupRuntimeMethod("default_get")
	if !ok {
		t.Fatalf("expected default_get method")
	}

	result, err := method.Call(context.Background(), meta, nil, nil, map[string]any{
		"fields": []string{"name", "is_active", "retry_seed"},
	})
	if err != nil {
		t.Fatalf("default_get call: %v", err)
	}

	defaults, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("expected map result from default_get, got %T", result)
	}
	if defaults["name"] != "Draft" {
		t.Fatalf("expected string default Draft, got %#v", defaults["name"])
	}
	if defaults["is_active"] != true {
		t.Fatalf("expected bool default true, got %#v", defaults["is_active"])
	}
	if defaults["retry_seed"] != int64(3) {
		t.Fatalf("expected integer default 3, got %#v", defaults["retry_seed"])
	}
}

func TestRuntimeCRUDMethodsDelegateToRegisteredExecutor(t *testing.T) {
	registry := NewModelRegistry()

	meta, err := registry.Register(goTypeOf[runtimeDefaultsDocument](), ModelSpec{
		Name:  "test.runtime_crud_methods",
		Label: "Runtime CRUD Methods",
	}.WithStorageName("runtime_crud_method_docs"))
	if err != nil {
		t.Fatalf("register runtime crud method model: %v", err)
	}

	executor := &runtimeExecutorStub{
		searchResult:      []any{int64(11), int64(22)},
		searchCountResult: 2,
		readResult:        []RecordMap{{"id": int64(11), "name": "A"}},
		searchReadResult:  []RecordMap{{"id": int64(22), "name": "B"}},
		createResult:      RecordMap{"id": int64(33), "name": "Created"},
		writeResult:       true,
		unlinkResult:      true,
	}
	RegisterRuntimeExecutor(meta.Name, executor)

	searchMethod, ok := meta.LookupRuntimeMethod("search")
	if !ok {
		t.Fatalf("expected search runtime method")
	}
	searchResult, err := searchMethod.Call(context.Background(), meta, nil, nil, map[string]any{"filter": RecordMap{"name": "A"}})
	if err != nil {
		t.Fatalf("search call: %v", err)
	}
	if len(searchResult.([]any)) != 2 {
		t.Fatalf("expected 2 ids from search, got %#v", searchResult)
	}

	searchCountMethod, ok := meta.LookupRuntimeMethod("search_count")
	if !ok {
		t.Fatalf("expected search_count runtime method")
	}
	searchCountResult, err := searchCountMethod.Call(context.Background(), meta, nil, nil, map[string]any{"filter": RecordMap{"name": "A"}})
	if err != nil {
		t.Fatalf("search_count call: %v", err)
	}
	if searchCountResult.(int64) != 2 {
		t.Fatalf("expected search_count 2, got %#v", searchCountResult)
	}

	readMethod, ok := meta.LookupRuntimeMethod("read")
	if !ok {
		t.Fatalf("expected read runtime method")
	}
	readResult, err := readMethod.Call(context.Background(), meta, []string{"11"}, nil, map[string]any{"fields": []string{"id", "name"}})
	if err != nil {
		t.Fatalf("read call: %v", err)
	}
	if len(readResult.([]RecordMap)) != 1 {
		t.Fatalf("expected one record from read, got %#v", readResult)
	}

	searchReadMethod, ok := meta.LookupRuntimeMethod("search_read")
	if !ok {
		t.Fatalf("expected search_read runtime method")
	}
	searchReadResult, err := searchReadMethod.Call(context.Background(), meta, nil, nil, map[string]any{"filter": RecordMap{"name": "B"}})
	if err != nil {
		t.Fatalf("search_read call: %v", err)
	}
	if len(searchReadResult.([]RecordMap)) != 1 {
		t.Fatalf("expected one record from search_read, got %#v", searchReadResult)
	}

	createMethod, ok := meta.LookupRuntimeMethod("create")
	if !ok {
		t.Fatalf("expected create runtime method")
	}
	createResult, err := createMethod.Call(context.Background(), meta, nil, []any{RecordMap{"name": "Created"}}, nil)
	if err != nil {
		t.Fatalf("create call: %v", err)
	}
	if createResult.(RecordMap)["name"] != "Created" {
		t.Fatalf("expected created record result, got %#v", createResult)
	}

	writeMethod, ok := meta.LookupRuntimeMethod("write")
	if !ok {
		t.Fatalf("expected write runtime method")
	}
	writeResult, err := writeMethod.Call(context.Background(), meta, []string{"33"}, []any{RecordMap{"name": "Updated"}}, nil)
	if err != nil {
		t.Fatalf("write call: %v", err)
	}
	if writeResult.(bool) != true {
		t.Fatalf("expected write to return true, got %#v", writeResult)
	}

	unlinkMethod, ok := meta.LookupRuntimeMethod("unlink")
	if !ok {
		t.Fatalf("expected unlink runtime method")
	}
	unlinkResult, err := unlinkMethod.Call(context.Background(), meta, []string{"33"}, nil, nil)
	if err != nil {
		t.Fatalf("unlink call: %v", err)
	}
	if unlinkResult.(bool) != true {
		t.Fatalf("expected unlink to return true, got %#v", unlinkResult)
	}
}

func TestRuntimeReadRequiresIDs(t *testing.T) {
	registry := NewModelRegistry()

	meta, err := registry.Register(goTypeOf[runtimeDefaultsDocument](), ModelSpec{
		Name:  "test.runtime_read_requitbl_ids",
		Label: "Runtime Read Requires IDs",
	}.WithStorageName("runtime_read_requitbl_ids"))
	if err != nil {
		t.Fatalf("register runtime read model: %v", err)
	}

	readMethod, ok := meta.LookupRuntimeMethod("read")
	if !ok {
		t.Fatalf("expected read runtime method")
	}

	if _, err := readMethod.Call(context.Background(), meta, nil, nil, nil); err == nil {
		t.Fatalf("expected read without ids to fail")
	}
}

func TestRuntimeSearchFetchAndExistsDelegateToExecutor(t *testing.T) {
	registry := NewModelRegistry()

	meta, err := registry.Register(goTypeOf[runtimeDefaultsDocument](), ModelSpec{
		Name:  "test.runtime_search_fetch_exists",
		Label: "Runtime Search Fetch Exists",
	}.WithStorageName("runtime_search_fetch_exists"))
	if err != nil {
		t.Fatalf("register runtime search_fetch model: %v", err)
	}

	RegisterRuntimeExecutor(meta.Name, &runtimeExecutorStub{
		readResult:       []RecordMap{{"id": int64(11), "name": "A"}},
		searchReadResult: []RecordMap{{"id": int64(22), "name": "B"}},
	})

	searchFetchMethod, ok := meta.LookupRuntimeMethod("search_fetch")
	if !ok {
		t.Fatalf("expected search_fetch runtime method")
	}
	searchFetchResult, err := searchFetchMethod.Call(context.Background(), meta, nil, nil, map[string]any{
		"filter": RecordMap{"name": "B"},
		"fields": []string{"name"},
	})
	if err != nil {
		t.Fatalf("search_fetch call: %v", err)
	}
	if len(searchFetchResult.([]RecordMap)) != 1 || searchFetchResult.([]RecordMap)[0]["name"] != "B" {
		t.Fatalf("unexpected search_fetch result %#v", searchFetchResult)
	}

	existsMethod, ok := meta.LookupRuntimeMethod("exists")
	if !ok {
		t.Fatalf("expected exists runtime method")
	}
	existsResult, err := existsMethod.Call(context.Background(), meta, []string{"11", "99"}, nil, nil)
	if err != nil {
		t.Fatalf("exists call: %v", err)
	}
	if len(existsResult.([]any)) != 1 || existsResult.([]any)[0] != int64(11) {
		t.Fatalf("unexpected exists result %#v", existsResult)
	}
}

func TestRuntimeArchiveHelpersUseBooleanActiveField(t *testing.T) {
	registry := NewModelRegistry()

	meta, err := registry.Register(goTypeOf[runtimeArchiveDocument](), ModelSpec{
		Name:       "test.runtime_archive",
		Label:      "Runtime Archive",
		Definition: ModelDefinition("test.runtime_archive", (*runtimeArchiveDocument)(nil)),
	}.WithStorageName("runtime_archive"))
	if err != nil {
		t.Fatalf("register runtime archive model: %v", err)
	}

	RegisterRuntimeExecutor(meta.Name, &runtimeExecutorStub{
		readResult: []RecordMap{
			{"id": int64(11), "is_active": true},
			{"id": int64(22), "is_active": false},
		},
		writeResult: true,
	})

	toggleMethod, ok := meta.LookupRuntimeMethod("toggle_active")
	if !ok {
		t.Fatalf("expected toggle_active runtime method")
	}
	toggleResult, err := toggleMethod.Call(context.Background(), meta, []string{"11", "22"}, nil, nil)
	if err != nil {
		t.Fatalf("toggle_active call: %v", err)
	}
	if toggleResult.(bool) != true {
		t.Fatalf("expected toggle_active to return true, got %#v", toggleResult)
	}
}

func TestRuntimeToggleActivePrefersExecutorHook(t *testing.T) {
	registry := NewModelRegistry()

	meta, err := registry.Register(goTypeOf[runtimeDefaultsDocument](), ModelSpec{
		Name:  "test.runtime_toggle_executor",
		Label: "Runtime Toggle Executor",
	}.WithStorageName("runtime_toggle_executor"))
	if err != nil {
		t.Fatalf("register runtime toggle executor model: %v", err)
	}

	executor := &runtimeToggleExecutorStub{toggleResult: true}
	RegisterRuntimeExecutor(meta.Name, executor)

	toggleMethod, ok := meta.LookupRuntimeMethod("toggle_active")
	if !ok {
		t.Fatalf("expected toggle_active runtime method")
	}

	result, err := toggleMethod.Call(context.Background(), meta, []string{"11", "22"}, nil, nil)
	if err != nil {
		t.Fatalf("toggle_active call: %v", err)
	}
	if result.(bool) != true {
		t.Fatalf("expected toggle_active result true, got %#v", result)
	}
	if executor.calls != 1 {
		t.Fatalf("expected runtime toggle hook to be called once, got %d", executor.calls)
	}
	if got := strings.Join(executor.toggleIDs, ","); got != "11,22" {
		t.Fatalf("expected toggle ids 11,22, got %q", got)
	}
}

func TestRuntimeToggleDocumentStateUsesToggleHookForActiveField(t *testing.T) {
	toggleCalls := 0
	recordID, nextState, err := runtimeToggleDocumentState(runtimeToggleHookDocument{
		BaseModel:   BaseModel{ID: 55, Active: true},
		ToggleCalls: &toggleCalls,
	}, "active")
	if err != nil {
		t.Fatalf("runtimeToggleDocumentState: %v", err)
	}
	if recordID != int64(55) {
		t.Fatalf("record id = %#v, want 55", recordID)
	}
	if nextState != false {
		t.Fatalf("nextState = %#v, want false", nextState)
	}
	if toggleCalls != 1 {
		t.Fatalf("toggle hook calls = %d, want 1", toggleCalls)
	}
}

func TestRuntimeReadHelpersAllowModelsWithoutActiveField(t *testing.T) {
	registry := NewModelRegistry()

	meta, err := registry.Register(goTypeOf[runtimeNoActiveDocument](), ModelSpec{
		Name:       "test.runtime_no_active",
		Label:      "Runtime No Active",
		Definition: ModelDefinition("test.runtime_no_active", (*runtimeNoActiveDocument)(nil)),
	}.WithStorageName("runtime_no_active"))
	if err != nil {
		t.Fatalf("register runtime no-active model: %v", err)
	}

	executor := &runtimeExecutorStub{
		searchCountResult: 1,
		readResult:        []RecordMap{{"id": int64(11), "name": "A"}},
		searchReadResult:  []RecordMap{{"id": int64(11), "name": "A"}},
	}
	RegisterRuntimeExecutor(meta.Name, executor)

	ctx := ContextWithEnvironment(context.Background(), NewEnvironment(nil, 7, map[string]any{"active_test": true}, false))

	searchCountMethod, ok := meta.LookupRuntimeMethod("search_count")
	if !ok {
		t.Fatalf("expected search_count runtime method")
	}
	searchCountResult, err := searchCountMethod.Call(ctx, meta, nil, nil, map[string]any{"filter": RecordMap{"name": "A"}})
	if err != nil {
		t.Fatalf("search_count call: %v", err)
	}
	if searchCountResult.(int64) != 1 {
		t.Fatalf("expected search_count 1, got %#v", searchCountResult)
	}
	if _, exists := executor.lastSearchCountFilter["active"]; exists {
		t.Fatalf("expected search_count filter to skip active injection, got %#v", executor.lastSearchCountFilter)
	}

	readMethod, ok := meta.LookupRuntimeMethod("read")
	if !ok {
		t.Fatalf("expected read runtime method")
	}
	readResult, err := readMethod.Call(ctx, meta, []string{"11"}, nil, map[string]any{"fields": []string{"id", "name"}})
	if err != nil {
		t.Fatalf("read call: %v", err)
	}
	if len(readResult.([]RecordMap)) != 1 {
		t.Fatalf("expected one record from read, got %#v", readResult)
	}
	if len(executor.lastReadFields) != 2 || executor.lastReadFields[0] != "id" || executor.lastReadFields[1] != "name" {
		t.Fatalf("unexpected read fields %#v", executor.lastReadFields)
	}

	searchReadMethod, ok := meta.LookupRuntimeMethod("search_read")
	if !ok {
		t.Fatalf("expected search_read runtime method")
	}
	searchReadResult, err := searchReadMethod.Call(ctx, meta, nil, nil, map[string]any{
		"filter": RecordMap{"name": "A"},
		"fields": []string{"id", "name"},
	})
	if err != nil {
		t.Fatalf("search_read call: %v", err)
	}
	if len(searchReadResult.([]RecordMap)) != 1 {
		t.Fatalf("expected one record from search_read, got %#v", searchReadResult)
	}
	if _, exists := executor.lastSearchReadFilter["active"]; exists {
		t.Fatalf("expected search_read filter to skip active injection, got %#v", executor.lastSearchReadFilter)
	}
	if len(executor.lastSearchReadFields) != 2 || executor.lastSearchReadFields[0] != "id" || executor.lastSearchReadFields[1] != "name" {
		t.Fatalf("unexpected search_read fields %#v", executor.lastSearchReadFields)
	}
}

func TestRuntimeModelArtifactMethodsFallbackToRegistryMetadata(t *testing.T) {
	registry := NewModelRegistry()

	meta, err := registry.Register(goTypeOf[runtimeNoActiveDocument](), ModelSpec{
		Name:        "test.runtime_artifacts",
		Label:       "Runtime Artifacts",
		Description: "Registry-backed artifact helpers",
		Definition:  ModelDefinition("test.runtime_artifacts", (*runtimeNoActiveDocument)(nil)),
	}.WithStorageName("runtime_artifacts"))
	if err != nil {
		t.Fatalf("register runtime artifact model: %v", err)
	}

	helperMethod, ok := meta.LookupRuntimeMethod("get_helper")
	if !ok {
		t.Fatalf("expected get_helper runtime method")
	}
	helperRaw, err := helperMethod.Call(context.Background(), meta, nil, nil, nil)
	if err != nil {
		t.Fatalf("get_helper call: %v", err)
	}
	helper, ok := helperRaw.(string)
	if !ok {
		t.Fatalf("expected get_helper string result, got %T", helperRaw)
	}
	if helper != "" {
		t.Fatalf("expected empty helper when fields do not expose help text, got %q", helper)
	}

	jsonMethod, ok := meta.LookupRuntimeMethod("get_json_dto")
	if !ok {
		t.Fatalf("expected get_json_dto runtime method")
	}
	dtoRaw, err := jsonMethod.Call(context.Background(), meta, nil, nil, nil)
	if err != nil {
		t.Fatalf("get_json_dto call: %v", err)
	}
	dto, ok := dtoRaw.(map[string]any)
	if !ok {
		t.Fatalf("expected get_json_dto map result, got %T", dtoRaw)
	}
	if dto["name"] != "test.runtime_artifacts" {
		t.Fatalf("dto name = %#v, want test.runtime_artifacts", dto["name"])
	}
}
