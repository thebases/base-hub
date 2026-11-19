package nodes

// File overview:
// - Responsibility: the crud_model@v1 node — generic model create/search/write/delete against
//   any registered model, driven entirely by the runtime hooks (no direct DB access here).
// - Key symbols: `CRUDModel`.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"git.thebase.vn/base_hub/internal/services/log"
)

// init registers this node's single definition (plan T93): canvas spec, catalog entry and
// param validation all derive from it.
func init() {
	registerNode(nodeDef{
		Code:        "crud_model",
		Version:     1,
		Sequence:    20,
		Label:       "CRUD Model",
		Description: "Run a generic runtime model CRUD operation and merge the result back into the workflow payload.",
		Category:    "Persistence",
		Params: []paramDef{
			{Name: "model_name", Label: "Model Name", Type: "combobox", Required: true, Column: "model_name"},
			{
				Name:     "operation",
				Label:    "Operation",
				Type:     "select",
				Required: true,
				Default:  "find_or_create",
				Options: []paramOption{
					{Value: "create", Label: "Create"},
					{Value: "update", Label: "Update"},
					{Value: "upsert", Label: "Upsert"},
					{Value: "find_or_create", Label: "Find Or Create"},
				},
				ExtraKey: "operation",
			},
			{Name: "merge_result", Label: "Merge Result", Type: "boolean", Default: true, ExtraKey: "merge_result"},
			{Name: "result_key", Label: "Result Key", Type: "string", Default: "_crud_result", ExtraKey: "result_key"},
		},
		SideEffect: effectWrite,
	}, CRUDModel)
}

// CRUDModel executes a generic model operation and returns the operation result
// merged into the current workflow payload by default.
func CRUDModel(ctx context.Context, req WorkflowActionRequest) ([]byte, error) {
	modelName := strings.TrimSpace(firstStringParam(req.Step.Params, "model", "model_name"))
	if modelName == "" {
		log.Errorf("crud_model: params.model is required")
		return nil, fmt.Errorf("crud_model: params.model is required")
	}

	input, err := decodeJSONObject(req.Payload)
	if err != nil {
		log.Errorf("crud_model: %v", err)
		return nil, fmt.Errorf("crud_model: %w", err)
	}

	operation := strings.TrimSpace(strings.ToLower(firstStringParam(req.Step.Params, "operation", "op", "method")))
	if operation == "" {
		operation = "find_or_create"
	}
	fields := stringSliceParam(req.Step.Params, "fields")

	filters, err := renderedObjectListParam(req.Step.Params, input, "filters", "domains", "searches")
	if err != nil {
		log.Errorf("crud_model: %v", err)
		return nil, fmt.Errorf("crud_model: %w", err)
	}
	filter, err := renderedObjectParam(req.Step.Params, input, "filter", "domain", "search")
	if err != nil {
		log.Errorf("crud_model: %v", err)
		return nil, fmt.Errorf("crud_model: %w", err)
	}
	if len(filters) == 0 {
		filters = []map[string]any{filter}
	}
	values, err := renderedObjectParam(req.Step.Params, input, "values", "payload", "data")
	if err != nil {
		log.Errorf("crud_model: %v", err)
		return nil, fmt.Errorf("crud_model: %w", err)
	}
	if len(values) == 0 && (operation == "create" || operation == "find_or_create" || operation == "search_or_create" || operation == "ensure") {
		values = cloneJSONObject(input)
	}

	var result any
	switch operation {
	case "search", "search_read":
		records, err := searchCRUDModelRecords(ctx, modelName, filters[0], fields)
		if err != nil {
			return nil, err
		}
		result = records
	case "search_one", "read_one":
		records, err := searchCRUDModelRecords(ctx, modelName, filters[0], fields)
		if err != nil {
			return nil, err
		}
		if len(records) > 0 {
			result = records[0]
		} else {
			result = map[string]any{}
		}
	case "create":
		record, err := createCRUDModelRecord(ctx, modelName, values)
		if err != nil {
			return nil, err
		}
		result = record
	case "find_or_create", "search_or_create", "ensure":
		record, err := findOrCreateCRUDModelRecord(ctx, modelName, filters, fields, values)
		if err != nil {
			return nil, err
		}
		result = record
	case "write", "update":
		ids := stringSliceParam(req.Step.Params, "ids")
		if len(ids) == 0 {
			records, err := searchCRUDModelRecords(ctx, modelName, filters[0], []string{"id"})
			if err != nil {
				return nil, err
			}
			for _, record := range records {
				if id := stringIDFromAny(record["id"]); id != "" {
					ids = append(ids, id)
				}
			}
		}
		updated, err := writeCRUDModelRecords(ctx, modelName, ids, values)
		if err != nil {
			return nil, err
		}
		result = map[string]any{"updated": updated, "ids": ids}
	case "unlink", "delete":
		ids := stringSliceParam(req.Step.Params, "ids")
		deleted, err := deleteCRUDModelRecords(ctx, modelName, ids)
		if err != nil {
			return nil, err
		}
		result = map[string]any{"deleted": deleted, "ids": ids}
	default:
		log.Errorf("crud_model: unsupported operation %q", operation)
		return nil, fmt.Errorf("crud_model: unsupported operation %q", operation)
	}

	if !boolParamDefault(req.Step.Params, "merge_result", true) {
		return json.Marshal(result)
	}
	output := cloneJSONObject(input)
	resultKey := strings.TrimSpace(firstStringParam(req.Step.Params, "result_key"))
	if resultKey == "" {
		resultKey = "_crud_result"
	}
	output[resultKey] = result
	if idField := strings.TrimSpace(firstStringParam(req.Step.Params, "id_field", "result_id_field")); idField != "" {
		if id := crudResultID(result); id != nil {
			output[idField] = id
		}
	}
	return json.Marshal(output)
}

func searchCRUDModelRecords(ctx context.Context, modelName string, filter map[string]any, fields []string) ([]map[string]any, error) {
	if workflowActionRuntimeHooks.SearchModelRecords == nil {
		log.Errorf("crud_model: runtime search hook is not initialized")
		return nil, fmt.Errorf("crud_model: runtime search hook is not initialized")
	}
	records, err := workflowActionRuntimeHooks.SearchModelRecords(ctx, modelName, filter, fields)
	if err != nil {
		log.Errorf("crud_model: search %s: %v", modelName, err)
		return nil, fmt.Errorf("crud_model: search %s: %w", modelName, err)
	}
	return records, nil
}

func createCRUDModelRecord(ctx context.Context, modelName string, values map[string]any) (map[string]any, error) {
	if workflowActionRuntimeHooks.CreateModelRecord == nil {
		log.Errorf("crud_model: runtime create hook is not initialized")
		return nil, fmt.Errorf("crud_model: runtime create hook is not initialized")
	}
	created, err := workflowActionRuntimeHooks.CreateModelRecord(ctx, modelName, values)
	if err != nil {
		log.Errorf("crud_model: create %s: %v", modelName, err)
		return nil, fmt.Errorf("crud_model: create %s: %w", modelName, err)
	}
	return created, nil
}

func findOrCreateCRUDModelRecord(ctx context.Context, modelName string, filters []map[string]any, fields []string, values map[string]any) (map[string]any, error) {
	for _, filter := range filters {
		records, err := searchCRUDModelRecords(ctx, modelName, filter, fields)
		if err != nil {
			return nil, err
		}
		if len(records) > 0 {
			return records[0], nil
		}
	}
	return createCRUDModelRecord(ctx, modelName, values)
}

func writeCRUDModelRecords(ctx context.Context, modelName string, ids []string, values map[string]any) (bool, error) {
	if workflowActionRuntimeHooks.WriteModelRecords == nil {
		log.Errorf("crud_model: runtime write hook is not initialized")
		return false, fmt.Errorf("crud_model: runtime write hook is not initialized")
	}
	if len(ids) == 0 {
		log.Errorf("crud_model: write requires ids")
		return false, fmt.Errorf("crud_model: write requires ids")
	}
	updated, err := workflowActionRuntimeHooks.WriteModelRecords(ctx, modelName, ids, values)
	if err != nil {
		log.Errorf("crud_model: write %s: %v", modelName, err)
		return false, fmt.Errorf("crud_model: write %s: %w", modelName, err)
	}
	return updated, nil
}

func deleteCRUDModelRecords(ctx context.Context, modelName string, ids []string) (bool, error) {
	if workflowActionRuntimeHooks.DeleteModelRecords == nil {
		log.Errorf("crud_model: runtime delete hook is not initialized")
		return false, fmt.Errorf("crud_model: runtime delete hook is not initialized")
	}
	if len(ids) == 0 {
		log.Errorf("crud_model: delete requires ids")
		return false, fmt.Errorf("crud_model: delete requires ids")
	}
	deleted, err := workflowActionRuntimeHooks.DeleteModelRecords(ctx, modelName, ids)
	if err != nil {
		log.Errorf("crud_model: delete %s: %v", modelName, err)
		return false, fmt.Errorf("crud_model: delete %s: %w", modelName, err)
	}
	return deleted, nil
}

func crudResultID(result any) any {
	switch typed := result.(type) {
	case map[string]any:
		return typed["id"]
	case []map[string]any:
		if len(typed) == 0 {
			return nil
		}
		return typed[0]["id"]
	default:
		return nil
	}
}

func stringIDFromAny(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case int:
		return fmt.Sprint(typed)
	case int64:
		return fmt.Sprint(typed)
	case float64:
		if typed == float64(int64(typed)) {
			return fmt.Sprint(int64(typed))
		}
	}
	return ""
}
