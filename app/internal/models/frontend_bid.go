package models

// File overview:
// - Responsibility: defines built-in runtime model metadata and server-side behavior for frontend bid inside the kernel model catalog.
// - Key symbols: `registerFrontendBIDConstraint`, `validateFrontendBIDConstraint`.
// - Pattern: Leans on metadata-driven registration so storage mapping, runtime methods, and behavior hooks stay centralized instead of being scattered across handlers.
// - Pattern: Threads `context.Context` through the main execution paths so cancellation, auth scope, and transaction state can follow the work end to end.

import (
	"context"
	"fmt"
	"strings"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
)

const frontendBIDContextKey = "rpc_source"
const frontendBIDContextValue = "frontend"
const frontendBIDMethodContextKey = "rpc_method"
const frontendBIDCreateMethod = "create"
const frontendBIDPrefix = "x-"

// registerFrontendBIDConstraint registers the frontend bid constraint handled by this package.
func registerFrontendBIDConstraint(modelName string) {
	orm.RegisterConstraint(modelName, []string{"bid"}, validateFrontendBIDConstraint)
}

// validateFrontendBIDConstraint validates the frontend bid constraint handled by this package.
func validateFrontendBIDConstraint(ctx context.Context, _ orm.RecordSet, values orm.RecordMap) error {
	env := orm.EnvironmentFromContext(ctx)
	if env == nil {
		return nil
	}
	source, _ := env.ContextValue(frontendBIDContextKey)
	if !strings.EqualFold(strings.TrimSpace(stringValue(source)), frontendBIDContextValue) {
		return nil
	}
	method, _ := env.ContextValue(frontendBIDMethodContextKey)
	if !strings.EqualFold(strings.TrimSpace(stringValue(method)), frontendBIDCreateMethod) {
		return nil
	}

	bidValue, changed := firstChangedBID(values)
	if !changed {
		return nil
	}
	if bidValue == "" {
		log.Errorf("bid is required for frontend-created records and must start with %q", frontendBIDPrefix)
		return fmt.Errorf("bid is required for frontend-created records and must start with %q", frontendBIDPrefix)
	}
	if !strings.HasPrefix(strings.ToLower(bidValue), frontendBIDPrefix) {
		log.Errorf("bid %q must start with %q", bidValue, frontendBIDPrefix)
		return fmt.Errorf("bid %q must start with %q", bidValue, frontendBIDPrefix)
	}
	return nil
}

func firstChangedBID(values orm.RecordMap) (string, bool) {
	// Walk each candidate key ("bid", "bId") in order so validation and side effects stay scoped to the current element.
	for _, key := range []string{"bid", "bId"} {
		raw, ok := values[key]
		if !ok {
			continue
		}
		return strings.TrimSpace(stringValue(raw)), true
	}
	return "", false
}
