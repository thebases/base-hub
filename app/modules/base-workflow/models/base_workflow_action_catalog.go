package models

// File overview:
// - Responsibility: defines the DB-backed `base.workflow.action.catalog` model contract (field
//   metadata, resolved accessors) plus the catalog_id/action_code selection lookup used when
//   normalizing a configured workflow action. The static built-in registry itself lives in
//   `builtin_workflow_action_registry.go`.
// - Key symbols: `WorkflowActionCatalogRecord`, `resolveWorkflowActionCatalogSelection`.
// - Pattern: Leans on metadata-driven registration so storage mapping, runtime methods, and behavior hooks stay centralized instead of being scattered across handlers.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
)

type WorkflowActionCatalogRecord struct {
	orm.BaseModel
	Name           string         `bun:"name" json:"name" meta:"index,label:Action Name,help:Stores the human readable catalog label shown when users select a workflow action type."`
	Code           string         `bun:"code" json:"code" meta:"index,label:Action Code,help:Stores the stable built-in workflow action code used by the runtime handler registry."`
	Version        int            `bun:"version" json:"version" meta:"index,label:Version,help:Stores the built-in action version for this workflow action catalog row."`
	IsActive       bool           `bun:"is_active" json:"is_active" meta:"index,label:Is Active,help:Controls whether this built-in workflow action remains selectable in configuration screens."`
	Description    string         `bun:"description" json:"description" meta:"label:Description,help:Describes the business purpose of this built-in workflow action type."`
	HandlerKey     string         `bun:"handler_key" json:"handler_key" meta:"index,label:Handler Key,help:Stores the runtime handler key executed when this catalog action runs."`
	HandlerVersion int            `bun:"handler_version" json:"handler_version" meta:"label:Handler Version,help:Stores the runtime handler version executed for this catalog action."`
	DefaultParams  map[string]any `bun:"default_params,type:jsonb,nullzero" json:"default_params" meta:"kind:json,label:Default Params,help:Stores default runtime parameters that each configured action inherits unless overridden."`
}

const WorkflowActionCatalogModelName = "base.workflow.action.catalog"

var WorkflowActionCatalogDefinition = orm.ModelDefinition(WorkflowActionCatalogModelName, (*WorkflowActionCatalogRecord)(nil))

var WorkflowActionCatalogModelSpec = orm.ModelSpec{
	Name:        WorkflowActionCatalogModelName,
	Label:       "Workflow Action Catalog",
	Description: "Fixed built-in workflow action types that configured workflow actions can select",
	Definition:  WorkflowActionCatalogDefinition,
}

var WorkflowActionCatalogModel = orm.Define[WorkflowActionCatalogRecord](&Models, 90, WorkflowActionCatalogModelSpec)

// Legacy aliases keep internal packages compiling while the workflow module
// migrates from the old ETL naming to the workflow action catalog naming.
type ETLActionRecord = WorkflowActionCatalogRecord

const ETLActionModelName = WorkflowActionCatalogModelName

var (
	ETLActionDefinition = WorkflowActionCatalogDefinition
	ETLActionModelSpec  = WorkflowActionCatalogModelSpec
	ETLActionModel      = WorkflowActionCatalogModel
)

func (r WorkflowActionCatalogRecord) ResolvedCode() string {
	if code := strings.TrimSpace(r.Code); code != "" {
		return code
	}
	return strings.TrimSpace(r.Name)
}

func (r WorkflowActionCatalogRecord) ResolvedHandlerKey() string {
	if key := strings.TrimSpace(r.HandlerKey); key != "" {
		return key
	}
	if entry, ok := ResolveBuiltinWorkflowAction(r.ResolvedCode(), r.ResolvedVersion()); ok {
		return entry.HandlerKey
	}
	return r.ResolvedCode()
}

func (r WorkflowActionCatalogRecord) ResolvedHandlerVersion() int {
	if r.HandlerVersion > 0 {
		return r.HandlerVersion
	}
	if entry, ok := ResolveBuiltinWorkflowAction(r.ResolvedCode(), r.ResolvedVersion()); ok {
		return entry.HandlerVersion
	}
	return 1
}

func (r WorkflowActionCatalogRecord) ResolvedVersion() int {
	if r.Version > 0 {
		return r.Version
	}
	return 1
}

func (r WorkflowActionCatalogRecord) ResolvedDefaultParams() map[string]any {
	if len(r.DefaultParams) > 0 {
		return cloneActionParams(r.DefaultParams)
	}
	if entry, ok := ResolveBuiltinWorkflowAction(r.ResolvedCode(), r.ResolvedVersion()); ok {
		return cloneActionParams(entry.DefaultParams)
	}
	return map[string]any{}
}

// resolveWorkflowActionCatalogSelection resolves the workflow action catalog selection handled by this package.
func resolveWorkflowActionCatalogSelection(ctx context.Context, values orm.RecordMap, current *workflowActionState) (*WorkflowActionCatalogRecord, ETLActionCatalogEntry, error) {
	if catalogID := firstNonNil(values["catalog_id"], currentCatalogID(current)); catalogID != nil {
		record, err := loadWorkflowActionCatalogRecord(ctx, catalogID)
		if err != nil {
			return nil, ETLActionCatalogEntry{}, err
		}
		if record == nil {
			log.Errorf("base.workflow.action catalog_id does not reference an existing workflow action catalog row")
			return nil, ETLActionCatalogEntry{}, orm.InvalidParamsf("base.workflow.action catalog_id does not reference an existing workflow action catalog row")
		}
		entry, ok := ResolveBuiltinWorkflowAction(record.ResolvedCode(), record.ResolvedVersion())
		if !ok {
			log.Errorf("base.workflow.action catalog code %q is not part of the built-in workflow catalog", record.ResolvedCode())
			return nil, ETLActionCatalogEntry{}, orm.InvalidParamsf("base.workflow.action catalog code %q is not part of the built-in workflow catalog", record.ResolvedCode())
		}
		return record, entry, nil
	}

	actionCode := strings.TrimSpace(currentWorkflowActionCode(current))
	if rawCode, ok := stringFromAny(values["action_code"]); ok {
		trimmedCode := strings.TrimSpace(rawCode)
		// A stored `related:catalog_id.code` action_code serializes as boolean `false` when
		// catalog_id is unset; a client-side `String(value ?? "")` coercion bug can turn that
		// back into the literal text "false" before it round-trips here. Treat that (and other
		// JS-ism sentinels) as unset rather than a real code, so a stray bad value from one
		// legacy/corrupted row can't silently pass catalog resolution or block a whole batch save
		// with a confusing "false is not part of the catalog" error.
		if trimmedCode != "" && trimmedCode != "false" && trimmedCode != "null" && trimmedCode != "undefined" {
			actionCode = trimmedCode
		}
	}
	if actionCode == "" {
		log.Errorf("base.workflow.action catalog_id or action_code is required")
		return nil, ETLActionCatalogEntry{}, orm.InvalidParamsf("base.workflow.action catalog_id or action_code is required")
	}
	entry, ok := ResolveBuiltinWorkflowAction(actionCode, resolvedWorkflowActionVersion(values, current))
	if !ok {
		log.Errorf("base.workflow.action action_code %q is not part of the built-in workflow catalog", actionCode)
		return nil, ETLActionCatalogEntry{}, orm.InvalidParamsf("base.workflow.action action_code %q is not part of the built-in workflow catalog", actionCode)
	}
	return nil, entry, nil
}

// loadWorkflowActionCatalogRecord loads the workflow action catalog record handled by this package.
func loadWorkflowActionCatalogRecord(ctx context.Context, rawID any) (*WorkflowActionCatalogRecord, error) {
	ptr, ok := int64PointerValue(rawID)
	if !ok {
		log.Errorf("base.workflow.action catalog_id must be a workflow action catalog id")
		return nil, orm.InvalidParamsf("base.workflow.action catalog_id must be a workflow action catalog id")
	}
	if ptr == nil || *ptr <= 0 {
		return nil, nil
	}
	rows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(WorkflowActionCatalogModelName, nil), []string{fmt.Sprint(*ptr)}, nil)
	if err != nil {
		log.Errorf("base.workflow.action catalog_id lookup failed: %v", err)
		return nil, fmt.Errorf("base.workflow.action catalog_id lookup failed: %w", err)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	raw, err := json.Marshal(rows[0])
	if err != nil {
		return nil, err
	}
	var record WorkflowActionCatalogRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		return nil, err
	}
	return &record, nil
}

func currentCatalogID(current *workflowActionState) any {
	// A nil *int64 stored in an interface is not == nil: return an untyped nil so firstNonNil
	// falls through to action_code for rows stored without a catalog row.
	if current == nil || current.CatalogID == nil {
		return nil
	}
	return current.CatalogID
}
