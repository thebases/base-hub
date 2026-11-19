package apidoc

// File overview:
// - Responsibility: builds API documentation structures for method docs, translating runtime metadata into discoverable docs and OpenAPI-friendly payloads.
// - Key symbols: `methodDocument`, `requestExampleForMethod`, `methodTemplate`.
// - Pattern: Keeps one focused responsibility per file so adjacent packages can reuse this logic without depending on unrelated transport or persistence details.

import (
	"fmt"
	"sort"
	"strings"

	"git.thebase.vn/base_hub/internal/orm"
)

type methodTemplate struct {
	Signature  string
	Parameters []ParameterDoc
	Return     *ReturnDoc
	Doc        string
}

var defaultMethodDocs = map[string]methodTemplate{
	"fields_get": {
		Signature: "(fields=None) -> object",
		Parameters: []ParameterDoc{
			{Name: "fields", Annotation: "[]string", Doc: "Optional logical field names to describe. Omit or pass an empty list to return every documented field."},
		},
		Return: &ReturnDoc{Annotation: "object", Doc: "Map keyed by logical field name with runtime field metadata."},
		Doc:    "Describe the registered fields for one model.",
	},
	"default_get": {
		Signature: "(fields) -> object",
		Parameters: []ParameterDoc{
			{Name: "fields", Annotation: "[]string", Doc: "Logical field names whose default values should be resolved."},
		},
		Return: &ReturnDoc{Annotation: "object", Doc: "Map of default field values derived from runtime metadata."},
		Doc:    "Resolve runtime defaults for a subset of fields.",
	},
	"search": {
		Signature: "(filter=None) -> []int",
		Parameters: []ParameterDoc{
			{Name: "filter", Annotation: "object", Doc: "Optional filter or domain-style payload used by the runtime repository search helper."},
		},
		Return: &ReturnDoc{Annotation: "[]int", Doc: "Ordered record IDs that match the requested filter."},
		Doc:    "Search for records that satisfy the runtime filter contract.",
	},
	"search_count": {
		Signature: "(filter=None) -> int",
		Parameters: []ParameterDoc{
			{Name: "filter", Annotation: "object", Doc: "Optional filter or domain-style payload used to count matching rows."},
		},
		Return: &ReturnDoc{Annotation: "int", Doc: "Number of matching records."},
		Doc:    "Count records that satisfy the runtime filter contract.",
	},
	"search_fetch": {
		Signature: "(filter=None, fields=None) -> []object",
		Parameters: []ParameterDoc{
			{Name: "filter", Annotation: "object", Doc: "Optional filter or domain-style payload used by the runtime repository search helper."},
			{Name: "fields", Annotation: "[]string", Doc: "Optional logical field names to project in the response."},
		},
		Return: &ReturnDoc{Annotation: "[]object", Doc: "Projected records matching the requested filter."},
		Doc:    "Search and return projected records in one call.",
	},
	"read": {
		Signature: "(ids, fields=None) -> []object",
		Parameters: []ParameterDoc{
			{Name: "ids", Annotation: "[]int", Doc: "Record IDs to load. When using the HTTP transport, these IDs are supplied in the path."},
			{Name: "fields", Annotation: "[]string", Doc: "Optional logical field names to project in the response."},
		},
		Return: &ReturnDoc{Annotation: "[]object", Doc: "Projected records for the requested IDs."},
		Doc:    "Read one or more existing records by ID.",
	},
	"search_read": {
		Signature: "(filter=None, fields=None) -> []object",
		Parameters: []ParameterDoc{
			{Name: "filter", Annotation: "object", Doc: "Optional filter or domain-style payload used by the runtime repository search helper."},
			{Name: "fields", Annotation: "[]string", Doc: "Optional logical field names to project in the response."},
		},
		Return: &ReturnDoc{Annotation: "[]object", Doc: "Projected records matching the requested filter."},
		Doc:    "Search and read records in one round trip.",
	},
	"create": {
		Signature: "(values) -> object",
		Parameters: []ParameterDoc{
			{Name: "values", Annotation: "object", Doc: "Field-value map for the new record."},
		},
		Return: &ReturnDoc{Annotation: "object", Doc: "The created record payload after runtime normalization."},
		Doc:    "Create one record inside a single runtime transaction.",
	},
	"write": {
		Signature: "(ids, values) -> bool",
		Parameters: []ParameterDoc{
			{Name: "ids", Annotation: "[]int", Doc: "Record IDs to update. When using the HTTP transport, these IDs are supplied in the path."},
			{Name: "values", Annotation: "object", Doc: "Field-value map to write on every targeted record."},
		},
		Return: &ReturnDoc{Annotation: "bool", Doc: "True when the write succeeds."},
		Doc:    "Write field values to one or more records in one runtime transaction.",
	},
	"unlink": {
		Signature: "(ids) -> bool",
		Parameters: []ParameterDoc{
			{Name: "ids", Annotation: "[]int", Doc: "Record IDs to delete. When using the HTTP transport, these IDs are supplied in the path."},
		},
		Return: &ReturnDoc{Annotation: "bool", Doc: "True when the unlink succeeds."},
		Doc:    "Delete one or more records.",
	},
	"name_create": {
		Signature: "(name) -> [id, display_name]",
		Parameters: []ParameterDoc{
			{Name: "name", Annotation: "string", Doc: "Display name for the record to create."},
		},
		Return: &ReturnDoc{Annotation: "[id, display_name]", Doc: "Tuple containing the new record ID and display name."},
		Doc:    "Convenience helper that creates a record from a single display-name value.",
	},
	"name_search": {
		Signature: "(name='', filter=None, limit=100) -> [][2]any",
		Parameters: []ParameterDoc{
			{Name: "name", Annotation: "string", Default: "", Doc: "Free-text value used by the model's name search semantics."},
			{Name: "filter", Annotation: "object", Doc: "Optional filter or domain-style payload applied alongside the name lookup."},
			{Name: "limit", Annotation: "int", Default: 100, Doc: "Maximum number of suggestions to return."},
		},
		Return: &ReturnDoc{Annotation: "[][2]any", Doc: "List of `[id, display_name]` tuples."},
		Doc:    "Search by display name and return lightweight suggestions.",
	},
	"name_get": {
		Signature: "(ids) -> [][2]any",
		Parameters: []ParameterDoc{
			{Name: "ids", Annotation: "[]int", Doc: "Record IDs whose display names should be resolved."},
		},
		Return: &ReturnDoc{Annotation: "[][2]any", Doc: "List of `[id, display_name]` tuples."},
		Doc:    "Return display-name tuples for the requested records.",
	},
	"read_group": {
		Signature: "(filter=None, fields=None, groupby=None) -> []object",
		Parameters: []ParameterDoc{
			{Name: "filter", Annotation: "object", Doc: "Optional filter or domain-style payload applied before grouping."},
			{Name: "fields", Annotation: "[]string", Doc: "Aggregated field list to compute for each group."},
			{Name: "groupby", Annotation: "[]string", Doc: "Logical field names used to group the result set."},
		},
		Return: &ReturnDoc{Annotation: "[]object", Doc: "Aggregated group rows."},
		Doc:    "Aggregate records by one or more fields.",
	},
	"copy_data": {
		Signature: "(ids) -> []object",
		Parameters: []ParameterDoc{
			{Name: "ids", Annotation: "[]int", Doc: "Record IDs whose values should be duplicated into an unsaved payload."},
		},
		Return: &ReturnDoc{Annotation: "[]object", Doc: "Field maps suitable for a later create/copy flow."},
		Doc:    "Return normalized duplicate payloads without persisting a new record.",
	},
	"copy": {
		Signature: "(ids) -> any",
		Parameters: []ParameterDoc{
			{Name: "ids", Annotation: "[]int", Doc: "Record IDs to duplicate."},
		},
		Return: &ReturnDoc{Annotation: "any", Doc: "Model-defined copy result."},
		Doc:    "Duplicate one or more records.",
	},
	"exists": {
		Signature: "(ids) -> []any",
		Parameters: []ParameterDoc{
			{Name: "ids", Annotation: "[]int", Doc: "Record IDs to test for existence."},
		},
		Return: &ReturnDoc{Annotation: "[]any", Doc: "Subset of IDs that still exist."},
		Doc:    "Return the surviving IDs from the requested record set.",
	},
	"has_access": {
		Signature: "(ids) -> bool",
		Parameters: []ParameterDoc{
			{Name: "ids", Annotation: "[]int", Doc: "Record IDs to test against the caller's access rules."},
		},
		Return: &ReturnDoc{Annotation: "bool", Doc: "True when the caller can access the requested records."},
		Doc:    "Check record-level access for the current caller.",
	},
	"check_access_rights": {
		Signature: "(operation='read') -> bool",
		Parameters: []ParameterDoc{
			{Name: "operation", Annotation: "string", Default: "read", Doc: "CRUD operation to validate for the current caller."},
		},
		Return: &ReturnDoc{Annotation: "bool", Doc: "True when the caller has the requested model-level permission."},
		Doc:    "Check model-level access rights for the current caller.",
	},
	"toggle_active": {
		Signature: "(ids) -> bool",
		Parameters: []ParameterDoc{
			{Name: "ids", Annotation: "[]int", Doc: "Record IDs whose `active` flag should be toggled."},
		},
		Return: &ReturnDoc{Annotation: "bool", Doc: "True when the toggle succeeds."},
		Doc:    "Toggle the archive state of one or more records.",
	},
	"onchange": {
		Signature: "(values, changed_fields, fields_spec=None) -> object",
		Parameters: []ParameterDoc{
			{Name: "values", Annotation: "object", Doc: "Current draft values before the onchange call."},
			{Name: "changed_fields", Annotation: "[]string", Doc: "Logical field names that changed on the client."},
			{Name: "fields_spec", Annotation: "object", Doc: "Optional UI field spec used by dynamic clients."},
		},
		Return: &ReturnDoc{Annotation: "object", Doc: "Onchange payload with updated values and warnings."},
		Doc:    "Run idempotent UI-side onchange logic without persisting a write.",
	},
	"get_empty_list_help": {
		Signature: "() -> string",
		Return:    &ReturnDoc{Annotation: "string", Doc: "Human-friendly empty-state helper text."},
		Doc:       "Return the model's empty-list help text.",
	},
	"view_header_get": {
		Signature: "() -> string",
		Return:    &ReturnDoc{Annotation: "string", Doc: "Human-friendly view header text."},
		Doc:       "Return one display header for the model's primary view.",
	},
}

func methodDocument(meta orm.ModelMetadata, runtimeMethod orm.RuntimeMethodMetadata, basePath, lang string) MethodDocument {
	template, hasTemplate := defaultMethodDocs[runtimeMethod.Name]
	moduleName := moduleNameForModel(meta.Name)
	doc := MethodDocument{
		Signature:      genericMethodSignature(runtimeMethod),
		Parameters:     map[string]ParameterDoc{},
		RequestExample: requestExampleForMethod(meta, runtimeMethod),
		Model:          meta.Name,
		Module:         moduleName,
		Public:         runtimeMethod.Public,
		ReadOnly:       runtimeMethod.ReadOnly,
		ModelOnly:      runtimeMethod.ModelOnly,
		Path:           rpcPathForMethod(basePath, meta.Name, runtimeMethod),
		AliasPath:      aliasPathForMethod(basePath, runtimeMethod),
		AliasPaths:     extraAliasPaths(basePath, runtimeMethod),
	}
	if hasTemplate {
		doc.Signature = template.Signature
		doc.Doc = template.Doc
		if template.Return != nil {
			returnCopy := *template.Return
			doc.Return = &returnCopy
		}
		for _, parameter := range template.Parameters {
			parameterCopy := parameter
			doc.Parameters[parameter.Name] = parameterCopy
		}
	}
	if len(doc.Parameters) == 0 {
		// Walk each genericmethodparameters(runtimemethod) entry in order so validation and side effects stay scoped to the current element.
		for _, parameter := range genericMethodParameters(runtimeMethod) {
			doc.Parameters[parameter.Name] = parameter
		}
	}
	// A model-declared Description wins over the built-in default text.
	if strings.TrimSpace(runtimeMethod.Description) != "" {
		doc.Doc = runtimeMethod.Description
	}
	doc.DocFile = descriptionFileBody(lang, runtimeMethod.DescriptionFile)
	doc.API = methodAPITags(runtimeMethod)
	return doc
}

func requestExampleForMethod(meta orm.ModelMetadata, runtimeMethod orm.RuntimeMethodMetadata) any {
	writableValues := writableFieldExample(meta.Fields)
	allFieldNames := documentedFieldNames(meta.Fields)

	switch runtimeMethod.Name {
	case "create", "write":
		if len(writableValues) == 0 {
			return map[string]any{"args": []any{map[string]any{}}}
		}
		return map[string]any{"args": []any{writableValues}}
	case "default_get":
		return map[string]any{"args": []any{allFieldNames}}
	case "fields_get":
		return map[string]any{"args": []any{allFieldNames}}
	case "search", "search_count", "search_read", "search_fetch", "read_group":
		return map[string]any{"kwargs": map[string]any{"filter": map[string]any{}}}
	case "name_search":
		return map[string]any{"args": []any{""}, "kwargs": map[string]any{"filter": map[string]any{}}}
	case "onchange":
		return map[string]any{
			"args": []any{
				writableValues,
				[]string{},
			},
			"kwargs": map[string]any{
				"fields_spec": map[string]any{},
			},
		}
	default:
		return nil
	}
}

func documentedFieldNames(fields []orm.FieldMetadata) []string {
	names := make([]string, 0, len(fields))
	for _, field := range fields {
		if strings.TrimSpace(field.Name) == "" {
			continue
		}
		names = append(names, field.Name)
	}
	sort.Strings(names)
	return names
}

func writableFieldExample(fields []orm.FieldMetadata) map[string]any {
	names := make([]string, 0, len(fields))
	fieldsByName := make(map[string]orm.FieldMetadata, len(fields))
	for _, field := range fields {
		if !includeWritableFieldExample(field) {
			continue
		}
		names = append(names, field.Name)
		fieldsByName[field.Name] = field
	}
	sort.Strings(names)

	values := make(map[string]any, len(names))
	for _, name := range names {
		values[name] = sampleValueForField(fieldsByName[name])
	}
	return values
}

func includeWritableFieldExample(field orm.FieldMetadata) bool {
	if strings.TrimSpace(field.Name) == "" {
		return false
	}
	if field.Name == "id" || field.Kind == orm.FieldKindID || field.PrimaryKey {
		return false
	}
	if field.ReadOnly || strings.TrimSpace(field.Compute) != "" {
		return false
	}
	return true
}

func sampleValueForField(field orm.FieldMetadata) any {
	if len(field.Selection) > 0 {
		return field.Selection[0].Value
	}
	switch field.Kind {
	case orm.FieldKindBoolean:
		return false
	case orm.FieldKindID, orm.FieldKindInteger, orm.FieldKindManyToOne, orm.FieldKindOneToOne:
		return 0
	case orm.FieldKindFloat:
		return 0
	case orm.FieldKindJSON:
		if strings.HasPrefix(field.Type, "[]") {
			return []any{}
		}
		return map[string]any{}
	case orm.FieldKindOneToMany, orm.FieldKindManyToMany:
		return []any{}
	case orm.FieldKindDate:
		return "2026-01-01"
	case orm.FieldKindDateTime:
		return "2026-01-01T00:00:00Z"
	default:
		return ""
	}
}

func methodAPITags(method orm.RuntimeMethodMetadata) []string {
	out := make([]string, 0, 2)
	if method.ModelOnly {
		out = append(out, "model")
	}
	if method.ReadOnly {
		out = append(out, "readonly")
	}
	return out
}

func genericMethodSignature(method orm.RuntimeMethodMetadata) string {
	if method.ModelOnly {
		return "(*args, **kwargs) -> any"
	}
	return "(ids, *args, **kwargs) -> any"
}

func genericMethodParameters(method orm.RuntimeMethodMetadata) []ParameterDoc {
	parameters := make([]ParameterDoc, 0, 3)
	if !method.ModelOnly {
		parameters = append(parameters, ParameterDoc{
			Name:       "ids",
			Annotation: "[]int",
			Doc:        "Record IDs supplied in the transport path or positional arguments.",
		})
	}
	parameters = append(parameters,
		ParameterDoc{Name: "args", Annotation: "[]any", Doc: "Positional runtime arguments."},
		ParameterDoc{Name: "kwargs", Annotation: "object", Doc: "Keyword runtime arguments."},
	)
	return parameters
}

func publicMethodNames(methods []orm.RuntimeMethodMetadata) []string {
	names := make([]string, 0, len(methods))
	for _, method := range methods {
		if !method.Public {
			continue
		}
		names = append(names, method.Name)
	}
	sort.Strings(names)
	return names
}

func publicMethodMap(meta orm.ModelMetadata, basePath, lang string) map[string]MethodDocument {
	names := publicMethodNames(meta.RuntimeMethods)
	methodsByName := make(map[string]orm.RuntimeMethodMetadata, len(meta.RuntimeMethods))
	for _, method := range meta.RuntimeMethods {
		methodsByName[method.Name] = method
	}

	out := make(map[string]MethodDocument, len(names))
	for _, name := range names {
		method := methodsByName[name]
		out[name] = methodDocument(meta, method, basePath, lang)
	}
	return out
}

// aliasPathForMethod mirrors registerRuntimeMethodAliasRoutes: a legacy AliasRoute is mounted at
// "/base/<AliasRoute>" and a REST-form one ("GET /base/x") at its own path, both on the same
// router as /rpc, so they share the documented base path. A REST-form alias keeps its verb
// prefix ("GET /base/x") since it isn't a POST. Returns "" when the method declares no alias.
func aliasPathForMethod(basePath string, method orm.RuntimeMethodMetadata) string {
	verb, path, rest := orm.ParseAliasRoute(method.AliasRoute)
	if path == "" {
		return ""
	}
	if rest {
		return verb + " " + normalizeBasePath(basePath) + path
	}
	return normalizeBasePath(basePath) + path
}

// extraAliasPaths renders each RuntimeMethod.Aliases route as "VERB /path" with the same base
// path rules as aliasPathForMethod; unlike it, the verb is always shown (legacy routes are POST).
func extraAliasPaths(basePath string, method orm.RuntimeMethodMetadata) []string {
	if len(method.AliasRoutes) == 0 {
		return nil
	}
	paths := make([]string, 0, len(method.AliasRoutes))
	for _, route := range method.AliasRoutes {
		verb, path, _ := orm.ParseAliasRoute(route)
		if path == "" {
			continue
		}
		paths = append(paths, verb+" "+normalizeBasePath(basePath)+path)
	}
	return paths
}

// It uses normalizeBasePath and the runtime method metadata so docs reflect
// the transport contract declared by each model method.
func rpcPathForMethod(basePath, modelName string, method orm.RuntimeMethodMetadata) string {
	basePath = normalizeBasePath(basePath)
	escapedModel := strings.TrimSpace(modelName)
	escapedMethod := strings.TrimSpace(method.Name)
	if method.UsesPathID {
		return fmt.Sprintf("%s/rpc/%s/%s/{id}", basePath, escapedModel, escapedMethod)
	}
	return fmt.Sprintf("%s/rpc/%s/%s", basePath, escapedModel, escapedMethod)
}

// descriptionFileBody loads a DescriptionFile reference; empty when unset or unreadable.
func descriptionFileBody(lang, ref string) string {
	if strings.TrimSpace(ref) == "" {
		return ""
	}
	body, _ := ReadDocument(lang, ref)
	return body
}
