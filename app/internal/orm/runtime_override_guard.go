package orm

// File overview:
// - Responsibility: T15 kernel seam: a model that replaces the standard create/write/unlink with its own
//   RuntimeMethod still gets the record-rule and company guards the kernel defaults run.
// - Key symbols: `guardMutationOverride`, `isKernelMutationDefault`.

import (
	"context"
	"reflect"
	"strings"
)

// isKernelMutationDefault reports whether call is the kernel's own create/write/unlink, which already
// runs these guards itself, so the dispatcher does not run them twice.
func isKernelMutationDefault(name string, call RuntimeMethodFunc) bool {
	var kernel RuntimeMethodFunc
	switch name {
	case "create":
		kernel = runtimeCreate
	case "write":
		kernel = runtimeWrite
	case "unlink":
		kernel = runtimeUnlink
	default:
		return false
	}
	return call != nil && reflect.ValueOf(call).Pointer() == reflect.ValueOf(kernel).Pointer()
}

// guardMutationOverride runs before a model's own create/write/unlink override. Overrides usually
// insert through RuntimeRepositoryCreate/Write/Unlink, which only check the model ACL, so without this
// a caller could write or delete rows of a company it cannot see, or create rows in such a company.
//
// It applies the same population the record rules apply to: callers whose environment carries an RPC
// identity (auth mode) or models with an explicit record-rule handler. Sudo and identity-less internal
// callers (seeds, imports, bootstrap) keep today's behaviour. It only checks, it never rewrites the
// payload: company pinning on create stays with the override (orm.ApplyImplicitCompanyOnCreate),
// because several overrides inherit company_id from a parent record instead.
func guardMutationOverride(ctx context.Context, model *ModelMetadata, method RuntimeMethod, ids []string, args []any, kwargs map[string]any) error {
	name := strings.TrimSpace(method.Name)
	switch name {
	case "create", "write", "unlink":
	default:
		return nil
	}
	if model == nil || isKernelMutationDefault(name, method.Call) || !shouldEnforceRecordRules(ctx, model) {
		return nil
	}
	switch name {
	case "write":
		resolvedIDs, err := runtimeIDsArg(ids, nil, kwargs)
		if err != nil {
			return err
		}
		if err := ensureRecordRuleAccess(ctx, model, "write", resolvedIDs); err != nil {
			return err
		}
		// A malformed payload is left to the override, which reports it in its own words.
		values, err := runtimeRecordMapArg(args, kwargs, "values")
		if err != nil {
			return nil
		}
		return validateOverrideCompany(ctx, model, values, "write")
	case "unlink":
		resolvedIDs, err := runtimeIDsArg(ids, args, kwargs)
		if err != nil {
			return err
		}
		return ensureRecordRuleAccess(ctx, model, "unlink", resolvedIDs)
	default: // create
		for _, values := range overrideCreatePayloads(args, kwargs) {
			if err := validateOverrideCompany(ctx, model, values, "create"); err != nil {
				return err
			}
		}
		return nil
	}
}

// validateOverrideCompany checks an explicit company_id in the payload (under any alias of the field)
// against the caller's allowed companies, the same rule the default create/write apply.
func validateOverrideCompany(ctx context.Context, model *ModelMetadata, values RecordMap, operation string) error {
	if values == nil {
		return nil
	}
	canonical, err := normalizePayload(model, values)
	if err != nil {
		return nil
	}
	return validateImplicitCompanyAccess(ctx, model, canonical, operation)
}

// overrideCreatePayloads collects the record maps a create override may receive: one map (positional or
// kwargs "values"/"vals"), or a list of maps (positional or kwargs "vals_list"/"values").
func overrideCreatePayloads(args []any, kwargs map[string]any) []RecordMap {
	var raw []any
	if len(args) > 0 {
		raw = append(raw, args[0])
	}
	for _, key := range []string{"values", "vals", "vals_list"} {
		if value, ok := kwargs[key]; ok {
			raw = append(raw, value)
		}
	}
	var out []RecordMap
	for _, candidate := range raw {
		if list, ok := candidate.([]any); ok {
			for _, item := range list {
				if record, err := anyToRecordMap(item); err == nil && record != nil {
					out = append(out, record)
				}
			}
			continue
		}
		if record, err := anyToRecordMap(candidate); err == nil && record != nil {
			out = append(out, record)
		}
	}
	return out
}
