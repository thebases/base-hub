package nodes

// File overview:
// - Responsibility: the lua_script@v1 node — runs a stored base-automation `lua` action selected by
//   `automation_action_id` (seeded workflows use the action's seed external_id in
//   `automation_action` instead). Scripts are authored and saved in base-automation only; the workflow
//   never carries Lua source. The script sees the previous node's output as `payload`, the exact
//   step input bytes as `raw`, the node's own configured JSON object as `data`, plus `topic`,
//   `step_key` and `base.call(model, method, kwargs)` for model runtime methods in the run's
//   database context.
// - Key symbols: `LuaScriptAction`.
// - Payload contract: the returned table replaces the payload; returning nothing keeps the
//   (possibly mutated) `payload`. A non-object step input (e.g. a raw MQTT frame that is not a
//   JSON object) starts the script with an empty payload, and `raw` still carries the bytes.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
)

// init registers this node's single definition (plan T93): canvas spec, catalog entry and
// param validation all derive from it.
func init() {
	registerNode(nodeDef{
		Code:        "lua_script",
		Version:     1,
		Sequence:    110,
		Label:       "Lua Script",
		Description: "Run a Lua custom action saved in Automation > Actions. It receives the previous node's output as payload and this node's custom JSON as data; base.call(model, method, kwargs) reaches model runtime methods in the run's database.",
		Category:    "Action",
		Params: []paramDef{
			{
				// Options are the stored lua automation.action rows, filled per request by the
				// /wf/node-specs route (withDynamicActionParams).
				Name:      "automation_action_id",
				Label:     "Custom Action",
				Type:      "select",
				Required:  true,
				Help:      "Lua custom action (Automation > Actions) to run. It sees payload (previous node output), data (the JSON below), raw, topic, step_key and base.",
				ExtraKey:  "automation_action_id",
				ValueType: valueTypeInt,
			},
			{
				Name:     "data",
				Label:    "Custom Data",
				Type:     "json",
				Help:     `Optional JSON object handed to the script as the global "data", e.g. {"threshold": 10}.`,
				ExtraKey: "data",
			},
		},
		ExtraDefaults: map[string]any{"data": map[string]any{}},
		SideEffect:    effectScript,
	}, LuaScriptAction)
}

// LuaScriptAction runs the selected automation action and writes its result as the next payload.
func LuaScriptAction(ctx context.Context, req WorkflowActionRequest) ([]byte, error) {
	actionID := int64(intParamDefault(req.Step.Params, "automation_action_id", 0))
	// Seeded workflows reference the action by its seed external_id (`automation_action`), since a
	// JSON param cannot carry a row id; the canvas/form write the numeric automation_action_id.
	actionRef := strings.TrimSpace(firstStringParam(req.Step.Params, "automation_action"))
	if actionID <= 0 && actionRef == "" {
		return nil, orm.InvalidParamsf("lua_script: select an automation action (automation_action_id)")
	}
	if (actionID > 0 && workflowActionRuntimeHooks.RunLuaAction == nil) || (actionID <= 0 && workflowActionRuntimeHooks.RunLuaActionByRef == nil) {
		log.Errorf("lua_script: run hook is not initialized")
		return nil, fmt.Errorf("lua_script: run hook is not initialized")
	}
	// A non-object input is not an error here: the script decides what to do with `raw`.
	payload, err := decodeJSONObject(req.Payload)
	if err != nil {
		payload = map[string]any{}
	}
	// `data` is the custom JSON the node author defined when creating the step (extra_params.data);
	// it is always an object so scripts can index it without a nil check.
	data, _ := req.Step.Params["data"].(map[string]any)
	if data == nil {
		data = map[string]any{}
	}
	globals := map[string]any{
		"raw":      string(req.Payload),
		"data":     data,
		"topic":    strings.TrimSpace(req.Topic),
		"step_key": strings.TrimSpace(req.Step.Key),
		// `<run name>:<step key>`, the same on every attempt of this step (plan T56): pass it to
		// base.call / outbound systems that deduplicate. "" when the run has no name.
		"idempotency_key": stepIdempotencyKey(ctx, req),
	}
	var result map[string]any
	if actionID > 0 {
		result, err = workflowActionRuntimeHooks.RunLuaAction(ctx, actionID, payload, globals)
	} else {
		result, err = workflowActionRuntimeHooks.RunLuaActionByRef(ctx, actionRef, payload, globals)
	}
	if err != nil {
		return nil, fmt.Errorf("lua_script: %w", err)
	}
	return json.Marshal(result)
}
