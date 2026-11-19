package orm

// File overview:
// - Responsibility: implements the metadata-driven ORM/runtime kernel for model schema, where storage, runtime dispatch, and security rules stay centralized.
// - Key symbols: `NewField`, `TextField`, `FieldKind`, `SelectionOption`.
// - Pattern: Leans on metadata-driven registration so storage mapping, runtime methods, and behavior hooks stay centralized instead of being scattered across handlers.

import (
	"fmt"
	"git.thebase.vn/base_hub/internal/services/log"
	"sort"
	"strings"
)

// FieldKind captures the coarse runtime field family used by base-tagged
// record definitions.
type FieldKind string

const (
	FieldKindID         FieldKind = "id"
	FieldKindBoolean    FieldKind = "boolean"
	FieldKindInteger    FieldKind = "integer"
	FieldKindFloat      FieldKind = "float"
	FieldKindText       FieldKind = "text"
	FieldKindHTML       FieldKind = "html"
	FieldKindSelection  FieldKind = "selection"
	FieldKindJSON       FieldKind = "json"
	FieldKindDate       FieldKind = "date"
	FieldKindDateTime   FieldKind = "datetime"
	FieldKindOneToOne   FieldKind = "one2one"
	FieldKindManyToOne  FieldKind = "many2one"
	FieldKindOneToMany  FieldKind = "one2many"
	FieldKindManyToMany FieldKind = "many2many"
)

// SelectionOption stores one allowed selection value plus its display label.
type SelectionOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// FieldDefinition keeps the richer field metadata produced by Bun/meta-tagged
// record authoring.
type FieldDefinition struct {
	Name           string            `json:"name"`
	Label          string            `json:"label,omitempty"`
	Help           string            `json:"help,omitempty"`
	Column         string            `json:"column"`
	Kind           FieldKind         `json:"kind"`
	Relation       string            `json:"relation,omitempty"`
	Inverse        string            `json:"inverse,omitempty"`
	RelationTable  string            `json:"relation_table,omitempty"`
	RelationColumn string            `json:"relation_column,omitempty"`
	RelationKey    string            `json:"relation_key,omitempty"`
	OnDelete       string            `json:"on_delete,omitempty"`
	Required       bool              `json:"required"`
	ReadOnly       bool              `json:"read_only"`
	Secret         bool              `json:"secret,omitempty"` // credential-like column, see secret_fields.go (plan T19)
	Stored         bool              `json:"stored"`
	Unique         bool              `json:"unique"`
	Indexed        bool              `json:"indexed"`
	IsPrimary      bool              `json:"is_primary"`
	Related        []string          `json:"related,omitempty"`
	Depends        []string          `json:"depends,omitempty"`
	Compute        string            `json:"compute,omitempty"`
	ComputeSudo    bool              `json:"compute_sudo"`
	Selection      []SelectionOption `json:"selection,omitempty"`
	// Translate marks a scalar text field as language-scoped (SRS-F-021): the
	// storage column becomes jsonb keyed by lang code with "en" as the required
	// base key, and every read/search/order path resolves it against the
	// environment language with an "en" fallback.
	Translate bool `json:"translate,omitempty"`
	// UseSequence (meta `use_sequence`) fills this field from sys.sequence on create and previews it
	// in default_get; SequenceCode (meta `sequence_code`) overrides the default `<model>.<field>` code.
	UseSequence  bool   `json:"use_sequence,omitempty"`
	SequenceCode string `json:"sequence_code,omitempty"`
	State        string `json:"state,omitempty"`
}

// NewField builds one normalized field definition with stored=true by default.
func NewField(name, column string, kind FieldKind) *FieldDefinition {
	field := &FieldDefinition{
		Name:   name,
		Column: column,
		Kind:   kind,
		Stored: true,
	}
	field.Normalize()
	return field
}

// TextField is a convenience constructor for simple text columns.
func TextField(name string) *FieldDefinition {
	return NewField(name, name, FieldKindText)
}

// ModelKind distinguishes static compile-time models from dynamic user-authored
// models.
type ModelKind string

const (
	ModelKindStatic  ModelKind = "static"
	ModelKindDynamic ModelKind = "dynamic"
)

// SchemaDefinition stores the richer model metadata generated from a record
// struct authored with Bun/meta tags.
type SchemaDefinition struct {
	Name        string                      `json:"name"`
	Label       string                      `json:"label,omitempty"`
	Table       string                      `json:"table,omitempty"`
	Kind        ModelKind                   `json:"kind,omitempty"`
	State       string                      `json:"state,omitempty"`
	Description string                      `json:"description,omitempty"`
	IsSystem    bool                        `json:"is_system"`
	Inherit     bool                        `json:"inherit"`
	Fields      map[string]*FieldDefinition `json:"fields"`
}

// NewModel builds one normalized schema definition.
func NewModel(name string, fields ...*FieldDefinition) *SchemaDefinition {
	model := &SchemaDefinition{
		Name:   name,
		Fields: make(map[string]*FieldDefinition, len(fields)),
	}
	for _, field := range fields {
		if field == nil {
			continue
		}
		model.Fields[field.Name] = field
	}
	model.Normalize()
	return model
}

// Normalize fills in defaults for table and field metadata.
func (m *SchemaDefinition) Normalize() {
	if m == nil {
		return
	}
	if m.Fields == nil {
		m.Fields = make(map[string]*FieldDefinition)
	}
	if strings.TrimSpace(m.Label) == "" {
		m.Label = m.Name
	}
	if m.Kind == "" {
		m.Kind = ModelKindDynamic
	}
	if strings.TrimSpace(m.State) == "" {
		m.State = "active"
	}
	if strings.TrimSpace(m.Table) == "" && strings.TrimSpace(m.Name) != "" {
		m.Table = canonicalModelTableName(m.Name)
	}
	for name, field := range m.Fields {
		if field == nil {
			delete(m.Fields, name)
			continue
		}
		field.Normalize()
		if field.Kind == FieldKindManyToMany {
			applyManyToManyDefaults(m, field)
		}
	}
	// Validate the invariants for this stage before the function commits to the next step.
	ensurePrimaryIDField(m)
}

// Clone returns a deep copy so shared package-level definitions can be safely
// adjusted per registration.
func (m *SchemaDefinition) Clone() *SchemaDefinition {
	if m == nil {
		return nil
	}

	cloned := &SchemaDefinition{
		Name:        m.Name,
		Label:       m.Label,
		Table:       m.Table,
		Kind:        m.Kind,
		State:       m.State,
		Description: m.Description,
		IsSystem:    m.IsSystem,
		Inherit:     m.Inherit,
		Fields:      make(map[string]*FieldDefinition, len(m.Fields)),
	}

	for name, field := range m.Fields {
		if field == nil {
			continue
		}
		fieldCopy := *field
		fieldCopy.Related = append([]string(nil), field.Related...)
		fieldCopy.Depends = append([]string(nil), field.Depends...)
		fieldCopy.Selection = append([]SelectionOption(nil), field.Selection...)
		cloned.Fields[name] = &fieldCopy
	}

	cloned.Normalize()
	return cloned
}

// AddField inserts or replaces one field and reapplies relation defaults.
func (m *SchemaDefinition) AddField(field *FieldDefinition) {
	if m == nil || field == nil {
		return
	}
	m.Normalize()
	field.Normalize()
	if field.Kind == FieldKindManyToMany {
		applyManyToManyDefaults(m, field)
	}
	m.Fields[field.Name] = field
}

// Field returns one named field definition.
func (m *SchemaDefinition) Field(name string) (*FieldDefinition, error) {
	if m == nil {
		log.Errorf("orm: nil model definition")
		return nil, fmt.Errorf("orm: nil model definition")
	}
	field, ok := m.Fields[name]
	if !ok {
		log.Errorf("orm: field %s.%s not found", m.Name, name)
		return nil, fmt.Errorf("orm: field %s.%s not found", m.Name, name)
	}
	return field, nil
}

// FieldNames returns field names in deterministic order for tests and metadata
// serialization.
func (m *SchemaDefinition) FieldNames() []string {
	if m == nil {
		return nil
	}
	names := make([]string, 0, len(m.Fields))
	for name := range m.Fields {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Normalize keeps one field definition internally consistent with the
// Bun/meta-tagged authoring contract.
func (f *FieldDefinition) Normalize() {
	if f == nil {
		return
	}
	if strings.TrimSpace(f.Name) == "" && strings.TrimSpace(f.Column) != "" {
		// Prefer the explicit storage column as the logical name when callers
		// omitted Name in the authoring helper.
		f.Name = f.Column
	}
	if strings.TrimSpace(f.Label) == "" {
		// Default the UI label to the logical field name.
		f.Label = f.Name
	}
	if strings.TrimSpace(f.Column) == "" && strings.TrimSpace(f.Name) != "" {
		// Keep the storage column aligned with the logical name by default.
		f.Column = f.Name
	}
	if f.Kind == "" {
		// Text is the safest default for untyped authoring helpers.
		f.Kind = FieldKindText
	}
	if strings.TrimSpace(f.State) == "" {
		// Fields are active unless explicitly retired from metadata.
		f.State = "active"
	}
	if (f.Kind == FieldKindManyToOne || f.Kind == FieldKindOneToOne) && f.Column == f.Name && !strings.HasSuffix(f.Column, "_id") {
		// Many2one storage columns follow the conventional *_id suffix.
		f.Column = f.Name + "_id"
	}
	if f.Kind == FieldKindManyToOne || f.Kind == FieldKindOneToOne {
		// Normalize the delete action against required/optional semantics.
		f.OnDelete = normalizeOnDelete(f.OnDelete, f.Required)
		if f.Kind == FieldKindOneToOne {
			// Keep one-to-one declarations honest at the storage level.
			f.Unique = true
		}
	} else {
		// Non-many2one fields do not carry on-delete semantics.
		f.OnDelete = ""
	}
	if f.Kind == FieldKindOneToMany || f.Kind == FieldKindManyToMany {
		// Collection relations are computed from relational joins rather than
		// stored directly on the owning table.
		f.Stored = false
	}
	if len(f.Selection) > 0 {
		// Deduplicate selection values while preserving the first declaration.
		normalized := make([]SelectionOption, 0, len(f.Selection))
		seen := make(map[string]struct{}, len(f.Selection))
		for _, option := range f.Selection {
			if strings.TrimSpace(option.Value) == "" {
				// Ignore incomplete options with no stored value.
				continue
			}
			if _, ok := seen[option.Value]; ok {
				// Keep the first label for duplicate values.
				continue
			}
			seen[option.Value] = struct{}{}
			if strings.TrimSpace(option.Label) == "" {
				// Fall back to the stored value when no display label is supplied.
				option.Label = option.Value
			}
			normalized = append(normalized, option)
		}
		f.Selection = normalized
		if f.Kind == FieldKindText {
			// Promote plain text fields with enumerated values into selection type.
			f.Kind = FieldKindSelection
		}
	}
}

// normalizeOnDelete preserves many2one delete semantics compatible with the
// required flag.
func normalizeOnDelete(raw string, required bool) string {
	// Compare against an upper-cased canonical form so authoring tags can use
	// any case without affecting behavior.
	value := strings.ToUpper(strings.TrimSpace(raw))
	switch value {
	case "", "SET NULL":
		if required {
			return "RESTRICT"
		}
		return "SET NULL"
	case "CASCADE", "RESTRICT", "SET DEFAULT", "NO ACTION":
		return value
	default:
		if required {
			return "RESTRICT"
		}
		return "SET NULL"
	}
}

// ensurePrimaryIDField guarantees a primary id field even when the authoring
// struct omits it.
func ensurePrimaryIDField(model *SchemaDefinition) {
	if model == nil {
		return
	}
	idField, ok := model.Fields["id"]
	if !ok || idField == nil {
		// Create the synthetic id definition when authoring omitted it.
		idField = NewField("id", "id", FieldKindID)
		model.Fields["id"] = idField
	}
	// Force the canonical primary-key contract even if the incoming field was
	// only partially specified.
	idField.Name = "id"
	idField.Column = "id"
	idField.Kind = FieldKindID
	idField.IsPrimary = true
	idField.Required = true
	idField.Stored = true
}

// applyManyToManyDefaults mirrors the relation-table defaults from the sample
// runtime so authoring tags can stay concise.
func applyManyToManyDefaults(model *SchemaDefinition, field *FieldDefinition) {
	if model == nil || field == nil || strings.TrimSpace(field.Relation) == "" {
		return
	}

	// Derive the relation-table names from both sides so defaults stay stable
	// across registrations.
	leftTable := model.Table
	rightTable := strings.ReplaceAll(field.Relation, ".", "_")
	if strings.TrimSpace(field.RelationTable) == "" {
		switch {
		case leftTable == rightTable:
			field.RelationTable = leftTable + "_" + field.Name + "_rel"
		case leftTable < rightTable:
			field.RelationTable = leftTable + "_" + rightTable + "_rel"
		default:
			field.RelationTable = rightTable + "_" + leftTable + "_rel"
		}
	}
	if strings.TrimSpace(field.RelationColumn) == "" {
		// The left side always points back to the owning model table.
		field.RelationColumn = leftTable + "_id"
	}
	if strings.TrimSpace(field.RelationKey) == "" {
		// Self-relations need the field name to avoid key-name collisions.
		if leftTable == rightTable {
			field.RelationKey = field.Name + "_id"
		} else {
			field.RelationKey = rightTable + "_id"
		}
	}
}

// canonicalModelTableName applies the package-wide naming rule for derived SQL
// tables by replacing each model-name separator with a single underscore.
func canonicalModelTableName(modelName string) string {
	modelName = strings.TrimSpace(modelName)
	if modelName == "" {
		return ""
	}
	return strings.ReplaceAll(modelName, ".", "_")
}
