package orm

// File overview:
// - Responsibility: implements the metadata-driven ORM/runtime kernel for runtime base methods, where storage, runtime dispatch, and security rules stay centralized.
// - Key symbols: `defaultRuntimeMethods`, `runtimeFieldsGet`.
// - Pattern: Leans on metadata-driven registration so storage mapping, runtime methods, and behavior hooks stay centralized instead of being scattered across handlers.
// - Pattern: Threads `context.Context` through the main execution paths so cancellation, auth scope, and transaction state can follow the work end to end.

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"git.thebase.vn/base_hub/internal/services/log"
	"git.thebase.vn/base_hub/internal/utils"
)

// ErrRuntimeMethodNotImplemented keeps unsupported default runtime methods explicit.
var ErrRuntimeMethodNotImplemented = fmt.Errorf("orm: runtime method not implemented")

// defaultRuntimeMethods selects the runtime methods handled by this package.
func defaultRuntimeMethods() []RuntimeMethod {
	// The defaults mirror the conventional model API surface so every registered
	// model gets the same baseline behavior unless it overrides a method.
	return []RuntimeMethod{
		{Name: "fields_get", Public: true, ModelOnly: true, ReadOnly: true, Call: runtimeFieldsGet},
		{Name: "default_get", Public: true, ModelOnly: true, ReadOnly: true, Call: runtimeDefaultGet},
		{Name: "search", Public: true, ModelOnly: true, ReadOnly: true, Call: runtimeSearch},
		{Name: "search_count", Public: true, ModelOnly: true, ReadOnly: true, Call: runtimeSearchCount},
		{Name: "search_fetch", Public: true, ModelOnly: true, ReadOnly: true, Call: runtimeSearchFetch},
		{Name: "read", Public: true, ReadOnly: true, UsesPathID: true, Call: runtimeRead},
		{Name: "search_read", Public: true, ModelOnly: true, ReadOnly: true, Call: runtimeSearchRead},
		{Name: "create", Public: true, ModelOnly: true, Call: runtimeCreate},
		{Name: "write", Public: true, UsesPathID: true, Call: runtimeWrite},
		{Name: "unlink", Public: true, UsesPathID: true, Call: runtimeUnlink},
		{Name: "name_create", Public: true, ModelOnly: true, Call: runtimeNameCreate},
		{Name: "name_search", Public: true, ModelOnly: true, ReadOnly: true, Call: runtimeNameSearch},
		{Name: "name_get", Public: true, ReadOnly: true, UsesPathID: true, Call: runtimeNameGet},
		{Name: "read_group", Public: true, ModelOnly: true, ReadOnly: true, Call: runtimeReadGroup},
		{Name: "cohort_data", Public: true, ModelOnly: true, ReadOnly: true, Call: runtimeCohortData},
		{Name: "find_or_create", Public: true, ModelOnly: true, ReadOnly: false, Call: runtimeFindOrCreate},
		{Name: "copy_data", Public: true, ReadOnly: true, UsesPathID: true, Call: runtimeCopyData},
		{Name: "copy", Public: true, UsesPathID: true, Call: runtimeCopy},
		{Name: "exists", Public: true, ReadOnly: true, UsesPathID: true, Call: runtimeExists},
		{Name: "has_access", Public: true, ReadOnly: true, UsesPathID: true, Call: runtimeHasAccess},
		{Name: "check_access_rights", Public: true, ModelOnly: true, ReadOnly: true, UsesPathID: true, Call: runtimeCheckAccessRights},
		{Name: "toggle_active", Public: true, UsesPathID: true, Call: runtimeToggleActive},
		{Name: "update_field_translations", Public: true, UsesPathID: true, Call: runtimeUpdateFieldTranslations},
		{Name: "onchange", Public: true, ModelOnly: true, ReadOnly: true, Call: runtimeOnchange},
		{Name: "_get_access_action", Public: false, ReadOnly: true, Call: runtimeNotImplemented("_get_access_action")},
		{Name: "get_formview_action", Public: true, ReadOnly: true, Call: runtimeNotImplemented("get_formview_action")},
		{Name: "_get_records_action", Public: false, ReadOnly: true, Call: runtimeNotImplemented("_get_records_action")},
		{Name: "get_empty_list_help", Public: true, ModelOnly: true, ReadOnly: true, Call: runtimeGetEmptyListHelp},
		{Name: "get_helper", Public: true, ModelOnly: true, ReadOnly: true, Call: runtimeGetHelper},
		{Name: "get_json_dto", Public: true, ModelOnly: true, ReadOnly: true, Call: runtimeGetJSONDTO},
		{Name: "view_header_get", Public: true, ModelOnly: true, ReadOnly: true, Call: runtimeViewHeaderGet},
		{Name: "_get_default_form_view", Public: false, ModelOnly: true, ReadOnly: true, Call: runtimeNotImplemented("_get_default_form_view")},
		{Name: "_get_default_search_view", Public: false, ModelOnly: true, ReadOnly: true, Call: runtimeNotImplemented("_get_default_search_view")},
		{Name: "_get_default_list_view", Public: false, ModelOnly: true, ReadOnly: true, Call: runtimeNotImplemented("_get_default_list_view")},
		{Name: "_get_default_pivot_view", Public: false, ModelOnly: true, ReadOnly: true, Call: runtimeNotImplemented("_get_default_pivot_view")},
		{Name: "_get_default_kanban_view", Public: false, ModelOnly: true, ReadOnly: true, Call: runtimeNotImplemented("_get_default_kanban_view")},
		{Name: "_get_default_graph_view", Public: false, ModelOnly: true, ReadOnly: true, Call: runtimeNotImplemented("_get_default_graph_view")},
		{Name: "_get_default_calendar_view", Public: false, ModelOnly: true, ReadOnly: true, Call: runtimeNotImplemented("_get_default_calendar_view")},
	}
}

func runtimeFieldsGet(_ context.Context, model *ModelMetadata, _ []string, args []any, kwargs map[string]any) (any, error) {
	if model == nil {
		log.Errorf("orm: model metadata is required")
		return nil, fmt.Errorf("orm: model metadata is required")
	}

	// Resolve caller-requested field names first so aliases and Go names map
	// back to the canonical registry field keys.
	requested, err := runtimeResolvedFieldNames(model, requestedRuntimeFields(args, kwargs))
	if err != nil {
		return nil, err
	}
	if len(requested) == 0 {
		// No filter means "describe every field the model exposes".
		fields := make(map[string]FieldMetadata, len(model.Fields))
		for _, field := range model.Fields {
			field.HasOnchange = OnchangeTriggerField(model.Name, field.Name)
			fields[field.Name] = field
		}
		return fields, nil
	}

	fields := make(map[string]FieldMetadata, len(requested))
	for _, requestedField := range requested {
		field, ok := runtimeLookupField(model, requestedField)
		if !ok {
			// Missing fields are skipped so partial field lists do not fail the
			// entire metadata query.
			continue
		}
		field.HasOnchange = OnchangeTriggerField(model.Name, field.Name)
		fields[field.Name] = field
	}
	return fields, nil
}

func runtimeDefaultGet(ctx context.Context, model *ModelMetadata, _ []string, args []any, kwargs map[string]any) (any, error) {
	if model == nil {
		log.Errorf("orm: model metadata is required")
		return nil, fmt.Errorf("orm: model metadata is required")
	}

	requested, err := runtimeResolvedFieldNames(model, requestedRuntimeFields(args, kwargs))
	if err != nil {
		return nil, err
	}
	requestedSet := make(map[string]struct{}, len(requested))
	for _, name := range requested {
		requestedSet[name] = struct{}{}
	}

	// Build defaults only from fields that declare one in metadata.
	defaults := make(map[string]any)
	for _, field := range model.Fields {
		if len(requestedSet) > 0 {
			if _, ok := requestedSet[field.Name]; !ok && !runtimeFieldRequestedByGoName(requestedSet, field.GoName) {
				continue
			}
		}
		if field.Default == "" {
			continue
		}
		// Defaults are stored as strings in metadata and parsed lazily here into
		// runtime-friendly values.
		defaults[field.Name] = parseRuntimeDefaultValue(field)
	}
	// use_sequence fields get a non-reserved preview of their next number.
	previewSequenceFields(ctx, model, requestedSet, defaults)

	if err := injectImplicitCompanyDefaults(ctx, model, defaults, requestedSet); err != nil {
		return nil, err
	}
	return defaults, nil
}

func runtimeGetEmptyListHelp(_ context.Context, model *ModelMetadata, _ []string, _ []any, _ map[string]any) (any, error) {
	if model == nil {
		log.Errorf("orm: model metadata is required")
		return "", fmt.Errorf("orm: model metadata is required")
	}
	if strings.TrimSpace(model.Description) != "" {
		return model.Description, nil
	}
	return model.Label, nil
}

// runtimeGetHelper returns the stored helper HTML for the current model and
// falls back to the live registry metadata when the sys.model row is missing.
func runtimeGetHelper(ctx context.Context, model *ModelMetadata, _ []string, _ []any, _ map[string]any) (any, error) {
	if model == nil {
		log.Errorf("orm: model metadata is required")
		return "", fmt.Errorf("orm: model metadata is required")
	}
	if helper, _, found, err := runtimeStoredModelArtifacts(ctx, model.Name); err != nil {
		return nil, err
	} else if found && strings.TrimSpace(helper) != "" {
		return helper, nil
	}
	return BuildModelHelperHTML(*model), nil
}

// runtimeGetJSONDTO returns the stored DTO for the current model and falls
// back to the live registry metadata when the sys.model row is missing.
func runtimeGetJSONDTO(ctx context.Context, model *ModelMetadata, _ []string, _ []any, _ map[string]any) (any, error) {
	if model == nil {
		log.Errorf("orm: model metadata is required")
		return nil, fmt.Errorf("orm: model metadata is required")
	}
	if _, dto, found, err := runtimeStoredModelArtifacts(ctx, model.Name); err != nil {
		return nil, err
	} else if found && dto != nil {
		return dto, nil
	}
	return BuildModelJSONDTO(*model), nil
}

func runtimeSearch(ctx context.Context, model *ModelMetadata, _ []string, args []any, kwargs map[string]any) (any, error) {
	// Search delegates all domain/filter parsing to the shared helper so count,
	// read, and fetch variants stay behaviorally aligned.
	filter, err := runtimeSearchFilter(ctx, model, args, kwargs)
	if err != nil {
		return nil, err
	}
	applyClientReadCap(ctx, model, filter)
	return runtimeRepositorySearch(ctx, model, filter)
}

func runtimeSearchCount(ctx context.Context, model *ModelMetadata, _ []string, args []any, kwargs map[string]any) (any, error) {
	filter, err := runtimeSearchFilter(ctx, model, args, kwargs)
	if err != nil {
		return nil, err
	}
	return runtimeRepositorySearchCount(ctx, model, filter)
}

func runtimeRead(ctx context.Context, model *ModelMetadata, ids []string, args []any, kwargs map[string]any) (any, error) {
	resolvedIDs, err := runtimeIDsArg(ids, args, kwargs)
	if err != nil {
		return nil, err
	}
	if len(resolvedIDs) == 0 {
		// Read is record-oriented, so an empty id set is treated as a caller error.
		log.Errorf("orm: read requires at least one id")
		return nil, fmt.Errorf("orm: read requires at least one id")
	}
	if err := ensureRecordRuleAccess(ctx, model, "read", resolvedIDs); err != nil {
		return nil, err
	}

	// Field selection is optional; an empty slice lets the repository decide the
	// default projection shape.
	fields, err := runtimeResolvedFieldNames(model, runtimeFieldsFromTail(args, kwargs))
	if err != nil {
		return nil, err
	}
	return runtimeRepositoryRead(ctx, model, resolvedIDs, fields)
}

func runtimeSearchRead(ctx context.Context, model *ModelMetadata, _ []string, args []any, kwargs map[string]any) (any, error) {
	filter, err := runtimeSearchFilter(ctx, model, args, kwargs)
	if err != nil {
		return nil, err
	}
	applyClientReadCap(ctx, model, filter)

	fields, err := runtimeResolvedFieldNames(model, runtimeFieldsFromTail(args, kwargs))
	if err != nil {
		return nil, err
	}
	return runtimeRepositorySearchRead(ctx, model, filter, fields)
}

func runtimeSearchFetch(ctx context.Context, model *ModelMetadata, _ []string, args []any, kwargs map[string]any) (any, error) {
	filter, err := runtimeSearchFilter(ctx, model, args, kwargs)
	if err != nil {
		return nil, err
	}
	applyClientReadCap(ctx, model, filter)

	fields, err := runtimeResolvedFieldNames(model, runtimeFieldsFromTail(args, kwargs))
	if err != nil {
		return nil, err
	}
	return runtimeRepositorySearchRead(ctx, model, filter, fields)
}

func runtimeCreate(ctx context.Context, model *ModelMetadata, _ []string, args []any, kwargs map[string]any) (any, error) {
	values, err := runtimeRecordMapArg(args, kwargs, "values")
	if err != nil {
		return nil, err
	}
	// One guard sequence for the default method and for overrides (CreateRecord).
	return CreateRecord(ctx, model, values)
}

func runtimeWrite(ctx context.Context, model *ModelMetadata, ids []string, args []any, kwargs map[string]any) (any, error) {
	resolvedIDs, err := runtimeIDsArg(ids, nil, kwargs)
	if err != nil {
		return nil, err
	}
	values, err := runtimeRecordMapArg(args, kwargs, "values")
	if err != nil {
		return nil, err
	}
	// One guard sequence for the default method and for overrides (WriteRecords).
	return WriteRecords(ctx, model, resolvedIDs, values)
}

func runtimeUnlink(ctx context.Context, model *ModelMetadata, ids []string, args []any, kwargs map[string]any) (any, error) {
	resolvedIDs, err := runtimeIDsArg(ids, args, kwargs)
	if err != nil {
		return nil, err
	}
	if err := ensureRecordRuleAccess(ctx, model, "unlink", resolvedIDs); err != nil {
		return nil, err
	}
	return runtimeRepositoryUnlink(ctx, model, resolvedIDs)
}

// KNOWN GAP: unlike name_get/copy_data/toggle_active (which now call
// ensureRecordRuleAccess), this still reads by raw id with no record-rule/
// company-domain check, so it can confirm a record's existence in a company
// the caller cannot see. Left unchanged because every registered-executor
// dispatch test (see internal/modelruntime/service_test.go and this
// package's own model_registry_test.go) configures its stub executor to
// return one fixed canned result per call regardless of filter content —
// routing exists() through the search/domain path breaks that contract
// there. Fixing this needs those stub tests reworked first, not just this
// function.
func runtimeExists(ctx context.Context, model *ModelMetadata, ids []string, args []any, kwargs map[string]any) (any, error) {
	resolvedIDs, err := runtimeIDsArg(ids, args, kwargs)
	if err != nil {
		return nil, err
	}
	if len(resolvedIDs) == 0 {
		return []any{}, nil
	}

	records, err := runtimeRepositoryRead(ctx, model, resolvedIDs, []string{"id"})
	if err != nil {
		return nil, err
	}

	existing := make([]any, 0, len(records))
	for _, record := range records {
		if id, ok := record["id"]; ok {
			existing = append(existing, id)
		}
	}
	return existing, nil
}

func runtimeToggleActive(ctx context.Context, model *ModelMetadata, ids []string, args []any, kwargs map[string]any) (any, error) {
	resolvedIDs, err := runtimeIDsArg(ids, args, kwargs)
	if err != nil {
		return nil, err
	}
	if len(resolvedIDs) == 0 {
		log.Errorf("orm: toggle_active requires at least one id")
		return false, fmt.Errorf("orm: toggle_active requires at least one id")
	}
	// toggle_active mutates records by raw id outside the normal write() path
	// (both the executor-native branch and the generic read+write fallback
	// below), so the record-rule/company scope must be checked explicitly here
	// — otherwise a caller with model-level write ACL could flip active/
	// inactive on another company's records.
	if err := ensureRecordRuleAccess(ctx, model, "write", resolvedIDs); err != nil {
		return nil, err
	}

	// Prefer an executor-native toggle path when one exists so models that
	// embed BaseModel can apply their ToggleActive method before persistence.
	executor, err := requireRuntimeExecutor(model)
	if err != nil {
		return nil, err
	}
	if toggleExecutor, ok := executor.(interface {
		RuntimeToggleActive(context.Context, []string) (bool, error)
	}); ok {
		return toggleExecutor.RuntimeToggleActive(ctx, resolvedIDs)
	}

	activeField, err := runtimeActiveFieldName(model)
	if err != nil {
		return nil, err
	}

	records, err := runtimeRepositoryRead(ctx, model, resolvedIDs, []string{"id", activeField})
	if err != nil {
		return nil, err
	}

	enableIDs := make([]string, 0, len(records))
	disableIDs := make([]string, 0, len(records))
	for _, record := range records {
		id, ok := runtimeStringID(record["id"])
		if !ok {
			continue
		}

		isActive, _ := record[activeField].(bool)
		if isActive {
			disableIDs = append(disableIDs, id)
			continue
		}
		enableIDs = append(enableIDs, id)
	}

	updated := false
	if len(disableIDs) > 0 {
		success, err := runtimeRepositoryWrite(ctx, model, disableIDs, RecordMap{activeField: false})
		if err != nil {
			return nil, err
		}
		updated = updated || success
	}
	if len(enableIDs) > 0 {
		success, err := runtimeRepositoryWrite(ctx, model, enableIDs, RecordMap{activeField: true})
		if err != nil {
			return nil, err
		}
		updated = updated || success
	}
	return updated, nil
}

func runtimeNameCreate(ctx context.Context, model *ModelMetadata, _ []string, args []any, _ map[string]any) (any, error) {
	name := ""
	if len(args) > 0 {
		name, _ = args[0].(string)
	}
	if strings.TrimSpace(name) == "" {
		log.Errorf("orm: name_create requires name")
		return nil, fmt.Errorf("orm: name_create requires name")
	}
	values := RecordMap{model.RecName: name}
	created, err := runtimeRepositoryCreate(ctx, model, values)
	if err != nil {
		return nil, err
	}
	return [2]any{created["id"], created[model.RecName]}, nil
}

func runtimeNameSearch(ctx context.Context, model *ModelMetadata, _ []string, args []any, kwargs map[string]any) (any, error) {
	name := ""
	if len(args) > 0 {
		name, _ = args[0].(string)
	}
	filter, err := runtimeSearchFilter(ctx, model, args[1:], kwargs)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(name) != "" {
		filter["__name"] = name
	}
	filter["__limit"] = runtimeIntArg(args, kwargs, "limit", 3, 10)
	applyClientReadCap(ctx, model, filter)
	records, err := runtimeRepositorySearchRead(ctx, model, filter, []string{"id", model.RecName})
	if err != nil {
		return nil, err
	}
	result := make([][2]any, 0, len(records))
	for _, record := range records {
		result = append(result, [2]any{record["id"], record[model.RecName]})
	}
	return result, nil
}

func runtimeNameGet(ctx context.Context, model *ModelMetadata, ids []string, args []any, kwargs map[string]any) (any, error) {
	resolvedIDs, err := runtimeIDsArg(ids, args, kwargs)
	if err != nil {
		return nil, err
	}
	// name_get reads by raw id and bypasses the search-path domain filter, so the
	// record-rule/company scope must be enforced explicitly here — otherwise a
	// caller could resolve the display name of a record in a company they are
	// not authorized for (e.g. through a many2one reference field).
	if err := ensureRecordRuleAccess(ctx, model, "read", resolvedIDs); err != nil {
		return nil, err
	}
	records, err := runtimeRepositoryRead(ctx, model, resolvedIDs, []string{"id", model.RecName})
	if err != nil {
		return nil, err
	}
	result := make([][2]any, 0, len(records))
	for _, record := range records {
		result = append(result, [2]any{record["id"], record[model.RecName]})
	}
	return result, nil
}

func runtimeReadGroup(ctx context.Context, model *ModelMetadata, _ []string, args []any, kwargs map[string]any) (any, error) {
	filter, err := runtimeSearchFilter(ctx, model, args, kwargs)
	if err != nil {
		return nil, err
	}
	groupBy := runtimeFieldNames(kwargs["groupby"])
	aggregates := runtimeFieldsFromTail(args, kwargs)
	// Group keys and sums would expose a secret column's values (plan T19).
	if err := ensureNoSecretFieldNames(ctx, model, append(append([]string(nil), groupBy...), aggregates...)); err != nil {
		return nil, err
	}
	records, err := runtimeRepositorySearchRead(ctx, model, filter, nil)
	if err != nil {
		return nil, err
	}
	return runtimeGroupRecords(records, groupBy, aggregates), nil
}

func runtimeCopyData(ctx context.Context, model *ModelMetadata, ids []string, args []any, kwargs map[string]any) (any, error) {
	resolvedIDs, err := runtimeIDsArg(ids, args, kwargs)
	if err != nil {
		return nil, err
	}
	// copy_data reads the full field set by raw id, so without an explicit
	// record-rule check here a caller could exfiltrate another company's record
	// values (and copy() would go on to duplicate them into its own company).
	if err := ensureRecordRuleAccess(ctx, model, "read", resolvedIDs); err != nil {
		return nil, err
	}
	records, err := runtimeRepositoryRead(ctx, model, resolvedIDs, nil)
	if err != nil {
		return nil, err
	}
	for index := range records {
		delete(records[index], "id")
	}
	return records, nil
}

func runtimeCopy(ctx context.Context, model *ModelMetadata, ids []string, args []any, kwargs map[string]any) (any, error) {
	payload, err := runtimeCopyData(ctx, model, ids, args, kwargs)
	if err != nil {
		return nil, err
	}
	rows, _ := payload.([]RecordMap)
	created := make([]RecordMap, 0, len(rows))
	for _, row := range rows {
		preparedRow, err := runtimePrepareCopyCreateRow(model, row)
		if err != nil {
			return nil, err
		}
		// A copy is a create: run ModelSpec.Prepare (HTML sanitising, slug/validation rules) like
		// CreateRecord does, so copy cannot duplicate content past a model's create rules. The
		// read row holds stored representations, not client input, so fields Prepare/the guards
		// would refuse are dropped first: computed values (the create recomputes them) and secret
		// values (sealed ciphertext a Prepare rejects as input; a copy must not clone credentials).
		preparedRow, err = prepareGuardedValues(ctx, WriteOp{Model: model, Create: true, Values: dropUncopyableFieldValues(model, preparedRow)})
		if err != nil {
			return nil, err
		}
		record, err := runtimeRepositoryCreate(ctx, model, preparedRow)
		if err != nil {
			return nil, err
		}
		created = append(created, record)
	}
	if len(created) == 1 {
		return created[0], nil
	}
	return created, nil
}

func runtimePrepareCopyCreateRow(model *ModelMetadata, row RecordMap) (RecordMap, error) {
	if model == nil {
		return nil, fmt.Errorf("orm: model metadata is required")
	}
	cloned := cloneRecordMap(row)
	if len(cloned) == 0 {
		return cloned, nil
	}
	suffix := ""
	for _, field := range model.Fields {
		if !runtimeCopyFieldNeedsSuffix(field) {
			continue
		}
		value, ok := cloned[field.Name]
		if !ok {
			continue
		}
		text, ok := value.(string)
		if !ok || strings.TrimSpace(text) == "" {
			continue
		}
		if suffix == "" {
			// 8 random bytes -> 16 hex chars: wide enough that copied unique
			// text values cannot realistically collide with a prior copy.
			nextSuffix, err := utils.RandomHexString(8)
			if err != nil {
				return nil, err
			}
			suffix = nextSuffix
		}
		cloned[field.Name] = runtimeAppendCopySuffix(text, suffix)
	}
	return cloned, nil
}

func runtimeCopyFieldNeedsSuffix(field FieldMetadata) bool {
	if field.PrimaryKey || field.ReadOnly {
		return false
	}
	if !field.Unique && !field.Indexed {
		return false
	}
	switch field.Kind {
	case FieldKindText, FieldKindHTML:
		return true
	default:
		return false
	}
}

func runtimeAppendCopySuffix(value string, suffix string) string {
	if suffix == "" {
		return value
	}
	trimmed := strings.TrimSpace(value)
	if at := strings.LastIndex(trimmed, "@"); at > 0 && at < len(trimmed)-1 && strings.Count(trimmed, "@") == 1 {
		return trimmed[:at] + "+" + suffix + trimmed[at:]
	}
	return trimmed + "-" + suffix
}

func runtimeHasAccess(ctx context.Context, model *ModelMetadata, ids []string, args []any, kwargs map[string]any) (any, error) {
	operation := runtimeStringArg(args, kwargs, "operation", 0)
	if operation == "" {
		operation = "read"
	}
	// Validate the invariants for this stage before the function commits to the next step.
	err := ensureModelAccess(ctx, EnvironmentFromContext(ctx), model, operation, ids, nil)
	return err == nil, nil
}

func runtimeCheckAccessRights(ctx context.Context, model *ModelMetadata, ids []string, args []any, kwargs map[string]any) (any, error) {
	operation := runtimeStringArg(args, kwargs, "operation", 0)
	if operation == "" {
		operation = "read"
	}
	if err := ensureModelAccess(ctx, EnvironmentFromContext(ctx), model, operation, ids, nil); err != nil {
		if raise, ok := kwargs["raise_exception"].(bool); ok && !raise {
			return false, nil
		}
		return nil, err
	}
	return true, nil
}

func runtimeOnchange(ctx context.Context, model *ModelMetadata, _ []string, args []any, kwargs map[string]any) (any, error) {
	values, err := runtimeRecordMapArg(args, kwargs, "values")
	if err != nil {
		return nil, err
	}
	if values == nil {
		values = RecordMap{}
	}
	changed := runtimeFieldNames(valueAt(args, 1, kwargs["field_names"]))
	fieldsSpec, _ := kwargs["fields_spec"].(map[string]any)
	runtimeHookRegistry.mu.RLock()
	hooks := append([]registeredOnchange(nil), runtimeHookRegistry.onchanges[model.Name]...)
	runtimeHookRegistry.mu.RUnlock()
	result := RecordMap{}
	records := NewRecordSet(model, EnvironmentFromContext(ctx), nil)
	for _, hook := range hooks {
		if len(hook.fields) > 0 && !runtimeStringSetsIntersect(hook.fields, changed) {
			continue
		}
		update, err := hook.handler(ctx, records, values, changed, fieldsSpec)
		if err != nil {
			return nil, err
		}
		for key, value := range update {
			result[key] = value
		}
	}
	return map[string]any{"value": result}, nil
}

func runtimeViewHeaderGet(_ context.Context, model *ModelMetadata, _ []string, _ []any, _ map[string]any) (any, error) {
	if model == nil {
		log.Errorf("orm: model metadata is required")
		return "", fmt.Errorf("orm: model metadata is required")
	}
	if strings.TrimSpace(model.Label) != "" {
		return model.Label, nil
	}
	return model.Name, nil
}

func runtimeNotImplemented(name string) RuntimeMethodFunc {
	return func(_ context.Context, model *ModelMetadata, _ []string, _ []any, _ map[string]any) (any, error) {
		modelName := ""
		if model != nil {
			modelName = model.Name
		}
		log.Errorf("%v: %s on model %q", ErrRuntimeMethodNotImplemented, name, modelName)
		return nil, fmt.Errorf("%v: %s on model %q", ErrRuntimeMethodNotImplemented, name, modelName)
	}
}

func requestedRuntimeFields(args []any, kwargs map[string]any) []string {
	if len(args) > 0 {
		if fields := runtimeFieldNames(args[0]); len(fields) > 0 {
			return fields
		}
	}
	if kwargs != nil {
		if fields := runtimeFieldNames(kwargs["fields"]); len(fields) > 0 {
			return fields
		}
	}
	return nil
}

func runtimeFieldNames(value any) []string {
	switch typed := value.(type) {
	case []string:
		return append([]string(nil), typed...)
	case []any:
		fields := make([]string, 0, len(typed))
		for _, item := range typed {
			name, _ := item.(string)
			if strings.TrimSpace(name) != "" {
				fields = append(fields, name)
			}
		}
		return fields
	default:
		return nil
	}
}

func runtimeFieldRequestedByGoName(requested map[string]struct{}, goName string) bool {
	_, ok := requested[goName]
	return ok
}

// parseRuntimeDefaultValue parses the runtime default value handled by this package.
func parseRuntimeDefaultValue(field FieldMetadata) any {
	raw := field.Default
	if field.Kind == FieldKindJSON {
		var decoded any
		// JSONB defaults are stored as JSON text in metadata so default_get and
		// runtime create can materialize real object/array values instead of the
		// raw string literal.
		if err := json.Unmarshal([]byte(raw), &decoded); err == nil {
			return decoded
		}
	}
	switch field.Type {
	case "bool":
		if parsed, err := strconv.ParseBool(strings.ToLower(raw)); err == nil {
			return parsed
		}
	case "string":
		return raw
	case "int", "int8", "int16", "int32", "int64":
		if parsed, err := strconv.ParseInt(raw, 10, 64); err == nil {
			return parsed
		}
	case "uint", "uint8", "uint16", "uint32", "uint64":
		if parsed, err := strconv.ParseUint(raw, 10, 64); err == nil {
			return parsed
		}
	case "float32", "float64":
		if parsed, err := strconv.ParseFloat(raw, 64); err == nil {
			return parsed
		}
	}
	return raw
}

func runtimeFieldsFromTail(args []any, kwargs map[string]any) []string {
	if len(args) > 1 {
		if fields := runtimeFieldNames(args[1]); len(fields) > 0 {
			return fields
		}
	}
	if kwargs != nil {
		if fields := runtimeFieldNames(kwargs["fields"]); len(fields) > 0 {
			return fields
		}
	}
	return nil
}

func runtimeResolvedFieldNames(model *ModelMetadata, requested []string) ([]string, error) {
	if len(requested) == 0 {
		return nil, nil
	}

	resolved := make([]string, 0, len(requested))
	seen := make(map[string]struct{}, len(requested))
	for _, name := range requested {
		field, ok := runtimeLookupField(model, name)
		if !ok {
			log.Errorf("orm: field %q is not registered on model %q", name, model.Name)
			return nil, fmt.Errorf("orm: field %q is not registered on model %q", name, model.Name)
		}
		if _, exists := seen[field.Name]; exists {
			continue
		}
		seen[field.Name] = struct{}{}
		resolved = append(resolved, field.Name)
	}
	return resolved, nil
}

func normalizePayload(model *ModelMetadata, values RecordMap) (RecordMap, error) {
	// Canonicalize field names so struct aliases, JSON keys, and logical names
	// all collapse to one representation before the ORM layer processes values.
	// False-value coercion and type conversion happen inside RuntimeCreate/Write
	// so every mutation path shares a single, consistent normalization rule set.
	return runtimeCanonicalRecordMap(model, values)
}

// rejectComputedFieldValues enforces the SRS-F-018 write contract: computed
// fields are produced by registered handlers only, so accepting a client value
// would either be silently discarded (non-stored) or silently overwritten by
// the next recompute (stored) — both hide caller bugs. Related-chain fields
// keep their existing recompute-wins behavior and are not rejected here.
func rejectComputedFieldValues(model *ModelMetadata, values RecordMap) error {
	for key := range values {
		field, ok := runtimeLookupField(model, key)
		if !ok {
			continue
		}
		if strings.TrimSpace(field.Compute) == "" || len(field.Related) > 0 {
			continue
		}
		log.Errorf("orm: field %q on %s is computed and read-only", field.Name, model.Name)
		return fmt.Errorf("orm: field %q on %s is computed and read-only", field.Name, model.Name)
	}
	return nil
}

// dropUncopyableFieldValues removes, from a payload built from a read row, the computed values
// rejectComputedFieldValues would refuse (computed, non-related) and every secret field value.
func dropUncopyableFieldValues(model *ModelMetadata, values RecordMap) RecordMap {
	for key := range values {
		field, ok := runtimeLookupField(model, key)
		if ok && (field.Secret || (strings.TrimSpace(field.Compute) != "" && len(field.Related) == 0)) {
			delete(values, key)
		}
	}
	return values
}

func runtimeCanonicalRecordMap(model *ModelMetadata, values RecordMap) (RecordMap, error) {
	if values == nil {
		return nil, nil
	}

	out := make(RecordMap, len(values))
	for key, value := range values {
		field, ok := runtimeLookupField(model, key)
		if !ok {
			// Silently skip unregistered fields — callers should not need to strip
			// unknown keys before sending a payload; type coercion and validation
			// happen at the ORM create/write layer, not at the API receiving level.
			continue
		}
		out[field.Name] = value
	}
	return out, nil
}

func runtimeNormalizeFalseValues(model *ModelMetadata, values RecordMap) (RecordMap, error) {
	if values == nil {
		return nil, nil
	}

	normalized := make(RecordMap, len(values))
	for key, value := range values {
		field, ok := runtimeLookupField(model, key)
		if !ok {
			normalized[key] = value
			continue
		}

		nextValue, keep, err := runtimeNormalizeFieldFalseValue(field, value)
		if err != nil {
			return nil, err
		}
		if !keep {
			continue
		}
		normalized[field.Name] = nextValue
	}
	return normalized, nil
}

func runtimeNormalizeFieldFalseValue(field FieldMetadata, value any) (any, bool, error) {
	// (b) boolean: explicit false is a real value for boolean fields, so keep
	// it and coerce string/numeric checkbox payloads into the correct Go bool
	// before Bun serializes them.
	if field.Kind == FieldKindBoolean || field.Type == "bool" {
		return normalizeRuntimeBooleanValue(value), true, nil
	}

	// (a), (c): raw false sentinel becomes null for every other field kind so
	// API clients can clear relation and scalar fields without sending nil.
	if boolValue, ok := value.(bool); ok && !boolValue {
		return nil, true, nil
	}

	switch field.Kind {
	case FieldKindManyToOne, FieldKindOneToOne:
		// (a) scalar relation: null is unchanged; otherwise try to extract the
		// integer FK value so display tuples like [id, name] collapse cleanly.
		if value == nil {
			return nil, true, nil
		}
		if relationID, ok := relatedRecordID(value); ok {
			return normalizeRuntimeIntegerValue(relationID), true, nil
		}
		return normalizeRuntimeIntegerValue(value), true, nil

	case FieldKindOneToMany, FieldKindManyToMany:
		// (a) collection relation: null is unchanged; otherwise process as x2many
		// commands and recursively normalize nested create/update value maps.
		if value == nil {
			return nil, true, nil
		}
		commands := normalizeX2ManyCommandsForField(field, value)
		if len(commands) == 0 {
			return value, true, nil
		}
		relationModel, ok := LookupModel(field.Relation)
		if !ok {
			return commands, true, nil
		}
		for i, command := range commands {
			if len(command.values) == 0 {
				continue
			}
			normalizedValues, err := runtimeNormalizeFalseValues(relationModel, command.values)
			if err != nil {
				return nil, false, err
			}
			commands[i].values = normalizedValues
		}
		return commands, true, nil
	}

	// (c) all other field kinds: value is already non-false, pass through unchanged.
	return value, true, nil
}

func runtimeLookupField(model *ModelMetadata, requested string) (FieldMetadata, bool) {
	if model == nil {
		return FieldMetadata{}, false
	}

	needle := strings.TrimSpace(requested)
	if needle == "" {
		return FieldMetadata{}, false
	}

	for _, field := range model.Fields {
		switch needle {
		case field.Name, field.GoName, field.JSONName, field.BunColumn:
			return field, true
		}
	}
	return FieldMetadata{}, false
}

func runtimeActiveFieldName(model *ModelMetadata) (string, error) {
	if model == nil {
		log.Errorf("orm: model metadata is required")
		return "", fmt.Errorf("orm: model metadata is required")
	}

	if activeField, ok := runtimeOptionalActiveFieldName(model); ok {
		return activeField, nil
	}
	log.Errorf("orm: model %q does not expose a boolean active field", model.Name)
	return "", fmt.Errorf("orm: model %q does not expose a boolean active field", model.Name)
}

// runtimeOptionalActiveFieldName probes for the conventional active flag
// without logging when a model intentionally does not support archive
// semantics. Callers that only want optional active-test behavior should use
// this helper to avoid noisy logs on metadata models such as sys.model.data.
func runtimeOptionalActiveFieldName(model *ModelMetadata) (string, bool) {
	if model == nil {
		return "", false
	}

	// Walk each string{"active", "is active"} entry in order so validation and
	// side effects stay scoped to the current element.
	for _, candidate := range []string{"active", "is_active"} {
		field, ok := runtimeLookupField(model, candidate)
		if ok && (field.Kind == FieldKindBoolean || field.Type == "bool") {
			return field.Name, true
		}
	}
	return "", false
}

func runtimeStringID(value any) (string, bool) {
	switch typed := value.(type) {
	case string:
		trimmed := strings.TrimSpace(typed)
		return trimmed, trimmed != ""
	case fmt.Stringer:
		trimmed := strings.TrimSpace(typed.String())
		return trimmed, trimmed != ""
	case int:
		return strconv.Itoa(typed), true
	case int8:
		return strconv.FormatInt(int64(typed), 10), true
	case int16:
		return strconv.FormatInt(int64(typed), 10), true
	case int32:
		return strconv.FormatInt(int64(typed), 10), true
	case int64:
		return strconv.FormatInt(typed, 10), true
	case uint:
		return strconv.FormatUint(uint64(typed), 10), true
	case uint8:
		return strconv.FormatUint(uint64(typed), 10), true
	case uint16:
		return strconv.FormatUint(uint64(typed), 10), true
	case uint32:
		return strconv.FormatUint(uint64(typed), 10), true
	case uint64:
		return strconv.FormatUint(typed, 10), true
	case float32:
		return strconv.FormatInt(int64(typed), 10), true
	case float64:
		return strconv.FormatInt(int64(typed), 10), true
	default:
		return "", false
	}
}

func runtimeSearchFilter(ctx context.Context, model *ModelMetadata, args []any, kwargs map[string]any) (RecordMap, error) {
	filter, err := runtimeRecordMapArg(args, kwargs, "filter")
	if err != nil {
		return nil, err
	}
	if filter == nil {
		filter = RecordMap{}
	}
	if domainRaw := valueAt(args, 0, kwargs["domain"]); domainRaw != nil {
		domain, err := ParseDomain(domainRaw)
		if err != nil {
			return nil, err
		}
		filter["__domain"] = domain
	}
	if order := runtimeStringArg(args, kwargs, "order", 2); order != "" {
		filter["__order"] = order
	}
	if offset := runtimeIntArg(args, kwargs, "offset", 1, 0); offset > 0 {
		filter["__offset"] = offset
	}
	if limit := runtimeIntArg(args, kwargs, "limit", 2, 0); limit > 0 {
		filter["__limit"] = limit
	}
	if env := EnvironmentFromContext(ctx); env != nil && env.ActiveTest() {
		if activeField, ok := runtimeOptionalActiveFieldName(model); ok {
			if _, exists := filter[activeField]; !exists {
				filter[activeField] = true
			}
		}
	}
	// Checked on the caller's own filter, before record-rule domains are merged in (plan T19).
	if err := ensureNoSecretFieldsInSearch(ctx, model, filter); err != nil {
		return nil, err
	}
	// Relational paths in the caller's domain respect the target's read ACL and rules (plan T20).
	if domain, ok := filter["__domain"].(DomainNode); ok {
		scoped, err := scopeDomainTraversals(ctx, model, domain)
		if err != nil {
			return nil, err
		}
		filter["__domain"] = scoped
	}
	return applyImplicitRecordRuleDomain(ctx, model, filter, "read")
}

func runtimeStringArg(args []any, kwargs map[string]any, key string, index int) string {
	if kwargs != nil {
		if value, ok := kwargs[key].(string); ok {
			return strings.TrimSpace(value)
		}
	}
	if index >= 0 && index < len(args) {
		if value, ok := args[index].(string); ok {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func runtimeIntArg(args []any, kwargs map[string]any, key string, index int, defaultValue int) int {
	if kwargs != nil {
		if value, ok := kwargs[key]; ok {
			if parsed := int(toInt64(value)); parsed != 0 {
				return parsed
			}
		}
	}
	if index >= 0 && index < len(args) {
		if parsed := int(toInt64(args[index])); parsed != 0 {
			return parsed
		}
	}
	return defaultValue
}

// valueAt returns the at handled by this package.
func valueAt(args []any, index int, fallback any) any {
	if index >= 0 && index < len(args) {
		return args[index]
	}
	return fallback
}

func runtimeStringSetsIntersect(left, right []string) bool {
	for _, item := range left {
		for _, other := range right {
			if item == other {
				return true
			}
		}
	}
	return len(left) == 0
}

func runtimeGroupRecords(records []RecordMap, groupBy []string, aggregates []string) []RecordMap {
	if len(groupBy) == 0 {
		return []RecordMap{{"__count": len(records)}}
	}
	grouped := map[string]RecordMap{}
	for _, record := range records {
		keyParts := make([]string, 0, len(groupBy))
		for _, field := range groupBy {
			keyParts = append(keyParts, fmt.Sprint(record[field]))
		}
		key := strings.Join(keyParts, "|")
		bucket := grouped[key]
		if bucket == nil {
			bucket = RecordMap{"__count": 0}
			for _, field := range groupBy {
				bucket[field] = record[field]
			}
			for _, spec := range aggregates {
				if strings.HasSuffix(spec, ":sum") {
					field := strings.TrimSuffix(spec, ":sum")
					bucket[field+":sum"] = float64(0)
				}
			}
		}
		bucket["__count"] = bucket["__count"].(int) + 1
		for _, spec := range aggregates {
			if strings.HasSuffix(spec, ":sum") {
				field := strings.TrimSuffix(spec, ":sum")
				// Use a float-preserving conversion here (not toInt64) so decimal
				// measures (e.g. fractional hours on a Grid view) sum correctly
				// instead of being truncated to whole numbers before accumulation.
				bucket[field+":sum"] = bucket[field+":sum"].(float64) + toFloat64(record[field])
			}
		}
		grouped[key] = bucket
	}
	out := make([]RecordMap, 0, len(grouped))
	for _, bucket := range grouped {
		out = append(out, bucket)
	}
	return out
}

// RuntimeDefaultGet exposes the generic default_get (metadata defaults, sequence
// previews, implicit company) so a module's default_get override can extend it.
func RuntimeDefaultGet(ctx context.Context, model *ModelMetadata, ids []string, args []any, kwargs map[string]any) (any, error) {
	return runtimeDefaultGet(ctx, model, ids, args, kwargs)
}
