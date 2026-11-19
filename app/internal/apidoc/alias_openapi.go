package apidoc

// File overview:
// - Responsibility: builds the OpenAPI 3.1 document of every runtime-method alias route (AliasRoute + Aliases) registered by the backend modules; it feeds the "Alias Api" tab of the API docs page (frontend/vendor/base_apidoc).
// - Key symbols: `BuildAliasOpenAPI`, `WriteAliasOpenAPIFiles`, `AliasOpenAPIFileName`.
// - Pattern: mirrors registerRuntimeMethodAliasRoutes (same verb/path parsing, same duplicate-route rule) so the spec lists exactly the routes the server mounts; a method's DescriptionFile markdown becomes the operation description, which the viewer renders as markdown.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"git.thebase.vn/base_hub/internal/orm"
)

// AliasOpenAPIFileName returns the generated spec file name for lang: "open_apidoc.json" for the
// default language and "open_apidoc.<lang>.json" for the others.
func AliasOpenAPIFileName(lang string) string {
	lang = NormalizeLanguage(lang)
	if lang == DefaultLanguage {
		return "open_apidoc.json"
	}
	return "open_apidoc." + lang + ".json"
}

// WriteAliasOpenAPIFiles writes one alias OpenAPI file per supported language into dir.
// It is called by the build step (internal/tools/generate_open_apidoc), never at request time.
func WriteAliasOpenAPIFiles(dir string, config Config) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, lang := range Languages {
		body, err := json.MarshalIndent(BuildAliasOpenAPI(config, orm.RegisteredModels(), lang.Code), "", "  ")
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, AliasOpenAPIFileName(lang.Code)), append(body, '\n'), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// aliasEntry is one mounted alias route of a runtime method.
type aliasEntry struct {
	verb, path string
	rest       bool
	anonymous  bool
	example    any // MethodAlias.BodyExample; nil means generate one
}

var aliasPathParamPattern = regexp.MustCompile(`:([A-Za-z0-9_]+)\??`)

// BuildAliasOpenAPI returns an OpenAPI 3.1 document listing every alias route of models.
func BuildAliasOpenAPI(config Config, models []orm.ModelMetadata, lang string) map[string]any {
	basePath := normalizeBasePath(config.BasePath)
	paths := map[string]any{}
	tagNames := map[string]string{}
	seen := map[string]bool{}

	sorted := append([]orm.ModelMetadata(nil), models...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })

	for _, model := range sorted {
		moduleName := moduleNameForModel(model.Name)
		hasAlias := false
		for _, method := range model.RuntimeMethods {
			for index, alias := range aliasEntries(model, method) {
				// The server logs and skips a duplicate verb+path (first wins); the doc does the same.
				key := alias.verb + " " + alias.path
				if seen[key] {
					continue
				}
				seen[key] = true
				hasAlias = true

				docPath := basePath + aliasPathParamPattern.ReplaceAllString(alias.path, "{$1}")
				item, _ := paths[docPath].(map[string]any)
				if item == nil {
					item = map[string]any{}
					paths[docPath] = item
				}
				item[strings.ToLower(alias.verb)] = aliasOperation(model, method, alias, moduleName, index, lang)
			}
		}
		// Routes are grouped by model: one tag per model that declares an alias.
		if hasAlias {
			tagNames[model.Name] = modelTagDescription(model, moduleName)
		}
	}

	tags := make([]map[string]any, 0, len(tagNames))
	for _, name := range sortedKeys(tagNames) {
		tag := map[string]any{"name": name}
		if desc := tagNames[name]; desc != "" {
			tag["description"] = desc
		}
		tags = append(tags, tag)
	}
	return map[string]any{
		"openapi":           "3.1.0",
		"jsonSchemaDialect": "https://spec.openapis.org/oas/3.1/dialect/base",
		"info": map[string]any{
			"title":       strings.TrimSpace(config.Title) + " — Alias API",
			"version":     strings.TrimSpace(config.Version),
			"description": "Stable alias routes declared by the backend modules (`AliasRoute` / `Aliases` on runtime methods). Each one is an extra URL for the method at `/rpc/{model}/{method}`.",
		},
		"servers": []map[string]any{{"url": "/"}},
		"tags":    tags,
		"paths":   paths,
		"components": map[string]any{
			"securitySchemes": map[string]any{
				"BearerAuth": map[string]any{"type": "http", "scheme": "bearer"},
			},
		},
	}
}

// aliasEntries lists the routes of one method: AliasRoute first, then each extra alias. Anonymity
// per route needs the live method (metadata only carries the method-wide flag).
func aliasEntries(model orm.ModelMetadata, method orm.RuntimeMethodMetadata) []aliasEntry {
	var live *orm.RuntimeMethod
	if liveModel, ok := orm.LookupModel(model.Name); ok {
		if found, ok := liveModel.LookupRuntimeMethod(method.Name); ok {
			live = &found
		}
	}

	routes := []aliasEntry{}
	add := func(route string, anonymous bool, example any) {
		verb, path, rest := orm.ParseAliasRoute(route)
		if path == "" {
			return
		}
		routes = append(routes, aliasEntry{verb: verb, path: path, rest: rest, anonymous: method.AliasAnonymous || anonymous, example: example})
	}
	add(method.AliasRoute, false, nil)
	if live != nil {
		for _, alias := range live.Aliases {
			add(alias.Route, alias.Anonymous && strings.Contains(alias.Route, " "), alias.BodyExample)
		}
		return routes
	}
	for _, route := range method.AliasRoutes {
		add(route, false, nil)
	}
	return routes
}

// aliasOperation builds the Operation Object of one alias route.
func aliasOperation(model orm.ModelMetadata, method orm.RuntimeMethodMetadata, alias aliasEntry, moduleName string, index int, lang string) map[string]any {
	doc := methodDocument(model, method, "", lang)

	// Markdown body from DescriptionFile wins; otherwise fall back to the short description.
	description := strings.TrimSpace(doc.DocFile)
	if description == "" {
		description = strings.TrimSpace(doc.Doc)
	}
	header := fmt.Sprintf("Runs `%s.%s` (also at `POST /rpc/%s/%s`).", model.Name, method.Name, model.Name, method.Name)
	if description != "" {
		description = header + "\n\n" + description
	} else {
		description = header
	}

	operation := map[string]any{
		"tags":        []string{model.Name},
		"summary":     fmt.Sprintf("%s.%s", model.Name, method.Name),
		"description": description,
		"operationId": fmt.Sprintf("alias_%s_%d", operationID(model.Name, method.Name), index),
		"responses": map[string]any{
			"200": map[string]any{
				"description": "Successful response",
				"content":     map[string]any{"application/json": map[string]any{"schema": map[string]any{}}},
			},
			"400": map[string]any{"description": "Validation or runtime error"},
		},
	}
	if alias.anonymous {
		// An empty security array overrides the default: no credentials needed.
		operation["security"] = []map[string][]string{}
	} else {
		operation["security"] = []map[string][]string{{"BearerAuth": {}}}
		operation["responses"].(map[string]any)["401"] = map[string]any{"description": "Authentication required"}
		operation["responses"].(map[string]any)["403"] = map[string]any{"description": "Permission denied"}
	}

	var parameters []map[string]any
	for _, match := range aliasPathParamPattern.FindAllStringSubmatch(alias.path, -1) {
		parameters = append(parameters, map[string]any{
			"name":     match[1],
			"in":       "path",
			"required": true,
			"schema":   map[string]any{"type": "string"},
		})
	}
	hasBody := alias.verb == "POST" || alias.verb == "PUT" || alias.verb == "PATCH"
	for _, param := range method.Params {
		schema := map[string]any{"type": "string"}
		switch param.Type {
		case "integer", "int":
			schema["type"] = "integer"
		case "float", "number":
			schema["type"] = "number"
		case "boolean", "bool":
			schema["type"] = "boolean"
		}
		// A REST route reads kwargs from the query string (and the body); the legacy form is body-only.
		if alias.rest && !hasBody {
			parameters = append(parameters, map[string]any{
				"name": param.Name, "in": "query", "required": param.Required,
				"description": strings.TrimSpace(param.Help), "schema": schema,
			})
		}
	}
	if len(parameters) > 0 {
		operation["parameters"] = parameters
	}

	if hasBody {
		var schema map[string]any
		if alias.rest {
			schema = map[string]any{"type": "object", "additionalProperties": true, "description": "JSON object merged into the method kwargs."}
			if props := paramProperties(method.Params); len(props) > 0 {
				schema["properties"] = props
			}
		} else {
			schema = map[string]any{
				"type": "object",
				"properties": map[string]any{
					"args":   map[string]any{"type": "array", "items": map[string]any{}},
					"kwargs": map[string]any{"type": "object", "additionalProperties": true},
				},
			}
		}
		content := map[string]any{"schema": schema}
		// The viewer pre-fills its Body editor from `example`; without one it shows `{}`.
		if example := aliasBodyExample(model, method, alias); example != nil {
			content["example"] = example
		}
		operation["requestBody"] = map[string]any{
			"required": false,
			"content":  map[string]any{"application/json": content},
		}
	}
	return operation
}

// aliasBodyExample picks the sample request body of one alias: the declared BodyExample, else the
// RPC-style example for a legacy route, else a flat object built from the method's fields/params.
func aliasBodyExample(model orm.ModelMetadata, method orm.RuntimeMethodMetadata, alias aliasEntry) any {
	if alias.example != nil {
		return alias.example
	}
	if !alias.rest {
		if example := requestExampleForMethod(model, method); example != nil {
			return example
		}
		return map[string]any{"args": []any{}, "kwargs": paramExample(method.Params)}
	}
	// REST body is the kwargs object; for create/write the standard method takes the field values.
	if method.Name == "create" || method.Name == "write" {
		return writableFieldExample(model.Fields)
	}
	return paramExample(method.Params)
}

// paramExample builds a sample kwargs object from the declared params (default, first selection
// option, else a typed placeholder).
func paramExample(params []orm.RuntimeMethodParam) map[string]any {
	example := map[string]any{}
	for _, param := range params {
		switch {
		case param.Default != nil:
			example[param.Name] = param.Default
		case len(param.Selection) > 0:
			example[param.Name] = param.Selection[0].Value
		default:
			switch param.Type {
			case "integer", "int", "float", "number":
				example[param.Name] = 0
			case "boolean", "bool":
				example[param.Name] = false
			default:
				example[param.Name] = "string"
			}
		}
	}
	return example
}

// paramProperties maps declared RuntimeMethod params to JSON Schema properties.
func paramProperties(params []orm.RuntimeMethodParam) map[string]any {
	props := map[string]any{}
	for _, param := range params {
		prop := map[string]any{}
		switch param.Type {
		case "integer", "int":
			prop["type"] = "integer"
		case "float", "number":
			prop["type"] = "number"
		case "boolean", "bool":
			prop["type"] = "boolean"
		default:
			prop["type"] = "string"
		}
		if help := strings.TrimSpace(param.Help); help != "" {
			prop["description"] = help
		}
		props[param.Name] = prop
	}
	return props
}

// modelTagDescription labels a model group with its module and display name.
func modelTagDescription(model orm.ModelMetadata, moduleName string) string {
	label := strings.TrimSpace(modelLabel(model))
	if label == "" || label == model.Name {
		return "Module: " + moduleName
	}
	return label + " — module: " + moduleName
}

// sortedKeys returns the keys of set in ascending order.
func sortedKeys(set map[string]string) []string {
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
