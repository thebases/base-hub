package models

// File overview:
// - Responsibility: defines built-in runtime model metadata and server-side behavior for ui action client inside the kernel model catalog.
// - Key symbols: `init`, `createUIActionClient`, `UIActionClientRecord`.
// - Pattern: Leans on metadata-driven registration so storage mapping, runtime methods, and behavior hooks stay centralized instead of being scattered across handlers.
// - Pattern: Threads `context.Context` through the main execution paths so cancellation, auth scope, and transaction state can follow the work end to end.

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
)

// UIActionClientRecord stores a frontend-directed client action that mirrors
// the useful subset of ir.actions.client/sys.actions.client.
type UIActionClientRecord struct {
	orm.BaseModel
	Name     string         `bun:"name" json:"name" meta:"index,label:Name,help:Stores the human readable name for this record."`
	Type     string         `bun:"type" json:"type" meta:"index,label:Type,help:Stores the type value for this record."`
	Tag      string         `bun:"tag" json:"tag" meta:"index,label:Tag,help:Stores the tag value for this record."`
	Target   string         `bun:"target" json:"target" meta:"index,label:Target,help:Where the client opens the action: current | main | fullscreen | dialog (alias new) | sheet | window."`
	ResModel string         `bun:"sys_model" json:"sys_model" meta:"index,label:Res Model,help:Stores the res model value for this record."`
	Context  map[string]any `bun:"context,type:jsonb,nullzero" json:"context" meta:"kind:json,label:Context,help:Stores the context payload in JSON format."`
	Params   map[string]any `bun:"params,type:jsonb,nullzero" json:"params" meta:"kind:json,label:Params,help:Stores the params payload in JSON format."`
}

const UIActionClientModelName = "ui.actions.client"

// Client action targets decide where the frontend presents the action. The
// window targets ("current", "new", "fullscreen", "main") are shared with
// ui.actions.window (isSupportedUIActionTarget);
// dialog/sheet/window are client-only presentation modes so a view arch can
// declare "open in a popup / side sheet / new browser tab" as data instead of
// hardcoding it in the UI.
const (
	UIActionTargetCurrent = "current" // replace the current main view (navigate)
	UIActionTargetDialog  = "dialog"  // modal popup over the current view
	UIActionTargetSheet   = "sheet"   // off-canvas side sheet over the current view
	UIActionTargetWindow  = "window"  // new browser window/tab
)

// Presentation params read by the frontend host for dialog/sheet targets.
// Kept in `params` (not new columns) so no schema migration is needed.
var (
	uiActionClientDialogSizes = map[string]bool{"sm": true, "md": true, "lg": true, "xl": true}
	uiActionClientSheetSides  = map[string]bool{"left": true, "right": true} // BaseUI Sheet placements
)

var UIActionClientDefinition = orm.ModelDefinition(UIActionClientModelName, (*UIActionClientRecord)(nil))

// UIActionClientModel owns persisted client-action metadata and exposes an
// Runtime contract for action loading.
// Keep new customization in composition style by adding focused normalization
// helpers and handler options instead of a single customize function.
var UIActionClientModel = orm.Define[UIActionClientRecord](&Models, 180, orm.ModelSpec{
	Name:        UIActionClientModelName,
	Label:       "UI Client Action",
	Description: "Frontend client actions modeled after ir.actions.client",
	Definition:  UIActionClientDefinition,
	RuntimeMethods: []orm.RuntimeMethod{
		{Name: "create", Public: true, ModelOnly: true, Call: createUIActionClient},
		{Name: "write", Public: true, UsesPathID: true, Call: writeUIActionClient},
		{Name: "_get_action_dict", Public: false, ReadOnly: true, Call: getActionDictUIActionClient},
		{Name: "_get_readable_fields", Public: false, ModelOnly: true, ReadOnly: true, Call: getReadableFieldsUIActionClient},
	},
})

var uiActionClientReadableFields = []string{
	"context",
	"id",
	"name",
	"params",
	"sys_model",
	"tag",
	"target",
	"type",
}

// createUIActionClient creates the ui action client handled by this package.
func createUIActionClient(ctx context.Context, model *orm.ModelMetadata, _ []string, args []any, kwargs map[string]any) (any, error) {
	values, err := recordMapFromArgs(args, kwargs, "values")
	if err != nil {
		return nil, err
	}
	if values == nil {
		log.Errorf("ui.actions.client.create requires values")
		return nil, fmt.Errorf("ui.actions.client.create requires values")
	}
	normalized, err := normalizeUIActionClientValues(values, true)
	if err != nil {
		return nil, err
	}
	return orm.CreateRecord(ctx, orm.RuntimeModelRef(UIActionClientModelName, model), normalized)
}

func writeUIActionClient(ctx context.Context, model *orm.ModelMetadata, ids []string, args []any, kwargs map[string]any) (any, error) {
	values, err := recordMapFromArgs(args, kwargs, "values")
	if err != nil {
		return nil, err
	}
	if values == nil {
		log.Errorf("ui.actions.client.write requires values")
		return nil, fmt.Errorf("ui.actions.client.write requires values")
	}
	if len(ids) == 0 {
		log.Errorf("ui.actions.client.write requires ids")
		return nil, fmt.Errorf("ui.actions.client.write requires ids")
	}
	normalized, err := normalizeUIActionClientValues(values, false)
	if err != nil {
		return nil, err
	}
	return orm.WriteRecords(ctx, orm.RuntimeModelRef(UIActionClientModelName, model), ids, normalized)
}

// getActionDictUIActionClient retrieves the action dict ui action client handled by this package.
func getActionDictUIActionClient(ctx context.Context, _ *orm.ModelMetadata, ids []string, args []any, kwargs map[string]any) (any, error) {
	actionID, err := resolveUIActionID(ids, args, kwargs)
	if err != nil {
		return nil, err
	}
	row, err := loadUIActionClientRow(ctx, actionID)
	if err != nil {
		return nil, err
	}
	return buildUIActionClientDict(row), nil
}

// buildUIActionClientDict shapes one stored client-action row into the action
// dict the frontend dispatches on; shared by _get_action_dict and
// sys.model.action's get_bound_actions so both emit the same contract.
func buildUIActionClientDict(row orm.RecordMap) map[string]any {
	return map[string]any{
		"type":      UIActionClientModelName,
		"id":        int64Value(row["id"]),
		"name":      firstNonEmpty(strings.TrimSpace(stringValue(row["name"])), strings.TrimSpace(stringValue(row["tag"])), "Client Action"),
		"tag":       strings.TrimSpace(stringValue(row["tag"])),
		"target":    firstNonEmpty(strings.TrimSpace(stringValue(row["target"])), UIActionTargetCurrent),
		"sys_model": strings.TrimSpace(stringValue(row["sys_model"])),
		"context":   normalizeUIActionContextForOutput(row["context"]),
		"params":    normalizeUIActionContextForOutput(row["params"]),
	}
}

// getReadableFieldsUIActionClient retrieves the readable fields ui action client handled by this package.
func getReadableFieldsUIActionClient(_ context.Context, _ *orm.ModelMetadata, _ []string, _ []any, _ map[string]any) (any, error) {
	fields := append([]string(nil), uiActionClientReadableFields...)
	sort.Strings(fields)
	return fields, nil
}

func normalizeUIActionClientValues(values orm.RecordMap, creating bool) (orm.RecordMap, error) {
	// Clone the caller payload so normalization does not mutate transport-owned
	// maps shared with outer layers or tests.
	normalized := cloneRecordMap(values)
	normalized["type"] = UIActionClientModelName

	// Client actions always need a dispatch tag because that is the frontend's
	// stable identifier for deciding how to execute the action.
	if tag, ok := stringFromAny(normalized["tag"]); ok {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			log.Errorf("ui.actions.client tag is required")
			return nil, fmt.Errorf("ui.actions.client tag is required")
		}
		normalized["tag"] = tag
	} else if creating {
		log.Errorf("ui.actions.client tag is required")
		return nil, fmt.Errorf("ui.actions.client tag is required")
	}

	// Keep the display name readable and deterministic, defaulting to the tag.
	if name, ok := stringFromAny(normalized["name"]); ok {
		name = strings.TrimSpace(name)
		if name == "" {
			log.Errorf("ui.actions.client name is required")
			return nil, fmt.Errorf("ui.actions.client name is required")
		}
		normalized["name"] = name
	} else if creating {
		normalized["name"] = strings.TrimSpace(stringValue(normalized["tag"]))
	}

	// Targets are the window-action set plus the client-only presentation
	// modes (dialog/sheet/window), so dispatch mode is data, not UI code.
	if target, ok := stringFromAny(normalized["target"]); ok {
		target = strings.ToLower(strings.TrimSpace(target))
		if target == "" {
			target = UIActionTargetCurrent
		}
		if !isSupportedUIActionClientTarget(target) {
			log.Errorf("ui.actions.client invalid target %q", target)
			return nil, fmt.Errorf("ui.actions.client invalid target %q", target)
		}
		normalized["target"] = target
	} else if creating {
		normalized["target"] = UIActionTargetCurrent
	}

	// Optional destination model references should still point at registered
	// runtime models so client actions cannot drift to dead targets silently.
	if resModel, ok := stringFromAny(normalized["sys_model"]); ok {
		resModel = strings.TrimSpace(resModel)
		if resModel != "" {
			if _, exists := orm.LookupModel(resModel); !exists {
				log.Errorf("ui.actions.client unknown sys_model %q", resModel)
				return nil, fmt.Errorf("ui.actions.client unknown sys_model %q", resModel)
			}
			normalized["sys_model"] = resModel
		} else {
			normalized["sys_model"] = ""
		}
	}

	// Normalize both context and params to JSON objects so the action dict stays
	// strongly typed for current frontend consumers.
	if rawContext, ok := normalized["context"]; ok {
		contextValue, err := normalizeUIActionContext(rawContext)
		if err != nil {
			log.Errorf("ui.actions.client %v", err)
			return nil, fmt.Errorf("ui.actions.client %w", err)
		}
		normalized["context"] = contextValue
	} else if creating {
		normalized["context"] = map[string]any{}
	}
	if rawParams, ok := normalized["params"]; ok {
		paramsValue, err := normalizeUIActionContext(rawParams)
		if err != nil {
			log.Errorf("ui.actions.client params must be an object")
			return nil, fmt.Errorf("ui.actions.client params must be an object")
		}
		// Reject unknown presentation options up front so a typo in a view's
		// data file fails at load time instead of silently rendering defaults.
		if err := validateUIActionClientPresentation(paramsValue); err != nil {
			log.Errorf("ui.actions.client %v", err)
			return nil, fmt.Errorf("ui.actions.client %w", err)
		}
		normalized["params"] = paramsValue
	} else if creating {
		normalized["params"] = map[string]any{}
	}
	return normalized, nil
}

// isSupportedUIActionClientTarget accepts the shared window-action targets plus
// the client-only presentation modes.
func isSupportedUIActionClientTarget(target string) bool {
	switch target {
	case UIActionTargetDialog, UIActionTargetSheet, UIActionTargetWindow:
		return true
	default:
		return isSupportedUIActionTarget(target)
	}
}

// validateUIActionClientPresentation checks the optional dialog `size` and
// sheet `side` params. Both are optional and validated independently of the
// target, because a write may change params without resending target.
func validateUIActionClientPresentation(params map[string]any) error {
	if raw, ok := params["size"]; ok && raw != nil {
		size, _ := raw.(string)
		if !uiActionClientDialogSizes[strings.TrimSpace(size)] {
			return fmt.Errorf("invalid params.size %v (want sm|md|lg|xl)", raw)
		}
	}
	if raw, ok := params["side"]; ok && raw != nil {
		side, _ := raw.(string)
		if !uiActionClientSheetSides[strings.TrimSpace(side)] {
			return fmt.Errorf("invalid params.side %v (want left|right)", raw)
		}
	}
	return nil
}

// loadUIActionClientRow loads the ui action client row handled by this package.
func loadUIActionClientRow(ctx context.Context, actionID int64) (orm.RecordMap, error) {
	rows, err := orm.RuntimeRepositoryRead(
		ctx, orm.RuntimeModelRef(UIActionClientModelName, nil),
		[]string{fmt.Sprintf("%d", actionID)},
		[]string{"id", "name", "type", "tag", "target", "sys_model", "context", "params"},
	)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, sql.ErrNoRows
	}
	return rows[0], nil
}
