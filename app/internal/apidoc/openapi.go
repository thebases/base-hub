package apidoc

// File overview:
// - Responsibility: builds API documentation structures for openapi, translating runtime metadata into discoverable docs and OpenAPI-friendly payloads.
// - Key symbols: `buildOpenAPI`, `schemaNameForModel`.
// - Pattern: Keeps one focused responsibility per file so adjacent packages can reuse this logic without depending on unrelated transport or persistence details.
// - Spec version: emits OpenAPI 3.1.0 (`jsonSchemaDialect` pinned to the OAS 3.1 base dialect); Schema Objects here are plain JSON Schema, so avoid 3.0-only keywords like `nullable`.

import (
	"fmt"
	"sort"
	"strings"

	"git.thebase.vn/base_hub/internal/orm"
)

// buildOpenAPI builds the open api handled by this package.
func buildOpenAPI(config Config, models []orm.ModelMetadata, lang string) map[string]any {
	components := map[string]any{
		// OAuth 2.0 bearer is the sole inbound credential now (RBAC/OAuth
		// backend plan §1/§7.4): the X-API-Key service key and a bare
		// Authorization: Basic header on /rpc were both removed. Basic
		// survives only as POST /oauth/token's grant_type=password body,
		// which is not a per-route security requirement to document here.
		"securitySchemes": map[string]any{
			"BearerAuth": map[string]any{
				"type":   "http",
				"scheme": "bearer",
			},
		},
		"schemas": map[string]any{},
	}
	paths := map[string]any{}
	schemas := components["schemas"].(map[string]any)

	for _, model := range models {
		recordSchemaName := schemaNameForModel(model.Name)
		schemas[recordSchemaName] = modelRecordSchema(model)

		methodsByName := make(map[string]orm.RuntimeMethodMetadata, len(model.RuntimeMethods))
		for _, method := range model.RuntimeMethods {
			methodsByName[method.Name] = method
		}
		// Walk each runtimemethods) entry in order so validation and side effects stay scoped to the current element.
		for _, methodName := range publicMethodNames(model.RuntimeMethods) {
			method := methodsByName[methodName]
			path := rpcPathForMethod(config.BasePath, model.Name, method)
			operation := map[string]any{
				"tags":        []string{moduleNameForModel(model.Name), model.Name},
				"summary":     openAPISummary(model, method),
				"description": openAPIDescription(model, method, lang),
				"operationId": operationID(model.Name, method.Name),
				"security": []map[string][]string{
					{"BearerAuth": {}},
				},
				"responses": map[string]any{
					"200": map[string]any{
						"description": "Successful response",
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": responseSchemaForMethod(model, method),
							},
						},
					},
					"400": map[string]any{
						"description": "Validation or runtime error",
					},
					"401": map[string]any{
						"description": "Authentication required",
					},
					"403": map[string]any{
						"description": "Permission denied",
					},
				},
			}

			if method.UsesPathID {
				operation["parameters"] = []map[string]any{
					{
						"name":        "id",
						"in":          "path",
						"required":    true,
						"description": "One record ID or a comma-separated record set.",
						"schema": map[string]any{
							"type": "string",
						},
					},
				}
			}

			if requestSchema := requestSchemaForMethod(model, method); requestSchema != nil {
				requestContent := map[string]any{
					"schema": requestSchema,
				}
				if requestExample := requestExampleForMethod(model, method); requestExample != nil {
					requestContent["example"] = requestExample
				}
				operation["requestBody"] = map[string]any{
					"required": false,
					"content": map[string]any{
						"application/json": requestContent,
					},
				}
			}
			paths[path] = map[string]any{"post": operation}
		}
	}
	// modules/base-auth's /oauth/token and /oauth/revoke are plain Fiber
	// routes, not ORM-registered models, so the model-walk above never
	// discovers them; document them here by hand so the OAuth 2.0 surface
	// (RBAC/OAuth backend plan §3) isn't invisible in the generated spec.
	for path, operation := range staticOAuthPaths(config.BasePath) {
		paths[path] = operation
	}
	return map[string]any{
		"openapi": "3.1.0",
		// 3.1's Schema Object is full JSON Schema 2020-12; declaring the
		// dialect explicitly avoids ambiguity for validators/generators that
		// don't assume the OAS base dialect as their default.
		"jsonSchemaDialect": "https://spec.openapis.org/oas/3.1/dialect/base",
		"info": map[string]any{
			"title":   strings.TrimSpace(config.Title),
			"version": strings.TrimSpace(config.Version),
		},
		"paths":      paths,
		"components": components,
	}
}

// staticOAuthPaths documents modules/base-auth/routes' hand-written OAuth
// endpoints (RFC 6749 token grants/browser grants and RFC 7009 revocation).
// Form endpoints accept application/x-www-form-urlencoded bodies via
// c.FormValue, not JSON, so they need their own schema rather than reusing
// requestSchemaForMethod.
func staticOAuthPaths(basePath string) map[string]any {
	basePath = normalizeBasePath(basePath)
	tokenResponseSchema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"access_token":  map[string]any{"type": "string", "description": "Signed RS256 JWT access token."},
			"refresh_token": map[string]any{"type": "string", "description": "Opaque, rotate-on-use refresh token. Omitted for grant_type=client_credentials."},
			"expires_in":    map[string]any{"type": "integer", "description": "Access token lifetime in seconds."},
			"token_type":    map[string]any{"type": "string", "enum": []string{"Bearer"}},
			"scope":         map[string]any{"type": "string", "description": "Requested scope echoed by authorization_code; omitted for grants that do not round-trip scope."},
		},
		"required": []string{"access_token", "expires_in", "token_type"},
	}
	tokenForm := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"grant_type":    map[string]any{"type": "string", "enum": []string{"password", "refresh_token", "client_credentials", "authorization_code"}},
			"db":            map[string]any{"type": "string", "description": "Target database name. Defaults to the home control-plane DB when omitted."},
			"username":      map[string]any{"type": "string", "description": "Required for grant_type=password."},
			"password":      map[string]any{"type": "string", "format": "password", "description": "Required for grant_type=password."},
			"refresh_token": map[string]any{"type": "string", "description": "Required for grant_type=refresh_token."},
			"client_id":     map[string]any{"type": "string", "description": "Required for grant_type=client_credentials and authorization_code."},
			"client_secret": map[string]any{"type": "string", "format": "password", "description": "Required for grant_type=client_credentials and authorization_code."},
			"code":          map[string]any{"type": "string", "description": "Required for grant_type=authorization_code."},
			"redirect_uri":  map[string]any{"type": "string", "format": "uri", "description": "Required for grant_type=authorization_code; must match the authorize request."},
			"code_verifier": map[string]any{"type": "string", "description": "Required for authorization_code only when the authorize request used PKCE."},
		},
		"required": []string{"grant_type"},
	}
	authorizeParams := []map[string]any{
		{"name": "response_type", "in": "query", "required": true, "schema": map[string]any{"type": "string", "enum": []string{"code", "token"}}, "description": "code = Authorization Code flow; token = Implicit flow."},
		{"name": "client_id", "in": "query", "required": true, "schema": map[string]any{"type": "string"}},
		{"name": "redirect_uri", "in": "query", "required": true, "schema": map[string]any{"type": "string", "format": "uri"}},
		{"name": "state", "in": "query", "required": false, "schema": map[string]any{"type": "string"}},
		{"name": "db", "in": "query", "required": false, "schema": map[string]any{"type": "string"}},
		{"name": "scope", "in": "query", "required": false, "schema": map[string]any{"type": "string"}},
		{"name": "code_challenge", "in": "query", "required": false, "schema": map[string]any{"type": "string"}, "description": "PKCE challenge for response_type=code."},
		{"name": "code_challenge_method", "in": "query", "required": false, "schema": map[string]any{"type": "string", "enum": []string{"S256", "plain"}}, "description": "PKCE method for response_type=code."},
	}
	authorizeLoginForm := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"response_type":         map[string]any{"type": "string", "enum": []string{"code", "token"}},
			"client_id":             map[string]any{"type": "string"},
			"redirect_uri":          map[string]any{"type": "string", "format": "uri"},
			"state":                 map[string]any{"type": "string"},
			"db":                    map[string]any{"type": "string"},
			"scope":                 map[string]any{"type": "string"},
			"code_challenge":        map[string]any{"type": "string"},
			"code_challenge_method": map[string]any{"type": "string", "enum": []string{"S256", "plain"}},
			"username":              map[string]any{"type": "string"},
			"password":              map[string]any{"type": "string", "format": "password"},
		},
		"required": []string{"response_type", "client_id", "redirect_uri", "username", "password"},
	}
	revokeForm := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"db":            map[string]any{"type": "string", "description": "Target database name. Defaults to the home control-plane DB when omitted."},
			"refresh_token": map[string]any{"type": "string"},
		},
		"required": []string{"refresh_token"},
	}
	errorResponses := map[string]any{
		"400": map[string]any{"description": "Malformed request or unsupported grant_type"},
		"401": map[string]any{"description": "Invalid credentials, client, or refresh token"},
		"429": map[string]any{"description": "Too many failed password-grant attempts; retry after Retry-After seconds"},
	}
	return map[string]any{
		basePath + "/oauth/token": map[string]any{
			"post": map[string]any{
				"tags":        []string{"auth", "oauth"},
				"summary":     "Issue an OAuth 2.0 token",
				"description": "RFC 6749 §4 token endpoint. Supports grant_type=password, refresh_token, client_credentials, and authorization_code. The authorization_code exchange is builtin-only; other grants delegate to the active TokenIssuer.",
				"operationId": "oauthToken",
				"requestBody": map[string]any{
					"required": true,
					"content": map[string]any{
						"application/x-www-form-urlencoded": map[string]any{"schema": tokenForm},
					},
				},
				"responses": mergeResponses(map[string]any{
					"200": map[string]any{
						"description": "Token issued",
						"content": map[string]any{
							"application/json": map[string]any{"schema": tokenResponseSchema},
						},
					},
				}, errorResponses),
			},
		},
		basePath + "/oauth/authorize": map[string]any{
			"get": map[string]any{
				"tags":        []string{"auth", "oauth"},
				"summary":     "Render OAuth authorization login",
				"description": "Browser authorization endpoint. response_type=code renders the Authorization Code UI; response_type=token renders the Implicit flow UI. On protocol errors after redirect_uri is trusted, redirects with OAuth error parameters.",
				"operationId": "oauthAuthorize",
				"parameters":  authorizeParams,
				"responses": map[string]any{
					"200": map[string]any{"description": "HTML login form"},
					"302": map[string]any{"description": "Redirect to redirect_uri with an OAuth error"},
					"400": map[string]any{"description": "Unknown client_id, unregistered redirect_uri, or unknown database"},
				},
			},
			"post": map[string]any{
				"tags":        []string{"auth", "oauth"},
				"summary":     "Submit OAuth authorization login",
				"description": "Submits the browser login form. response_type=code redirects with a single-use code in query parameters; response_type=token redirects with access_token/token_type/expires_in in the URI fragment and no refresh token.",
				"operationId": "oauthAuthorizeSubmit",
				"requestBody": map[string]any{
					"required": true,
					"content": map[string]any{
						"application/x-www-form-urlencoded": map[string]any{"schema": authorizeLoginForm},
					},
				},
				"responses": map[string]any{
					"200": map[string]any{"description": "HTML login form re-rendered after invalid credentials or throttling"},
					"302": map[string]any{"description": "Redirect to redirect_uri with code query params, token fragment params, or OAuth error params"},
					"400": map[string]any{"description": "Unknown client_id, unregistered redirect_uri, or unknown database"},
				},
			},
		},
		basePath + "/oauth/revoke": map[string]any{
			"post": map[string]any{
				"tags":        []string{"auth", "oauth"},
				"summary":     "Revoke a refresh token",
				"description": "RFC 7009 §2.2 revocation endpoint. Always returns 200, even for an unknown or already-revoked token, so the endpoint can't be used to probe token validity.",
				"operationId": "oauthRevoke",
				"security": []map[string][]string{
					{},
				},
				"requestBody": map[string]any{
					"required": true,
					"content": map[string]any{
						"application/x-www-form-urlencoded": map[string]any{"schema": revokeForm},
					},
				},
				"responses": map[string]any{
					"200": map[string]any{"description": "Revocation processed (best-effort; does not confirm the token existed)"},
					"400": map[string]any{"description": "Malformed request or missing refresh_token"},
				},
			},
		},
	}
}

// mergeResponses combines a success-response map with a shared set of error
// responses so staticOAuthPaths doesn't repeat the same 400/401/429 blocks.
func mergeResponses(success, errors map[string]any) map[string]any {
	merged := make(map[string]any, len(success)+len(errors))
	for code, body := range success {
		merged[code] = body
	}
	for code, body := range errors {
		merged[code] = body
	}
	return merged
}

func schemaNameForModel(modelName string) string {
	replacer := strings.NewReplacer(".", "_", "-", "_", "/", "_")
	return replacer.Replace(modelName)
}

func modelRecordSchema(model orm.ModelMetadata) map[string]any {
	properties := make(map[string]any, len(model.Fields))
	required := make([]string, 0, len(model.Fields))
	fieldNames := make([]string, 0, len(model.Fields))
	for _, field := range model.Fields {
		fieldNames = append(fieldNames, field.Name)
	}
	sort.Strings(fieldNames)
	fieldsByName := make(map[string]orm.FieldMetadata, len(model.Fields))
	for _, field := range model.Fields {
		fieldsByName[field.Name] = field
	}

	for _, name := range fieldNames {
		field := fieldsByName[name]
		properties[name] = fieldSchema(field)
		if field.Required {
			required = append(required, name)
		}
	}

	schema := map[string]any{
		"type":       "object",
		"properties": properties,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func fieldSchema(field orm.FieldMetadata) map[string]any {
	schema := map[string]any{}
	switch field.Kind {
	case orm.FieldKindID, orm.FieldKindInteger:
		schema["type"] = "integer"
	case orm.FieldKindBoolean:
		schema["type"] = "boolean"
	case orm.FieldKindFloat:
		schema["type"] = "number"
	case orm.FieldKindDate:
		schema["type"] = "string"
		schema["format"] = "date"
	case orm.FieldKindDateTime:
		schema["type"] = "string"
		schema["format"] = "date-time"
	case orm.FieldKindJSON:
		schema["type"] = "object"
	case orm.FieldKindOneToMany, orm.FieldKindManyToMany:
		schema["type"] = "array"
		schema["items"] = map[string]any{"type": "integer"}
	case orm.FieldKindManyToOne, orm.FieldKindOneToOne:
		schema["oneOf"] = []map[string]any{
			{"type": "integer"},
			{"type": "array", "items": map[string]any{}},
			{"type": "object"},
		}
	default:
		schema["type"] = "string"
	}
	if len(field.Selection) > 0 {
		enumValues := make([]string, 0, len(field.Selection))
		for _, option := range field.Selection {
			enumValues = append(enumValues, option.Value)
		}
		schema["enum"] = enumValues
	}
	if help := strings.TrimSpace(field.Help); help != "" {
		schema["description"] = help
	}
	if label := fieldLabel(field); label != "" {
		schema["title"] = label
	}
	return schema
}

func requestSchemaForMethod(model orm.ModelMetadata, method orm.RuntimeMethodMetadata) map[string]any {
	recordRef := map[string]any{"$ref": fmt.Sprintf("#/components/schemas/%s", schemaNameForModel(model.Name))}
	filterSchema := map[string]any{
		"type":                 "object",
		"description":          "Runtime filter or domain-style payload.",
		"additionalProperties": true,
	}
	switch method.Name {
	case "create":
		return map[string]any{
			"type":       "object",
			"properties": map[string]any{"args": map[string]any{"type": "array", "items": recordRef}},
		}
	case "write":
		return map[string]any{
			"type": "object",
			"properties": map[string]any{
				"args": map[string]any{
					"type":  "array",
					"items": recordRef,
				},
			},
		}
	case "fields_get", "default_get":
		return map[string]any{
			"type": "object",
			"properties": map[string]any{
				"args": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type":  "array",
						"items": map[string]any{"type": "string"},
					},
				},
			},
		}
	case "search", "search_count", "search_read", "search_fetch", "read_group", "name_search":
		return map[string]any{
			"type": "object",
			"properties": map[string]any{
				"kwargs": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"filter": filterSchema,
					},
					"additionalProperties": true,
				},
			},
		}
	case "onchange":
		return map[string]any{
			"type": "object",
			"properties": map[string]any{
				"args": map[string]any{
					"type":  "array",
					"items": map[string]any{},
				},
				"kwargs": map[string]any{
					"type":                 "object",
					"additionalProperties": true,
				},
			},
		}
	case "read", "unlink", "exists", "name_get", "copy_data", "copy", "has_access", "toggle_active", "check_access_rights":
		return nil
	default:
		return map[string]any{
			"type": "object",
			"properties": map[string]any{
				"args": map[string]any{
					"type":  "array",
					"items": map[string]any{},
				},
				"kwargs": map[string]any{
					"type":                 "object",
					"additionalProperties": true,
				},
			},
		}
	}
}

func responseSchemaForMethod(model orm.ModelMetadata, method orm.RuntimeMethodMetadata) map[string]any {
	recordRef := map[string]any{"$ref": fmt.Sprintf("#/components/schemas/%s", schemaNameForModel(model.Name))}
	switch method.Name {
	case "read", "search_read", "search_fetch", "copy_data", "read_group":
		return map[string]any{"type": "array", "items": recordRef}
	case "create":
		return recordRef
	case "search":
		return map[string]any{"type": "array", "items": map[string]any{"type": "integer"}}
	case "search_count":
		return map[string]any{"type": "integer"}
	case "fields_get", "default_get", "onchange":
		return map[string]any{"type": "object", "additionalProperties": true}
	case "name_get", "name_search":
		return map[string]any{"type": "array", "items": map[string]any{"type": "array", "items": map[string]any{}}}
	case "write", "unlink", "toggle_active", "has_access", "check_access_rights":
		return map[string]any{"type": "boolean"}
	case "exists":
		return map[string]any{"type": "array", "items": map[string]any{}}
	default:
		return map[string]any{}
	}
}

func operationID(modelName, methodName string) string {
	replacer := strings.NewReplacer(".", "_", "-", "_", "/", "_")
	return replacer.Replace(modelName + "_" + methodName)
}

func openAPISummary(model orm.ModelMetadata, method orm.RuntimeMethodMetadata) string {
	if template, ok := defaultMethodDocs[method.Name]; ok && strings.TrimSpace(template.Doc) != "" {
		return template.Doc
	}
	return fmt.Sprintf("%s on %s", method.Name, model.Name)
}

func openAPIDescription(model orm.ModelMetadata, method orm.RuntimeMethodMetadata, lang string) string {
	methodDoc := methodDocument(model, method, "", lang)
	parts := []string{
		fmt.Sprintf("Model: `%s`.", model.Name),
		fmt.Sprintf("Method: `%s`.", method.Name),
	}
	if strings.TrimSpace(methodDoc.Doc) != "" {
		parts = append(parts, methodDoc.Doc)
	}
	if strings.TrimSpace(methodDoc.DocFile) != "" {
		parts = append(parts, methodDoc.DocFile)
	}
	if methodDoc.Signature != "" {
		parts = append(parts, fmt.Sprintf("Signature: `%s`.", methodDoc.Signature))
	}
	return strings.Join(parts, " ")
}
