package orm

// File overview:
// - Responsibility: implements the metadata-driven ORM/runtime kernel for model definition, where storage, runtime dispatch, and security rules stay centralized.
// - Key symbols: `ModelDefinition`, `ModelDefinitionFromRecord`.
// - Pattern: Leans on metadata-driven registration so storage mapping, runtime methods, and behavior hooks stay centralized instead of being scattered across handlers.
// - Pattern: Uses Bun-facing model or repository patterns so schema mapping stays explicit and transaction-aware inside the backend data layer.

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"

	"git.thebase.vn/base_hub/internal/services/log"
	"github.com/uptrace/bun"
)

// ModelDefinition converts one Bun-tagged record struct into the richer schema
// definition used by definition-based model registration.
func ModelDefinition(modelName string, model any) *SchemaDefinition {
	parsed, err := ModelDefinitionFromRecord(modelName, model)
	if err != nil {
		panic(err)
	}
	return parsed
}

// ModelDefinitionFromRecord reflects one record struct into a schema
// definition and validates the supported Bun/meta contract.
func ModelDefinitionFromRecord(modelName string, model any) (*SchemaDefinition, error) {
	// Resolve the concrete struct type first so later validation can report
	// stable type names even when callers pass pointers.
	modelType, err := modelRecordType(model)
	if err != nil {
		return nil, err
	}

	// Trim the logical model name because it becomes the canonical registry key.
	modelName = strings.TrimSpace(modelName)
	if modelName == "" {
		log.Errorf("orm: %s requires a non-empty model name", modelType.String())
		return nil, fmt.Errorf("orm: %s requires a non-empty model name", modelType.String())
	}

	// Start from a normalized schema definition so field extraction only needs
	// to fill in deltas from the authoring struct.
	parsed := NewModel(modelName)
	parsed.Kind = ModelKindStatic
	parsed.IsSystem = true
	// Walk the struct metadata and project each supported tag into schema state.
	if err := applyStructMetadata(parsed, modelType); err != nil {
		return nil, err
	}
	return parsed, nil
}

// applyStructMetadata walks the authoring struct and expands Bun-tagged fields
// into schema metadata.
func applyStructMetadata(model *SchemaDefinition, modelType reflect.Type) error {
	return applyStructFields(model, modelType)
}

// applyStructFields recursively expands embedded authoring structs so
// reusable field groups can be composed without copy-pasting.
func applyStructFields(model *SchemaDefinition, modelType reflect.Type) error {
	for i := 0; i < modelType.NumField(); i++ {
		// Inspect fields in declaration order so embedded fragments keep their
		// natural precedence and field index stability.
		structField := modelType.Field(i)

		if structField.Anonymous && structField.Type == reflect.TypeOf(bun.BaseModel{}) {
			// Bun's embedded BaseModel can carry table metadata even though it
			// does not map to a runtime field definition.
			if err := applyBunModelOptions(model, structField.Tag.Get("bun")); err != nil {
				return err
			}
			continue
		}
		if isComposedAuthoringField(structField) {
			// Recurse into exported embedded structs so reusable field groups are
			// flattened into the owning model definition.
			if err := applyStructFields(model, indirectType(structField.Type)); err != nil {
				return err
			}
			continue
		}
		if structField.PkgPath != "" {
			// Skip unexported fields because they are implementation detail and
			// should not leak into persisted schema metadata.
			continue
		}

		// Convert the Bun-tagged field into runtime metadata or report why it
		// cannot be represented in the definition contract.
		fieldDef, skip, err := bunFieldDefinition(structField)
		if err != nil {
			return err
		}
		if skip || fieldDef == nil {
			continue
		}
		// AddField reapplies normalization so relation defaults remain coherent.
		model.AddField(fieldDef)
	}
	return nil
}

// isComposedAuthoringField detects anonymous exported structs that should be
// flattened into the current definition.
func isComposedAuthoringField(structField reflect.StructField) bool {
	return structField.Anonymous &&
		structField.PkgPath == "" &&
		indirectType(structField.Type).Kind() == reflect.Struct
}

// applyBunModelOptions reads optional Bun model metadata such as an explicit
// table name from an embedded bun.BaseModel tag.
func applyBunModelOptions(model *SchemaDefinition, bunTag string) error {
	if strings.TrimSpace(bunTag) == "" {
		return nil
	}

	options := parseBunTagOptions(bunTag)
	if tableName := strings.TrimSpace(options["table"]); tableName != "" {
		// Respect an explicit Bun table mapping so logical model names can point
		// at legacy storage tables without forcing the struct name to match.
		model.Table = tableName
	}
	return nil
}

// bunFieldDefinition maps one struct field into one schema field definition.
func bunFieldDefinition(structField reflect.StructField) (*FieldDefinition, bool, error) {
	columnName, bunFlags, skip := parseBunFieldTag(structField)
	if skip {
		// Respect Bun's explicit opt-out marker for transient struct members.
		return nil, true, nil
	}
	if hasTagOption(bunFlags, "rel") || hasTagOption(bunFlags, "m2m") {
		// Bun-native relation helper fields stay available to Bun itself but do
		// not become standalone runtime metadata fields.
		return nil, true, nil
	}

	// Bun tags decide the storage column while meta tags carry richer runtime
	// metadata such as labels, compute hooks, and relation hints.
	metaOptions := parseFieldMetaOptions(structField)

	// Infer the runtime field kind before constructing the final definition.
	kind, err := bunFieldKind(structField, bunFlags, metaOptions)
	if err != nil {
		log.Errorf("orm: %s: %v", structField.Name, err)
		return nil, false, fmt.Errorf("orm: %s: %w", structField.Name, err)
	}

	fieldName := columnName
	if fieldName == "" {
		// Fall back to snake_case so unnamed Bun tags still produce stable
		// column and field names.
		fieldName = snakeCaseIdentifier(structField.Name)
	}

	// Seed the field with Bun-derived storage identity, then layer optional
	// runtime semantics from the meta and selection tags.
	field := NewField(fieldName, fieldName, kind)
	field.Column = fieldName
	field.Name = fieldName
	field.Label = firstNonEmpty(metaOptions["name"], metaOptions["label"], fieldName)
	field.Help = strings.TrimSpace(metaOptions["help"])
	field.Required = parseBoolOption(bunFlags, "notnull") || parseBoolOption(metaOptions, "required")
	field.Unique = parseBoolOption(bunFlags, "unique") || parseBoolOption(metaOptions, "unique")
	field.Indexed = field.Unique || parseBoolOption(metaOptions, "index") || parseBoolOption(metaOptions, "indexed")
	field.ReadOnly = parseBoolOption(metaOptions, "readonly")
	field.Secret = parseBoolOption(metaOptions, "secret")
	field.Stored = parseStoredOption(kind, metaOptions)
	field.IsPrimary = parseBoolOption(bunFlags, "pk") || parseBoolOption(metaOptions, "primary")

	selection, err := parseSelectionTag(structField.Tag.Get("selection"))
	if err != nil {
		log.Errorf("orm: %s: %v", structField.Name, err)
		return nil, false, fmt.Errorf("orm: %s: %w", structField.Name, err)
	}
	if len(selection) > 0 {
		// Selection metadata is only valid for text-backed persisted values.
		if field.Kind != FieldKindText && field.Kind != FieldKindSelection {
			log.Errorf("selection tag is only supported on text-backed fields")
			return nil, false, fmt.Errorf("selection tag is only supported on text-backed fields")
		}
		field.Selection = selection
		field.Kind = FieldKindSelection
	}

	if relationModel := strings.TrimSpace(metaOptions["model"]); relationModel != "" {
		field.Relation = relationModel
	}
	if inverse := strings.TrimSpace(metaOptions["inverse"]); inverse != "" {
		field.Inverse = inverse
	}
	if relationTable := strings.TrimSpace(metaOptions["relation_table"]); relationTable != "" {
		field.RelationTable = relationTable
	}
	if relationColumn := strings.TrimSpace(metaOptions["relation_column"]); relationColumn != "" {
		field.RelationColumn = relationColumn
	}
	if relationKey := strings.TrimSpace(metaOptions["relation_key"]); relationKey != "" {
		field.RelationKey = relationKey
	}
	if onDelete := strings.TrimSpace(metaOptions["ondelete"]); onDelete != "" {
		field.OnDelete = onDelete
	}
	if related := strings.TrimSpace(metaOptions["related"]); related != "" {
		field.Related = splitNonEmpty(related, ".")
	}
	if depends := strings.TrimSpace(metaOptions["depends"]); depends != "" {
		field.Depends = splitNonEmpty(depends, "|")
	}
	if compute := strings.TrimSpace(metaOptions["compute"]); compute != "" {
		field.Compute = compute
		// Computed fields are read-only through RPC (SRS-F-018): their values come
		// from registered handlers, so metadata must surface readonly=true or SDUI
		// forms would offer edits the runtime is required to reject.
		field.ReadOnly = true
	}
	field.ComputeSudo = parseBoolOption(metaOptions, "compute_sudo")
	field.Translate = parseBoolOption(metaOptions, "translate")
	field.UseSequence = parseBoolOption(metaOptions, "use_sequence")
	field.SequenceCode = strings.TrimSpace(metaOptions["sequence_code"])
	// translate:true is only valid on plain stored scalar text fields
	// (SRS-F-021): computed, related, relation, and selection fields have no
	// per-language storage contract, so misuse must fail model registration
	// (boot) instead of surfacing as broken jsonb SQL at request time.
	if field.Translate {
		if field.Kind != FieldKindText {
			log.Errorf("orm: %s: translate:true requires a scalar text field, got kind %q", structField.Name, field.Kind)
			return nil, false, fmt.Errorf("orm: %s: translate:true requires a scalar text field, got kind %q", structField.Name, field.Kind)
		}
		if field.Compute != "" || len(field.Related) > 0 || field.Relation != "" {
			log.Errorf("orm: %s: translate:true is not allowed on computed, related, or relation fields", structField.Name)
			return nil, false, fmt.Errorf("orm: %s: translate:true is not allowed on computed, related, or relation fields", structField.Name)
		}
		if !field.Stored {
			log.Errorf("orm: %s: translate:true requires a stored field", structField.Name)
			return nil, false, fmt.Errorf("orm: %s: translate:true requires a stored field", structField.Name)
		}
	}
	// Normalize once at the end so relation defaults and selection cleanup see
	// the full field payload.
	field.Normalize()
	return field, false, nil
}

// parseBunFieldTag extracts the Bun column name and any flag-style modifiers.
func parseBunFieldTag(structField reflect.StructField) (string, fieldTagOptions, bool) {
	options := parseBunTagOptions(structField.Tag.Get("bun"))
	if options["__name__"] == "-" {
		return "", nil, true
	}
	columnName := strings.TrimSpace(options["__name__"])
	if columnName == "" && !hasTagOption(options, "rel") && !hasTagOption(options, "m2m") {
		columnName = snakeCaseIdentifier(structField.Name)
	}
	return columnName, options, false
}

// bunFieldKind infers the field kind from Go type information unless the meta
// tag explicitly overrides it.
func bunFieldKind(structField reflect.StructField, bunFlags fieldTagOptions, metaOptions fieldTagOptions) (FieldKind, error) {
	if kindOverride := resolveFieldKindOption(metaOptions); kindOverride != "" {
		switch FieldKind(kindOverride) {
		case FieldKindID, FieldKindBoolean, FieldKindInteger, FieldKindFloat, FieldKindText, FieldKindHTML, FieldKindSelection, FieldKindJSON, FieldKindDate, FieldKindDateTime, FieldKindOneToOne, FieldKindManyToOne, FieldKindOneToMany, FieldKindManyToMany:
			return FieldKind(kindOverride), nil
		default:
			log.Errorf("unsupported field kind override %q", kindOverride)
			return "", fmt.Errorf("unsupported field kind override %q", kindOverride)
		}
	}

	fieldType := indirectType(structField.Type)
	switch {
	case parseBoolOption(bunFlags, "pk") && strings.EqualFold(structField.Name, "ID"):
		return FieldKindID, nil
	case fieldType == reflect.TypeOf(time.Time{}) || fieldType == reflect.TypeOf(bun.NullTime{}):
		return FieldKindDateTime, nil
	case fieldType == reflect.TypeOf(json.RawMessage{}) || fieldType.Kind() == reflect.Map:
		return FieldKindJSON, nil
	case fieldType.Kind() == reflect.Bool:
		return FieldKindBoolean, nil
	case isIntegerKind(fieldType.Kind()):
		if strings.TrimSpace(metaOptions["model"]) != "" {
			log.Errorf("relation field requires explicit meta:\"kind:many2one|one2one,model:<model>\"")
			return "", fmt.Errorf("relation field requires explicit meta:\"kind:many2one|one2one,model:<model>\"")
		}
		if parseBoolOption(bunFlags, "pk") {
			return FieldKindID, nil
		}
		return FieldKindInteger, nil
	case isFloatKind(fieldType.Kind()):
		return FieldKindFloat, nil
	case fieldType.Kind() == reflect.String:
		return FieldKindText, nil
	case fieldType.Kind() == reflect.Slice:
		log.Errorf("slice fields require meta:\"kind:json|one2many|many2many,model:<model>\"")
		return "", fmt.Errorf("slice fields require meta:\"kind:json|one2many|many2many,model:<model>\"")
	default:
		log.Errorf("unsupported field type %s", structField.Type.String())
		return "", fmt.Errorf("unsupported field type %s", structField.Type.String())
	}
}

// parseSelectionTag parses `(value,label)` selection metadata into structured
// options.
func parseSelectionTag(tag string) ([]SelectionOption, error) {
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return nil, nil
	}

	// Consume the tag left-to-right so malformed pairs can be reported close to
	// the exact segment that caused the failure.
	options := make([]SelectionOption, 0)
	for len(tag) > 0 {
		tag = strings.TrimSpace(tag)
		if !strings.HasPrefix(tag, "(") {
			log.Errorf("selection tag must contain comma-separated (value,label) pairs")
			return nil, fmt.Errorf("selection tag must contain comma-separated (value,label) pairs")
		}
		end := strings.Index(tag, ")")
		if end < 0 {
			log.Errorf("selection tag contains an unterminated pair")
			return nil, fmt.Errorf("selection tag contains an unterminated pair")
		}

		pair := strings.TrimSpace(tag[1:end])
		parts := strings.SplitN(pair, ",", 2)
		if len(parts) != 2 {
			log.Errorf("selection pair %q must contain value and label", pair)
			return nil, fmt.Errorf("selection pair %q must contain value and label", pair)
		}

		value := trimSelectionPart(parts[0])
		label := trimSelectionPart(parts[1])
		if value == "" || label == "" {
			log.Errorf("selection pair %q must contain non-empty value and label", pair)
			return nil, fmt.Errorf("selection pair %q must contain non-empty value and label", pair)
		}

		// Preserve declaration order because UI selection lists depend on it.
		options = append(options, SelectionOption{Value: value, Label: label})
		tag = strings.TrimSpace(tag[end+1:])
		if tag == "" {
			break
		}
		if !strings.HasPrefix(tag, ",") {
			log.Errorf("selection tag must separate pairs with commas")
			return nil, fmt.Errorf("selection tag must separate pairs with commas")
		}
		tag = tag[1:]
	}
	return options, nil
}

func trimSelectionPart(value string) string {
	// Trim whitespace first, then strip wrapping quotes so both
	// ("draft","Draft") and (draft,Draft) parse the same way.
	value = strings.TrimSpace(value)
	value = strings.Trim(value, `"'`)
	return strings.TrimSpace(value)
}

// modelRecordType unwraps pointers until it reaches the concrete authoring
// struct.
func modelRecordType(model any) (reflect.Type, error) {
	if model == nil {
		log.Errorf("orm: nil model definition record")
		return nil, fmt.Errorf("orm: nil model definition record")
	}

	// Keep unwrapping pointers so callers can pass either value or pointer
	// forms without changing the derived schema.
	modelType := reflect.TypeOf(model)
	for modelType.Kind() == reflect.Pointer {
		modelType = modelType.Elem()
	}
	if modelType.Kind() != reflect.Struct {
		log.Errorf("orm: expected struct or pointer to struct, got %T", model)
		return nil, fmt.Errorf("orm: expected struct or pointer to struct, got %T", model)
	}
	return modelType, nil
}

// indirectType unwraps pointers so kind inference works for optional scalar
// fields.
func indirectType(fieldType reflect.Type) reflect.Type {
	// Pointer indirection is ignored for metadata purposes because storage kind
	// depends on the underlying scalar or struct.
	for fieldType.Kind() == reflect.Pointer {
		fieldType = fieldType.Elem()
	}
	return fieldType
}

func isIntegerKind(kind reflect.Kind) bool {
	switch kind {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return true
	default:
		return false
	}
}

func isFloatKind(kind reflect.Kind) bool {
	switch kind {
	case reflect.Float32, reflect.Float64:
		return true
	default:
		return false
	}
}

func splitNonEmpty(value, separator string) []string {
	// Split first, then trim and discard blanks so tag authors can format lists
	// with extra spaces without changing semantics.
	parts := strings.Split(value, separator)
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}
