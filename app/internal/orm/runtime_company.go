package orm

// File overview:
// - Responsibility: implements the metadata-driven ORM/runtime kernel for runtime company, where storage, runtime dispatch, and security rules stay centralized.
// - Key symbols: `applyImplicitRecordRuleDomain`, `ensureRecordRuleAccess`.
// - Pattern: Leans on metadata-driven registration so storage mapping, runtime methods, and behavior hooks stay centralized instead of being scattered across handlers.
// - Pattern: Threads `context.Context` through the main execution paths so cancellation, auth scope, and transaction state can follow the work end to end.

import (
	"context"
	"fmt"
	"git.thebase.vn/base_hub/internal/services/log"
	"slices"
)

const mainCompanyID int64 = 1

func applyImplicitRecordRuleDomain(ctx context.Context, model *ModelMetadata, filter RecordMap, operation string) (RecordMap, error) {
	if model == nil {
		log.Errorf("orm: model metadata is required")
		return filter, fmt.Errorf("orm: model metadata is required")
	}
	if filter == nil {
		filter = RecordMap{}
	}
	if !shouldEnforceRecordRules(ctx, model) {
		return filter, nil
	}
	env := EnvironmentFromContext(ctx)
	domain, err := recordRuleDomain(ctx, env, model, operation)
	if err != nil {
		return nil, err
	}
	if domain.Kind == "" || domain.Kind == DomainKindTrue {
		return filter, nil
	}
	if existing, ok := filter["__domain"].(DomainNode); ok && existing.Kind != "" {
		filter["__domain"] = And(existing, domain)
		return filter, nil
	}
	filter["__domain"] = domain
	return filter, nil
}

// ensureRecordRuleAccess ensures the record rule access handled by this package.
func ensureRecordRuleAccess(ctx context.Context, model *ModelMetadata, operation string, ids []string) error {
	if model == nil || len(ids) == 0 || !shouldEnforceRecordRules(ctx, model) {
		return nil
	}
	filter := RecordMap{}
	if len(ids) == 1 {
		filter["id"] = toInt64(ids[0])
	} else {
		filter["id"] = idsToFilterValues(ids)
	}
	filter, err := applyImplicitRecordRuleDomain(ctx, model, filter, operation)
	if err != nil {
		return err
	}
	count, err := runtimeRepositorySearchCount(ctx, model, filter)
	if err != nil {
		return err
	}
	if count != int64(len(ids)) {
		log.Errorf("permission denied: record rule blocked %s on model %q", operation, model.Name)
		return fmt.Errorf("permission denied: record rule blocked %s on model %q", operation, model.Name)
	}
	return nil
}

func implicitCompanyRuleDomain(env *Environment, model *ModelMetadata, operation string) (DomainNode, error) {
	if env == nil || model == nil || env.IsSudo() {
		return TrueDomain(), nil
	}
	if _, ok := env.ContextValue(RBACContextAuthModeKey); !ok {
		return TrueDomain(), nil
	}

	allowed, err := env.AllowedCompanyIDs()
	if err != nil {
		return DomainNode{}, err
	}
	currentCompanyID, err := env.CompanyID()
	if err != nil {
		return DomainNode{}, err
	}
	switch {
	case model.Name == "tbl.company":
		if len(allowed) == 0 {
			return FalseDomain(), nil
		}
		return Where("id", "in", allowed), nil
	case modelHasField(model, "company_id"):
		sharedReadable := operation == "read"
		sharedWritable := operation == "write" || operation == "unlink"
		allowShared := sharedReadable || (sharedWritable && currentCompanyID == mainCompanyID)
		if len(allowed) == 0 {
			if allowShared {
				return Where("company_id", "=", nil), nil
			}
			return FalseDomain(), nil
		}
		scoped := Where("company_id", "in", allowed)
		if allowShared {
			return Or(Where("company_id", "=", nil), scoped), nil
		}
		return scoped, nil
	default:
		return TrueDomain(), nil
	}
}

func injectImplicitCompanyDefaults(ctx context.Context, model *ModelMetadata, defaults map[string]any, requestedSet map[string]struct{}) error {
	if model == nil || !modelHasField(model, "company_id") {
		return nil
	}
	if len(requestedSet) > 0 {
		if _, ok := requestedSet["company_id"]; !ok && !runtimeFieldRequestedByGoName(requestedSet, "CompanyID") {
			return nil
		}
	}
	if _, exists := defaults["company_id"]; exists {
		return nil
	}
	env := EnvironmentFromContext(ctx)
	if env == nil {
		return nil
	}
	companyID, err := env.CompanyID()
	if err != nil {
		return err
	}
	if companyID > 0 {
		defaults["company_id"] = companyID
	}
	return nil
}

// applyImplicitCreateCompanyValue pins a create payload that names no company to the active
// request company, so new rows land in the company the user is working in. A payload that already
// carries a company_id key keeps it, positive or shared (<= 0): that value is an explicit choice,
// either the user's or one a Prepare/override inherited from a parent record (T41: child company =
// parent company), and overwriting it would silently move the row. validateImplicitCompanyAccess
// then decides whether the caller may create in that company or create shared rows.
func applyImplicitCreateCompanyValue(ctx context.Context, model *ModelMetadata, values RecordMap) error {
	if model == nil || values == nil || !modelHasField(model, "company_id") {
		return nil
	}
	if _, exists := values["company_id"]; exists {
		return nil
	}
	env := EnvironmentFromContext(ctx)
	if env == nil {
		return nil
	}
	companyID, err := env.CompanyID()
	if err != nil {
		return err
	}
	if companyID > 0 {
		values["company_id"] = companyID
	}
	return nil
}

// validateImplicitCompanyAccess validates the implicit company access handled by this package.
func validateImplicitCompanyAccess(ctx context.Context, model *ModelMetadata, values RecordMap, operation string) error {
	if model == nil || values == nil || !modelHasField(model, "company_id") {
		return nil
	}
	env := EnvironmentFromContext(ctx)
	if env == nil || env.IsSudo() {
		return nil
	}
	currentCompanyID, err := env.CompanyID()
	if err != nil {
		return err
	}
	requestedCompanyID := toInt64(values["company_id"])
	if requestedCompanyID <= 0 {
		if _, exists := values["company_id"]; exists && (operation == "create" || operation == "write") && currentCompanyID != mainCompanyID {
			log.Errorf("permission denied: shared records can only be created or edited by company %d", mainCompanyID)
			return fmt.Errorf("permission denied: shared records can only be created or edited by company %d", mainCompanyID)
		}
		return nil
	}
	allowed, err := env.AllowedCompanyIDs()
	if err != nil {
		return err
	}
	if len(allowed) == 0 {
		log.Errorf("permission denied: no active company allows writing model %q", model.Name)
		return fmt.Errorf("permission denied: no active company allows writing model %q", model.Name)
	}
	if !slices.Contains(allowed, requestedCompanyID) {
		log.Errorf("permission denied: company %d is not active for model %q", requestedCompanyID, model.Name)
		return fmt.Errorf("permission denied: company %d is not active for model %q", requestedCompanyID, model.Name)
	}
	return nil
}

// ApplyImplicitCompanyOnCreate pins (only when values has no company_id key) and validates
// values["company_id"] the same way the guarded CreateRecord does (applyImplicitCreateCompanyValue
// then validateImplicitCompanyAccess). A model whose ModelSpec overrides "create"
// with its own RuntimeMethod/StandardHooks.Create.Call and inserts via
// RuntimeRepositoryCreate directly bypasses that generic dispatcher entirely, so a
// newly added company_id field would otherwise never get scoped. Call this before
// RuntimeRepositoryCreate in any such custom create handler.
func ApplyImplicitCompanyOnCreate(ctx context.Context, model *ModelMetadata, values RecordMap) error {
	if err := applyImplicitCreateCompanyValue(ctx, model, values); err != nil {
		return err
	}
	return validateImplicitCompanyAccess(ctx, model, values, "create")
}

func modelHasField(model *ModelMetadata, fieldName string) bool {
	if model == nil {
		return false
	}
	_, ok := runtimeLookupField(model, fieldName)
	return ok
}

func shouldEnforceRecordRules(ctx context.Context, model *ModelMetadata) bool {
	runtimeHookRegistry.mu.RLock()
	handler := runtimeHookRegistry.rules[model.Name]
	runtimeHookRegistry.mu.RUnlock()
	if handler != nil {
		return true
	}
	env := EnvironmentFromContext(ctx)
	if env == nil || env.IsSudo() {
		return false
	}
	_, ok := env.ContextValue(RBACContextAuthModeKey)
	return ok
}
