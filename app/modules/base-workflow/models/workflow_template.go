package models

// File overview:
// - Responsibility: the workflow templates gallery (plan T101). A template is a base.workflow row
//   with is_template=true (the demo seeds 14/15/16); it never runs (the trigger registry, /wh and
//   test-run refuse it). `list_templates` serves the gallery and `instantiate_template` turns one
//   into a normal, inactive workflow through the existing copy (workflow_copy.go).
// - Key symbols: `listWorkflowTemplates`, `instantiateWorkflowTemplate`, `WorkflowTemplateSummary`.

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"git.thebase.vn/base_hub/internal/orm"
)

// WorkflowTemplateSummary is one gallery card.
type WorkflowTemplateSummary struct {
	ID           int64    `json:"id"`
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	TriggerTypes []string `json:"trigger_types"`
	StepCount    int      `json:"step_count"`
}

// listWorkflowTemplates returns the templates the caller can read, by name. Trigger types are the
// template's trigger rows; a template with none is a webhook workflow (the canvas keeps webhook
// implicit). Templates are few (seeded demos), so the per-template child reads stay cheap.
func listWorkflowTemplates(ctx context.Context, model *orm.ModelMetadata, _ []string, _ []any, _ map[string]any) (any, error) {
	if err := EnsureWorkflowRole(ctx, WorkflowRoleViewer); err != nil {
		return nil, err
	}
	rows, err := orm.RuntimeRepositorySearchRead(ctx, orm.RuntimeModelRef(FlowModelName, model), orm.RecordMap{
		"is_template": true,
		"__order":     "name asc, id asc",
	}, []string{"id", "name", "description"})
	if err != nil {
		return nil, err
	}
	out := make([]WorkflowTemplateSummary, 0, len(rows))
	for _, row := range rows {
		id := int64Value(row["id"])
		// Repository reads skip record rules: a template the caller cannot read is left out.
		if err := orm.EnsureRecordAccess(ctx, FlowModelName, "read", []string{fmt.Sprint(id)}); err != nil {
			var typed *orm.Error
			if errors.As(err, &typed) && typed.Code == orm.ErrForbidden {
				continue
			}
			return nil, err
		}
		summary, err := workflowTemplateSummary(ctx, id, row)
		if err != nil {
			return nil, err
		}
		out = append(out, summary)
	}
	return out, nil
}

func workflowTemplateSummary(ctx context.Context, id int64, row orm.RecordMap) (WorkflowTemplateSummary, error) {
	summary := WorkflowTemplateSummary{
		ID:           id,
		Name:         stringValue(row["name"]),
		Description:  stringValue(row["description"]),
		TriggerTypes: []string{},
	}
	triggers, err := orm.RuntimeRepositorySearchRead(ctx, orm.RuntimeModelRef(WorkflowTriggerModelName, nil), orm.RecordMap{
		"workflow_id": id,
		"__order":     "id asc",
	}, []string{"trigger_type"})
	if err != nil {
		return summary, err
	}
	for _, trigger := range triggers {
		triggerType := strings.TrimSpace(stringValue(trigger["trigger_type"]))
		if triggerType != "" && !slices.Contains(summary.TriggerTypes, triggerType) {
			summary.TriggerTypes = append(summary.TriggerTypes, triggerType)
		}
	}
	if len(summary.TriggerTypes) == 0 {
		summary.TriggerTypes = append(summary.TriggerTypes, WorkflowTriggerTypeWebhook)
	}
	actions, err := orm.RuntimeRepositorySearchRead(ctx, orm.RuntimeModelRef(WorkflowActionModelName, nil), orm.RecordMap{
		"workflow_id": id,
	}, []string{"id"})
	if err != nil {
		return summary, err
	}
	summary.StepCount = len(actions)
	return summary, nil
}

// instantiateWorkflowTemplate copies template ids[0] (kwargs/args[0] `name` optional) through the
// workflow copy, so steps, edges and triggers are cloned in one locked transaction. The copy is a
// normal workflow (is_template=false) that stays inactive until its author activates it; the copy
// already creates non-webhook triggers inactive. Authoring: the dispatcher checks it as a write
// (base.group_system); the explicit check covers direct callers.
func instantiateWorkflowTemplate(ctx context.Context, model *orm.ModelMetadata, ids []string, args []any, kwargs map[string]any) (any, error) {
	if len(ids) == 0 {
		return nil, orm.InvalidParamsf("base.workflow.instantiate_template requires an id")
	}
	if err := EnsureWorkflowRole(ctx, WorkflowRoleAuthor); err != nil {
		return nil, err
	}
	if err := orm.EnsureRecordAccess(ctx, FlowModelName, "read", ids[:1]); err != nil {
		return nil, err
	}
	rows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(FlowModelName, model), ids[:1], []string{"id", "is_template"})
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, orm.NotFoundf("base.workflow.instantiate_template: workflow %s not found", ids[0])
	}
	if !boolValue(rows[0]["is_template"]) {
		return nil, orm.InvalidParamsf("base.workflow.instantiate_template: workflow %s is not a template", ids[0])
	}
	defaults := map[string]any{"is_template": false, "is_active": false}
	if name := workflowTemplateCopyName(args, kwargs); name != "" {
		defaults["name"] = name
	}
	return copyFlow(ctx, model, ids[:1], nil, map[string]any{"default": defaults})
}

// workflowTemplateCopyName reads the optional new name from kwargs["name"] or a string args[0].
func workflowTemplateCopyName(args []any, kwargs map[string]any) string {
	if name, ok := kwargs["name"].(string); ok {
		return strings.TrimSpace(name)
	}
	if len(args) > 0 {
		if name, ok := args[0].(string); ok {
			return strings.TrimSpace(name)
		}
	}
	return ""
}
