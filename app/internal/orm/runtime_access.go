package orm

// File overview:
// - Responsibility: implements the metadata-driven ORM/runtime kernel for runtime access, where storage, runtime dispatch, and security rules stay centralized.
// - Key symbols: `ensureModelAccess`, `EnsureRuntimeModelAccess`.
// - Pattern: Leans on metadata-driven registration so storage mapping, runtime methods, and behavior hooks stay centralized instead of being scattered across handlers.
// - Pattern: Threads `context.Context` through the main execution paths so cancellation, auth scope, and transaction state can follow the work end to end.

import (
	"context"
	"fmt"
	"git.thebase.vn/base_hub/internal/services/log"
	"strings"
)

// ensureModelAccess applies model-level access checks and honors sudo mode.
func ensureModelAccess(ctx context.Context, env *Environment, model *ModelMetadata, operation string, ids []string, values RecordMap) error {
	if model == nil {
		log.Errorf("orm: model metadata is required")
		return fmt.Errorf("orm: model metadata is required")
	}
	if env != nil && env.IsSudo() {
		// Sudo mode bypasses model-level access hooks by design.
		return nil
	}
	runtimeHookRegistry.mu.RLock()
	handler := runtimeHookRegistry.access[model.Name]
	defaultHandler := runtimeHookRegistry.defaultAccess
	runtimeHookRegistry.mu.RUnlock()
	if handler == nil {
		handler = defaultHandler
	}
	if handler == nil {
		// No handler means the model relies on default allow semantics.
		return nil
	}
	return handler(ctx, env, model, operation, ids, values)
}

type runtimeMethodNameKey struct{}

// RuntimeMethodNameFromContext returns the runtime method an access handler is being asked about
// ("" outside EnsureRuntimeModelAccess). Every non-read-only custom method is checked as "write",
// so a handler that grants one specific method more widely than generic writes keys on this name.
func RuntimeMethodNameFromContext(ctx context.Context) string {
	name, _ := ctx.Value(runtimeMethodNameKey{}).(string)
	return name
}

// EnsureRuntimeModelAccess applies model-level access checks for one runtime
// method using the method's read-only contract to derive the required CRUD mode.
func EnsureRuntimeModelAccess(ctx context.Context, model *ModelMetadata, method RuntimeMethod, ids []string) error {
	if model == nil {
		log.Errorf("orm: model metadata is required")
		return fmt.Errorf("orm: model metadata is required")
	}
	ctx = context.WithValue(ctx, runtimeMethodNameKey{}, strings.TrimSpace(method.Name))
	operation := "read"
	switch strings.TrimSpace(method.Name) {
	case "create", "name_create", "copy":
		operation = "create"
	case "write", "toggle_active":
		operation = "write"
	case "unlink":
		operation = "unlink"
	default:
		if !method.ReadOnly {
			operation = "write"
		}
	}
	return ensureModelAccess(ctx, EnvironmentFromContext(ctx), model, operation, ids, nil)
}

// EnsureDefaultModelAccess runs the registered default (ACL) access handler
// directly. Model-specific access handlers replace the default in
// ensureModelAccess, so a handler that only wants to ADD checks on top of the
// standard ACL semantics must chain through this helper first.
func EnsureDefaultModelAccess(ctx context.Context, env *Environment, model *ModelMetadata, operation string, ids []string, values RecordMap) error {
	if env != nil && env.IsSudo() {
		// Sudo mode bypasses model-level access hooks by design.
		return nil
	}
	runtimeHookRegistry.mu.RLock()
	handler := runtimeHookRegistry.defaultAccess
	runtimeHookRegistry.mu.RUnlock()
	if handler == nil {
		return nil
	}
	return handler(ctx, env, model, operation, ids, values)
}

// EnsureRecordReadAccess verifies the caller can read specific records of an
// arbitrary registered model: model ACL plus record rules plus company scope.
// sys.attachment delegates its access decision here so an attachment is
// exactly as visible as the record it belongs to (SRS-F-019 access rule).
func EnsureRecordReadAccess(ctx context.Context, modelName string, ids []string) error {
	return EnsureRecordAccess(ctx, modelName, "read", ids)
}

// EnsureRecordAccess is EnsureRecordReadAccess for any operation (read, write, unlink): model ACL
// plus that operation's record rules and company scope on ids. Polymorphic modules (discuss
// followers/messages) use it to check access to the record a row points at.
func EnsureRecordAccess(ctx context.Context, modelName, operation string, ids []string) error {
	meta, ok := LookupSharedModel(strings.TrimSpace(modelName))
	if !ok {
		log.Errorf("orm: model %q is not registered", modelName)
		return fmt.Errorf("orm: model %q is not registered", modelName)
	}
	if err := ensureModelAccess(ctx, EnvironmentFromContext(ctx), meta, operation, ids, nil); err != nil {
		return err
	}
	return ensureRecordRuleAccess(ctx, meta, operation, ids)
}

// RecordRuleDomainFor returns the record-rule domain (explicit rules plus implicit company scope)
// the caller in ctx gets for operation on modelName, or TrueDomain when none applies (sudo,
// internal callers, no rules). Polymorphic record rules use it to scope rows by their target.
func RecordRuleDomainFor(ctx context.Context, modelName, operation string) (DomainNode, error) {
	meta, ok := LookupSharedModel(strings.TrimSpace(modelName))
	if !ok || !shouldEnforceRecordRules(ctx, meta) {
		return TrueDomain(), nil
	}
	domain, err := recordRuleDomain(ctx, EnvironmentFromContext(ctx), meta, operation)
	if err != nil {
		return DomainNode{}, err
	}
	if domain.Kind == "" {
		return TrueDomain(), nil
	}
	return domain, nil
}

// recordRuleDomain resolves the optional record-rule domain for the model.
func recordRuleDomain(ctx context.Context, env *Environment, model *ModelMetadata, operation string) (DomainNode, error) {
	if model == nil {
		// Missing model metadata means there is nothing to restrict.
		return TrueDomain(), nil
	}
	if env != nil && env.IsSudo() {
		// Sudo mode also bypasses record-rule filtering.
		return TrueDomain(), nil
	}
	runtimeHookRegistry.mu.RLock()
	handler := runtimeHookRegistry.rules[model.Name]
	defaultHandler := runtimeHookRegistry.defaultRules
	runtimeHookRegistry.mu.RUnlock()
	if handler == nil {
		handler = defaultHandler
	}
	implicitDomain, err := implicitCompanyRuleDomain(env, model, operation)
	if err != nil {
		return DomainNode{}, err
	}
	if handler == nil {
		return implicitDomain, nil
	}
	explicitDomain, err := handler(ctx, env, model, operation)
	if err != nil {
		return DomainNode{}, err
	}
	switch {
	case implicitDomain.Kind == "" || implicitDomain.Kind == DomainKindTrue:
		return explicitDomain, nil
	case explicitDomain.Kind == "" || explicitDomain.Kind == DomainKindTrue:
		return implicitDomain, nil
	default:
		return And(implicitDomain, explicitDomain), nil
	}
}
