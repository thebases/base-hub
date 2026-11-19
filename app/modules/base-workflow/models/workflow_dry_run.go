package models

// File overview:
// - Responsibility: carries the dry-run flag of a test run on ctx (plan T101). The engine reads it
//   to skip side-effect steps (NodeDef.SideEffect), give every step one attempt and mark the run
//   row dry_run; the test-run route is the only place that sets it.
// - Key symbols: `ContextWithWorkflowDryRun`, `WorkflowDryRunFromContext`.

import "context"

type workflowDryRunContextKey struct{}

// ContextWithWorkflowDryRun marks every run started under the returned ctx as a dry run.
func ContextWithWorkflowDryRun(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, workflowDryRunContextKey{}, true)
}

// WorkflowDryRunFromContext reports whether ctx belongs to a dry run.
func WorkflowDryRunFromContext(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	dryRun, _ := ctx.Value(workflowDryRunContextKey{}).(bool)
	return dryRun
}
