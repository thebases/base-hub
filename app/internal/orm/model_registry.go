package orm

// File overview:
// - Responsibility: implements the metadata-driven ORM/runtime kernel for model registry, where storage, runtime dispatch, and security rules stay centralized.
// - Key symbols: `ModelSpec`, `string`, `StorageBackend`, `ModelSpec`.
// - Pattern: Leans on metadata-driven registration so storage mapping, runtime methods, and behavior hooks stay centralized instead of being scattered across handlers.
// - Pattern: Protects shared mutable state with explicit synchronization or worker coordination rather than relying on unsupervised goroutine side effects.

import (
	"context"
	"fmt"
	"git.thebase.vn/base_hub/internal/services/log"
	"reflect"
	"strings"
	"sync"
	"time"
)

type StorageBackend string

const (
	StorageBackendBun StorageBackend = "bun"
)

type ModelSpec struct {
	Name        string
	Label       string
	Description string
	// DescriptionFile is a path (`<module>/<file>`, resolved to <module>/<lang>/<file> under internal/documents/apidoc or modules/<module>/apidoc) to a markdown/text document shown
	// in the API docs behind a "Show Description" popup. Independent of Description.
	DescriptionFile string
	RecName         string
	DefaultOrder    string
	Persistence     string
	StorageNameFunc func() string
	Definition      *SchemaDefinition
	StandardHooks   StandardRuntimeHooks
	RuntimeMethods  []RuntimeMethod
	// Prepare runs inside the guarded default create/write (CreateRecord /
	// WriteRecords) after payload normalisation and before the company guards.
	// It may rewrite op.Values (canonical field names) or reject the write.
	Prepare func(ctx context.Context, op WriteOp) error
}

func (s ModelSpec) WithStorageName(name string) ModelSpec {
	if strings.TrimSpace(name) == "" {
		return s
	}
	if s.Definition != nil {
		// Clone the definition before mutating the table name so shared package
		// definitions remain immutable across registrations.
		cloned := s.Definition.Clone()
		cloned.Table = name
		cloned.Normalize()
		s.Definition = cloned
	}
	// Store the override as a function so callers can still defer evaluation if
	// they build specs dynamically elsewhere.
	s.StorageNameFunc = func() string { return name }
	return s
}

// ResolveStorageName resolves the storage name handled by this package.
func (s ModelSpec) ResolveStorageName() string {
	if s.StorageNameFunc != nil {
		// An explicit storage-name callback always wins.
		return s.StorageNameFunc()
	}
	if s.Definition != nil && strings.TrimSpace(s.Definition.Table) != "" {
		// Reuse the schema definition table when one is already normalized.
		return s.Definition.Table
	}
	if strings.TrimSpace(s.Name) != "" {
		// Otherwise derive the SQL table from the logical model name.
		return canonicalModelTableName(s.Name)
	}
	return ""
}

type FieldMetadata struct {
	Name           string            `json:"name"`
	GoName         string            `json:"go_name"`
	Type           string            `json:"type"`
	Kind           FieldKind         `json:"kind,omitempty"`
	Label          string            `json:"label,omitempty"`
	Help           string            `json:"help,omitempty"`
	Default        string            `json:"default,omitempty"`
	BSONName       string            `json:"bson_name,omitempty"`
	JSONName       string            `json:"json_name,omitempty"`
	BunColumn      string            `json:"bun_column,omitempty"`
	FilterKey      string            `json:"filter_key,omitempty"`
	PrimaryKey     bool              `json:"primary_key"`
	Required       bool              `json:"required"`
	ReadOnly       bool              `json:"read_only,omitempty"`
	Secret         bool              `json:"secret,omitempty"` // mirrors FieldDefinition.Secret (plan T19)
	Stored         bool              `json:"stored,omitempty"`
	Unique         bool              `json:"unique,omitempty"`
	Indexed        bool              `json:"indexed,omitempty"`
	Relation       string            `json:"relation,omitempty"`
	Inverse        string            `json:"inverse,omitempty"`
	RelationTable  string            `json:"relation_table,omitempty"`
	RelationColumn string            `json:"relation_column,omitempty"`
	RelationKey    string            `json:"relation_key,omitempty"`
	OnDelete       string            `json:"on_delete,omitempty"`
	Related        []string          `json:"related,omitempty"`
	Depends        []string          `json:"depends,omitempty"`
	Compute        string            `json:"compute,omitempty"`
	ComputeSudo    bool              `json:"compute_sudo,omitempty"`
	Selection      []SelectionOption `json:"selection,omitempty"`
	// Translate mirrors FieldDefinition.Translate (SRS-F-021): the storage
	// column is jsonb keyed by lang code and reads resolve against the
	// environment language with an "en" fallback.
	Translate bool `json:"translate,omitempty"`
	// UseSequence/SequenceCode mirror FieldDefinition: see runtime_sequence.go.
	UseSequence  bool   `json:"use_sequence,omitempty"`
	SequenceCode string `json:"sequence_code,omitempty"`
	// HasOnchange reports whether changing this field triggers a registered
	// `RegisterOnchange` handler on this model. It is computed live from the
	// runtime hook registry (see OnchangeTriggerField) when fields metadata is
	// served, not stored on the static struct-tag-derived definition, so it stays
	// correct regardless of package init order. Frontend widgets use it to decide
	// whether a field edit is worth an onchange round trip, without hardcoding
	// per-model/per-field lists client-side.
	HasOnchange bool `json:"has_onchange,omitempty"`
}

type ModelMetadata struct {
	Name            string                  `json:"name"`
	Label           string                  `json:"label"`
	Description     string                  `json:"description,omitempty"`
	DescriptionFile string                  `json:"description_file,omitempty"`
	RecName         string                  `json:"rec_name,omitempty"`
	DefaultOrder    string                  `json:"default_order,omitempty"`
	Persistence     string                  `json:"persistence,omitempty"`
	StorageBackend  StorageBackend          `json:"storage_backend"`
	StorageName     string                  `json:"storage_name"`
	GoType          string                  `json:"go_type"`
	Fields          []FieldMetadata         `json:"fields"`
	RuntimeMethods  []RuntimeMethodMetadata `json:"runtime_methods,omitempty"`
	RegisteredAt    time.Time               `json:"registered_at"`
	runtimeMethods  map[string]RuntimeMethod
	goReflectType   reflect.Type
	definition      *SchemaDefinition
	prepare         func(ctx context.Context, op WriteOp) error
}

type ModelRegistry struct {
	mu      sync.RWMutex
	byName  map[string]*ModelMetadata
	byType  map[string]*ModelMetadata
	ordered []string
}

// NewModelRegistry constructs and returns the model registry handled by this package.
func NewModelRegistry() *ModelRegistry {
	return &ModelRegistry{
		// Keep lookup maps separate so we can enforce uniqueness by logical name
		// and by backing Go type independently.
		byName: make(map[string]*ModelMetadata),
		byType: make(map[string]*ModelMetadata),
	}
}

var defaultModelRegistry = NewModelRegistry()

// DefaultModelRegistry selects the model registry handled by this package.
func DefaultModelRegistry() *ModelRegistry {
	return defaultModelRegistry
}

func RegisterModelType[T any](spec ModelSpec) (*ModelMetadata, error) {
	return defaultModelRegistry.Register(goTypeOf[T](), spec)
}

// LookupModel looks up the model handled by this package.
func LookupModel(name string) (*ModelMetadata, bool) {
	return defaultModelRegistry.Lookup(name)
}

func RegisteredModels() []ModelMetadata {
	return defaultModelRegistry.List()
}

// RuntimeModelRef resolves the metadata handle used by shared runtime
// repository helpers. It prefers the dispatcher-provided model (already the
// registered metadata), then the registry, and finally a name-only handle so
// helper dispatch and the test stub seam still key off the model name even
// before registry bootstrap. Executor lookup only needs Name, so the fallback
// never changes runtime behavior for registered models.
func RuntimeModelRef(name string, model *ModelMetadata) *ModelMetadata {
	// Prefer the model the runtime dispatcher already resolved for this call.
	if model != nil && strings.TrimSpace(model.Name) != "" {
		return model
	}
	// Fall back to the registry so helper calls outside a runtime method still
	// dispatch against the registered metadata.
	if meta, ok := LookupModel(strings.TrimSpace(name)); ok {
		return meta
	}
	return &ModelMetadata{Name: strings.TrimSpace(name)}
}

// Lookup looks up the lookup handled by this package.
func (r *ModelRegistry) Lookup(name string) (*ModelMetadata, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	meta, ok := r.byName[name]
	if !ok {
		return nil, false
	}

	// Return a clone so callers cannot mutate the registry's shared metadata.
	return cloneModelMetadata(meta), true
}

// LookupShared returns the registry's own metadata without cloning. The result
// is read-only: Register never mutates a published entry (it swaps in a new
// copy), so a shared pointer stays internally consistent, but callers must not
// write through it. Use Lookup when the metadata will be modified or handed to
// code that might modify it.
func (r *ModelRegistry) LookupShared(name string) (*ModelMetadata, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	meta, ok := r.byName[name]
	return meta, ok
}

// LookupSharedModel is LookupShared on the default registry.
func LookupSharedModel(name string) (*ModelMetadata, bool) {
	return defaultModelRegistry.LookupShared(name)
}

// List lists the list handled by this package.
func (r *ModelRegistry) List() []ModelMetadata {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]ModelMetadata, 0, len(r.ordered))
	for _, name := range r.ordered {
		meta := r.byName[name]
		if meta == nil {
			continue
		}
		// Preserve registration order in list output for deterministic exports and
		// stable admin UI ordering.
		out = append(out, *cloneModelMetadata(meta))
	}
	return out
}

// Register registers the register handled by this package.
func (r *ModelRegistry) Register(typ reflect.Type, spec ModelSpec) (*ModelMetadata, error) {
	if spec.Name == "" {
		log.Errorf("model name is required")
		return nil, fmt.Errorf("model name is required")
	}

	typ = dereferenceType(typ)
	if typ.Kind() != reflect.Struct {
		log.Errorf("model %q must be backed by a struct, got %s", spec.Name, typ.Kind())
		return nil, fmt.Errorf("model %q must be backed by a struct, got %s", spec.Name, typ.Kind())
	}

	meta := &ModelMetadata{
		// Capture the static identity and storage contract for the model up front;
		// field metadata is filled in immediately after.
		Name:            spec.Name,
		Label:           spec.Label,
		Description:     spec.Description,
		DescriptionFile: spec.DescriptionFile,
		RecName:         strings.TrimSpace(spec.RecName),
		DefaultOrder:    strings.TrimSpace(spec.DefaultOrder),
		Persistence:     strings.TrimSpace(spec.Persistence),
		StorageBackend:  StorageBackendBun,
		StorageName:     spec.ResolveStorageName(),
		GoType:          typ.PkgPath() + "." + typ.Name(),
		RegisteredAt:    time.Now().UTC(),
		goReflectType:   typ,
		prepare:         spec.Prepare,
	}
	if spec.Definition != nil {
		// Definition-backed registration uses the richer Bun/meta metadata model.
		meta.definition = spec.Definition.Clone()
		meta.Fields = collectDefinitionFieldMetadata(typ, spec.Definition, meta.StorageName)
		if meta.Label == "" {
			meta.Label = spec.Definition.Label
		}
		if meta.Description == "" {
			meta.Description = spec.Definition.Description
		}
	} else {
		// Plain struct registration falls back to lightweight field extraction.
		meta.Fields = collectFieldMetadata(typ)
	}
	// Fill in display-name, ordering, and persistence defaults before the model
	// becomes visible to runtime callers.
	meta.applyRuntimeDefaults()

	// Standard hooks override the built-in runtime surface first, then
	// explicit RuntimeMethods can still replace either a default or a standard
	// hook when a model needs full control over one method declaration.
	declaredRuntimeMethods := append(spec.StandardHooks.RuntimeMethods(), spec.RuntimeMethods...)
	runtimeMethods, runtimeMethodMetadata, err := mergeRuntimeMethods(declaredRuntimeMethods)
	if err != nil {
		return nil, err
	}
	meta.RuntimeMethods = runtimeMethodMetadata
	meta.runtimeMethods = runtimeMethods

	typeKey := meta.GoType

	r.mu.Lock()
	defer r.mu.Unlock()

	if existing := r.byName[spec.Name]; existing != nil {
		if existing.GoType != typeKey {
			log.Errorf("model %q already registered for %s", spec.Name, existing.GoType)
			return nil, fmt.Errorf("model %q already registered for %s", spec.Name, existing.GoType)
		}
		// Publish a refreshed copy instead of editing in place: LookupShared
		// hands out the stored pointer without locking, so a published entry
		// must stay immutable.
		next := *existing
		if meta.StorageName != "" {
			next.StorageName = meta.StorageName
		}
		if meta.Label != "" {
			next.Label = meta.Label
		}
		if meta.Description != "" {
			next.Description = meta.Description
		}
		if meta.DescriptionFile != "" {
			next.DescriptionFile = meta.DescriptionFile
		}
		if meta.RecName != "" {
			next.RecName = meta.RecName
		}
		if meta.DefaultOrder != "" {
			next.DefaultOrder = meta.DefaultOrder
		}
		if meta.Persistence != "" {
			next.Persistence = meta.Persistence
		}
		next.StorageBackend = meta.StorageBackend
		next.Fields = meta.Fields
		next.RuntimeMethods = meta.RuntimeMethods
		next.RegisteredAt = meta.RegisteredAt
		next.runtimeMethods = meta.runtimeMethods
		next.goReflectType = meta.goReflectType
		next.definition = meta.definition
		next.prepare = meta.prepare
		r.byName[spec.Name] = &next
		r.byType[typeKey] = &next
		return cloneModelMetadata(&next), nil
	}

	if existing := r.byType[typeKey]; existing != nil && existing.Name != spec.Name {
		log.Errorf("go type %s already registered as %q", typeKey, existing.Name)
		return nil, fmt.Errorf("go type %s already registered as %q", typeKey, existing.Name)
	}

	// First registration for this model name and Go type becomes authoritative.
	r.byName[spec.Name] = meta
	r.byType[typeKey] = meta
	r.ordered = append(r.ordered, spec.Name)
	return cloneModelMetadata(meta), nil
}

// Unregister removes one model from the registry. It exists as the rollback
// path for PublishModel: if a dynamic model registers successfully but
// physical-table provisioning then fails, the registration must not linger
// in memory pointing at a table that was never created.
func (r *ModelRegistry) Unregister(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	meta := r.byName[name]
	if meta == nil {
		return
	}
	delete(r.byName, name)
	delete(r.byType, meta.GoType)
	for i, existing := range r.ordered {
		if existing == name {
			r.ordered = append(r.ordered[:i], r.ordered[i+1:]...)
			break
		}
	}
}

// LookupRuntimeMethod looks up the runtime method handled by this package.
func (m *ModelMetadata) LookupRuntimeMethod(name string) (RuntimeMethod, bool) {
	if m == nil {
		return RuntimeMethod{}, false
	}
	method, ok := m.runtimeMethods[strings.TrimSpace(name)]
	return method, ok
}

// cloneModelMetadata clones the model metadata handled by this package.
func cloneModelMetadata(meta *ModelMetadata) *ModelMetadata {
	if meta == nil {
		return nil
	}

	// Start with a shallow copy, then deep-copy the slices and maps that hold
	// mutable metadata.
	clone := *meta
	clone.Fields = make([]FieldMetadata, 0, len(meta.Fields))
	for _, field := range meta.Fields {
		fieldCopy := field
		// Clone nested slices so relation and selection metadata stays isolated.
		fieldCopy.Related = append([]string(nil), field.Related...)
		fieldCopy.Depends = append([]string(nil), field.Depends...)
		fieldCopy.Selection = append([]SelectionOption(nil), field.Selection...)
		clone.Fields = append(clone.Fields, fieldCopy)
	}
	clone.RuntimeMethods = append([]RuntimeMethodMetadata(nil), meta.RuntimeMethods...)
	clone.runtimeMethods = cloneRuntimeMethodMap(meta.runtimeMethods)
	clone.goReflectType = meta.goReflectType
	if meta.definition != nil {
		clone.definition = meta.definition.Clone()
	}
	return &clone
}

// applyRuntimeDefaults fills runtime-only metadata that higher-level methods
// depend on for display names, ordering, and persistence behavior.
func (m *ModelMetadata) applyRuntimeDefaults() {
	if m == nil {
		return
	}
	if strings.TrimSpace(m.Persistence) == "" {
		// Most registered models behave like normal persisted models unless
		// explicitly marked otherwise.
		m.Persistence = "model"
	}
	if strings.TrimSpace(m.RecName) == "" {
		// Probe common display-name candidates without logging on misses because
		// many models intentionally expose only one of them, or none at all.
		for _, candidate := range []string{"name", "display_name", "title", "key"} {
			if _, ok := lookupFieldSilently(m, candidate); ok {
				m.RecName = candidate
				break
			}
		}
	}
	if strings.TrimSpace(m.RecName) == "" {
		// Fall back to the primary id when no better human-readable field exists.
		m.RecName = "id"
	}
	if strings.TrimSpace(m.DefaultOrder) == "" {
		// Default ordering by id keeps search/read results deterministic.
		if _, err := m.Field("id"); err == nil {
			m.DefaultOrder = "id asc"
		}
	}
}

// Field resolves one logical field name against the registered metadata.
func (m *ModelMetadata) Field(name string) (FieldMetadata, error) {
	if m == nil {
		log.Errorf("orm: model metadata is required")
		return FieldMetadata{}, fmt.Errorf("orm: model metadata is required")
	}
	for _, field := range m.Fields {
		// Accept logical, Go, JSON, and Bun column names so different call sites
		// can resolve fields without manual translation.
		if name == field.Name || name == field.GoName || name == field.JSONName || name == field.BunColumn {
			return field, nil
		}
	}
	log.Errorf("orm: field %q is not registered on model %q", name, m.Name)
	return FieldMetadata{}, fmt.Errorf("orm: field %q is not registered on model %q", name, m.Name)
}

// lookupFieldSilently supports metadata bootstrap paths that need to probe for
// optional conventional fields without treating each miss as an operator-facing
// error.
func lookupFieldSilently(model *ModelMetadata, name string) (FieldMetadata, bool) {
	if model == nil {
		return FieldMetadata{}, false
	}
	for _, field := range model.Fields {
		if name == field.Name || name == field.GoName || name == field.JSONName || name == field.BunColumn {
			return field, true
		}
	}
	return FieldMetadata{}, false
}

// FieldNames returns deterministic logical field names.
func (m *ModelMetadata) FieldNames() []string {
	if m == nil {
		return nil
	}
	names := make([]string, 0, len(m.Fields))
	for _, field := range m.Fields {
		names = append(names, field.Name)
	}
	return names
}

// Definition returns a clone of the registered schema definition when one exists.
func (m *ModelMetadata) Definition() *SchemaDefinition {
	if m == nil || m.definition == nil {
		return nil
	}
	return m.definition.Clone()
}

func goTypeOf[T any]() reflect.Type {
	// Use a typed nil pointer to obtain the generic type without allocating.
	var ptr *T
	return dereferenceType(reflect.TypeOf(ptr).Elem())
}

func dereferenceType(typ reflect.Type) reflect.Type {
	// Keep peeling pointers until we reach the concrete struct type.
	for typ != nil && typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	return typ
}

// collectFieldMetadata collects the field metadata handled by this package.
func collectFieldMetadata(typ reflect.Type) []FieldMetadata {
	fields := make([]FieldMetadata, 0, typ.NumField())

	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if field.Name == "_" {
			// Ignore placeholder blank fields used only for tags or padding.
			continue
		}

		if field.Anonymous {
			embedded := dereferenceType(field.Type)
			if embedded.Kind() == reflect.Struct {
				// Flatten embedded structs so callers see one logical field list.
				fields = append(fields, collectFieldMetadata(embedded)...)
			}
			continue
		}

		if !field.IsExported() {
			continue
		}

		bsonName, _ := parseStructTag(field.Tag.Get("bson"))
		jsonName, _ := parseStructTag(field.Tag.Get("json"))
		bunOptions := parseBunTagOptions(field.Tag.Get("bun"))
		if bunOptions["__name__"] == "-" || hasTagOption(bunOptions, "rel") || hasTagOption(bunOptions, "m2m") {
			continue
		}
		bunName := strings.TrimSpace(bunOptions["__name__"])
		if bunName == "" {
			bunName = snakeCaseIdentifier(field.Name)
		}
		metaOptions := parseFieldMetaOptions(field)

		name := firstNonEmpty(bsonName, jsonName, bunName, snakeCaseIdentifier(field.Name))
		// Project the Go struct field into lightweight runtime metadata derived
		// mostly from storage tags.
		fields = append(fields, FieldMetadata{
			Name:       name,
			GoName:     field.Name,
			Type:       dereferenceType(field.Type).String(),
			Label:      firstNonEmpty(metaOptions["label"], metaOptions["name"], field.Name),
			Help:       strings.TrimSpace(metaOptions["help"]),
			Default:    implicitJSONBDefault(structFieldJSONBDefault(field, bunOptions), field, FieldKindJSON),
			BSONName:   bsonName,
			JSONName:   jsonName,
			BunColumn:  bunName,
			FilterKey:  resolveFilterKey(name, metaOptions),
			PrimaryKey: parseBoolOption(bunOptions, "pk"),
			Required:   parseBoolOption(bunOptions, "notnull"),
		})
	}
	return fields
}

// collectDefinitionFieldMetadata collects the definition field metadata handled by this package.
func collectDefinitionFieldMetadata(typ reflect.Type, definition *SchemaDefinition, storageName string) []FieldMetadata {
	if definition == nil {
		return collectFieldMetadata(typ)
	}

	// Work on a clone because we may override the storage table for this
	// specific registration.
	working := definition.Clone()
	if working == nil {
		return collectFieldMetadata(typ)
	}
	if storageName != "" {
		working.Table = storageName
		working.Normalize()
	}

	fields := make([]FieldMetadata, 0, len(working.Fields))
	appendDefinitionFieldMetadata(&fields, typ, working)
	appendDefinitionOnlyRuntimeFields(&fields, working)
	return fields
}

func appendDefinitionFieldMetadata(fields *[]FieldMetadata, typ reflect.Type, definition *SchemaDefinition) {
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if field.Name == "_" {
			continue
		}

		if field.Anonymous {
			embedded := dereferenceType(field.Type)
			if embedded.Kind() == reflect.Struct {
				// Flatten embedded definitions in the same order as the Go struct.
				appendDefinitionFieldMetadata(fields, embedded, definition)
			}
			continue
		}
		if !field.IsExported() {
			continue
		}

		definitionField, skip, err := bunFieldDefinition(field)
		if err != nil || skip || definitionField == nil {
			continue
		}

		bsonName, _ := parseStructTag(field.Tag.Get("bson"))
		jsonName, _ := parseStructTag(field.Tag.Get("json"))
		bunName := strings.TrimSpace(parseBunTagOptions(field.Tag.Get("bun"))["__name__"])
		metaOptions := parseFieldMetaOptions(field)

		resolvedField := resolveDefinitionFieldForStruct(definition, definitionField.Name, jsonName, bsonName, bunName)
		if resolvedField == nil {
			// Fall back to the directly parsed field when the definition lookup
			// misses, which keeps registration tolerant of partial definitions.
			resolvedField = definitionField
		}

		name := firstNonEmpty(bsonName, jsonName, bunName, resolvedField.Name, snakeCaseIdentifier(field.Name))
		// Merge the rich schema definition with tag-derived transport names from
		// the concrete Go struct.
		*fields = append(*fields, FieldMetadata{
			Name:           name,
			GoName:         field.Name,
			Type:           dereferenceType(field.Type).String(),
			Kind:           resolvedField.Kind,
			Label:          firstNonEmpty(resolvedField.Label, metaOptions["label"], metaOptions["name"], field.Name),
			Help:           firstNonEmpty(resolvedField.Help, metaOptions["help"]),
			Default:        implicitJSONBDefault(structFieldJSONBDefault(field, parseBunTagOptions(field.Tag.Get("bun"))), field, resolvedField.Kind),
			BSONName:       bsonName,
			JSONName:       jsonName,
			BunColumn:      firstNonEmpty(bunName, resolvedField.Column),
			FilterKey:      resolveFilterKey(name, metaOptions),
			PrimaryKey:     resolvedField.IsPrimary,
			Required:       resolvedField.Required,
			ReadOnly:       resolvedField.ReadOnly,
			Secret:         resolvedField.Secret,
			Stored:         resolvedField.Stored,
			Unique:         resolvedField.Unique,
			Indexed:        resolvedField.Indexed,
			Relation:       resolvedField.Relation,
			Inverse:        resolvedField.Inverse,
			RelationTable:  resolvedField.RelationTable,
			RelationColumn: resolvedField.RelationColumn,
			RelationKey:    resolvedField.RelationKey,
			OnDelete:       resolvedField.OnDelete,
			Related:        append([]string(nil), resolvedField.Related...),
			Depends:        append([]string(nil), resolvedField.Depends...),
			Compute:        resolvedField.Compute,
			ComputeSudo:    resolvedField.ComputeSudo,
			Selection:      append([]SelectionOption(nil), resolvedField.Selection...),
			Translate:      resolvedField.Translate,
			UseSequence:    resolvedField.UseSequence,
			SequenceCode:   resolvedField.SequenceCode,
		})
	}
}

func resolveDefinitionFieldForStruct(definition *SchemaDefinition, candidates ...string) *FieldDefinition {
	if definition == nil {
		return nil
	}
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		if field, ok := definition.Fields[candidate]; ok && field != nil {
			return field
		}
	}
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		if field, err := definition.Field(candidate); err == nil && field != nil {
			return field
		}
	}
	return nil
}

func structFieldJSONBDefault(field reflect.StructField, bunOptions map[string]string) string {
	defaultValue := strings.TrimSpace(field.Tag.Get("default"))
	// Respect explicit field defaults first so model authors can override the
	// repo-wide JSONB object fallback on individual fields.
	if defaultValue != "" {
		return defaultValue
	}
	if !isJSONBColumn(bunOptions) {
		return ""
	}
	return implicitJSONBDefault("", field, FieldKindJSON)
}

func implicitJSONBDefault(defaultValue string, field reflect.StructField, kind FieldKind) string {
	if strings.TrimSpace(defaultValue) != "" {
		return strings.TrimSpace(defaultValue)
	}
	if kind != FieldKindJSON {
		return ""
	}
	// Keep JSONB defaults aligned with the Go container shape so runtime
	// default_get and DB-level defaults materialize values the decoder can scan
	// back into the target field type without shape mismatches.
	switch dereferenceType(field.Type).Kind() {
	case reflect.Map:
		return "{}"
	case reflect.Slice:
		return "[]"
	default:
		return ""
	}
}

func isJSONBColumn(bunOptions map[string]string) bool {
	return strings.EqualFold(strings.TrimSpace(bunOptions["type"]), "jsonb")
}

func appendDefinitionOnlyRuntimeFields(fields *[]FieldMetadata, definition *SchemaDefinition) {
	if definition == nil {
		return
	}
	seen := make(map[string]struct{}, len(*fields))
	for _, field := range *fields {
		seen[field.Name] = struct{}{}
	}
	// Walk each fieldnames() entry in order so validation and side effects stay scoped to the current element.
	for _, name := range definition.FieldNames() {
		if _, ok := seen[name]; ok {
			continue
		}
		definitionField, err := definition.Field(name)
		if err != nil || definitionField == nil {
			continue
		}
		// Only project definition-only runtime fields that do not require Bun to
		// select a missing physical column from the backing struct/table mapping.
		if definitionField.Stored && definitionField.Compute == "" && len(definitionField.Related) == 0 {
			continue
		}
		*fields = append(*fields, fieldMetadataFromDefinition(definitionField))
	}
}

func fieldMetadataFromDefinition(field *FieldDefinition) FieldMetadata {
	typeName := "string"
	switch field.Kind {
	case FieldKindID, FieldKindInteger:
		typeName = "int64"
	case FieldKindBoolean:
		typeName = "bool"
	case FieldKindFloat:
		typeName = "float64"
	case FieldKindJSON, FieldKindOneToMany, FieldKindManyToMany:
		typeName = "map[string]interface {}"
	}
	return FieldMetadata{
		Name:           field.Name,
		Type:           typeName,
		Kind:           field.Kind,
		Label:          field.Label,
		Help:           field.Help,
		BunColumn:      field.Column,
		FilterKey:      field.Name,
		PrimaryKey:     field.IsPrimary,
		Required:       field.Required,
		ReadOnly:       field.ReadOnly,
		Secret:         field.Secret,
		Stored:         field.Stored,
		Unique:         field.Unique,
		Indexed:        field.Indexed,
		Relation:       field.Relation,
		Inverse:        field.Inverse,
		RelationTable:  field.RelationTable,
		RelationColumn: field.RelationColumn,
		RelationKey:    field.RelationKey,
		OnDelete:       field.OnDelete,
		Related:        append([]string(nil), field.Related...),
		Depends:        append([]string(nil), field.Depends...),
		Compute:        field.Compute,
		ComputeSudo:    field.ComputeSudo,
		Selection:      append([]SelectionOption(nil), field.Selection...),
		Translate:      field.Translate,
		UseSequence:    field.UseSequence,
		SequenceCode:   field.SequenceCode,
	}
}

// parseStructTag parses the struct tag handled by this package.
func parseStructTag(tag string) (string, map[string]bool) {
	opts := make(map[string]bool)
	if tag == "" {
		return "", opts
	}

	// The first segment is the tag name; the rest are boolean-style options.
	parts := strings.Split(tag, ",")
	name := strings.TrimSpace(parts[0])
	// Walk each parts[1: entry in order so validation and side effects stay scoped to the current element.
	for _, part := range parts[1:] {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		opts[part] = true
	}

	if name == "-" {
		return "", opts
	}
	return name, opts
}
