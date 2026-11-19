package nodes

// File overview:
// - Responsibility: the dry-run stand-in for a step (plan T101). The engine calls
//   `DryRunStepOutput` instead of the handler for a step that must not run in a dry run, and the
//   step's output records what it would have done while passing the payload on.
// - Decision: one generic intent builder keyed on NodeDef.SideEffect instead of an Intent func per
//   node: the step params already hold the target, model, operation and values, so the only
//   node-specific part is which params to lift to the top of the record.

import (
	"encoding/json"
	"strings"

	workflowmodels "git.thebase.vn/base_hub/modules/base-workflow/models"
)

const (
	// DryRunMarkerKey is set to true on the output of every stubbed step.
	DryRunMarkerKey = "_dry_run"
	// DryRunIntendedKey lists the intent records of the stubbed steps so far, in execution order.
	DryRunIntendedKey = "_dry_run_intended"

	dryRunRedacted = "[redacted]"

	dryRunReasonSideEffect  = "side_effect"
	dryRunReasonCredential  = "credential"
	dryRunReasonUnknownNode = "unknown_node"
)

// crudReadOperations are crud_model operations that only read, so a dry run still executes them.
var crudReadOperations = map[string]struct{}{"search": {}, "search_read": {}, "search_one": {}, "read_one": {}}

// DryRunStepOutput decides whether a step of a dry run must be stubbed and, when it must, returns
// the output recorded in its place. A step is stubbed when its node has a side effect (crud_model
// reads excepted), when its node is not a registered builtin (its effect is unknown), or when its
// authored params carry a credential reference: a dry run never decrypts a secret, so even a pure
// crypto node with an @cred key is only described. params are the interpolated step params, never
// credential-resolved ones; credentialRefs is libs.AuthoredCredentialRefs of the authored params.
func DryRunStepOutput(code string, version int, stepKey string, params map[string]any, credentialRefs map[string]string, payload []byte) ([]byte, bool, error) {
	def, known := workflowmodels.LookupNodeDef(code, version)
	reason := ""
	switch {
	case !known:
		reason = dryRunReasonUnknownNode
	case def.SideEffect != "" && !dryRunReadOnlyCRUD(def, params):
		reason = dryRunReasonSideEffect
	case len(credentialRefs) > 0:
		reason = dryRunReasonCredential
	default:
		return nil, false, nil
	}
	input, _ := decodeJSONObject(payload)
	output, err := json.Marshal(dryRunPassThrough(input, dryRunIntent(def, code, version, stepKey, reason, params, input)))
	return output, true, err
}

func dryRunReadOnlyCRUD(def workflowmodels.NodeDef, params map[string]any) bool {
	if def.SideEffect != workflowmodels.NodeEffectWrite {
		return false
	}
	_, ok := crudReadOperations[strings.ToLower(strings.TrimSpace(firstStringParam(params, "operation", "op", "method")))]
	return ok
}

// dryRunIntent is the record of what the step would have done. Secrets are redacted and
// credential references are kept as written (a reference names a secret, it is not one).
func dryRunIntent(def workflowmodels.NodeDef, code string, version int, stepKey, reason string, params, input map[string]any) map[string]any {
	intent := map[string]any{
		"action": handlerKey(code, version),
		"step":   strings.TrimSpace(stepKey),
		"effect": def.SideEffect,
		"reason": reason,
		"params": redactDryRunValue(params),
	}
	switch def.SideEffect {
	case workflowmodels.NodeEffectHTTP, workflowmodels.NodeEffectOutbox:
		intent["method"] = "POST"
		intent["url"] = strings.TrimSpace(firstStringParam(params, "url", "target_ref", "callback_url"))
	case workflowmodels.NodeEffectWrite:
		intent["model"] = strings.TrimSpace(firstStringParam(params, "model", "model_name"))
		operation := strings.ToLower(strings.TrimSpace(firstStringParam(params, "operation", "op", "method")))
		if operation == "" {
			operation = "find_or_create"
		}
		intent["operation"] = operation
		// The values as crud_model would render them against the payload ({{ field }} templates).
		if values, err := renderedObjectParam(params, input, "values", "payload", "data"); err == nil && len(values) > 0 {
			intent["values"] = redactDryRunValue(values)
		}
	case workflowmodels.NodeEffectScript:
		intent["automation_action_id"] = params["automation_action_id"]
	}
	return intent
}

// dryRunPassThrough is the stubbed step's output: the input object unchanged (so downstream steps
// still see the payload) plus the dry-run marker and this intent appended to the earlier ones.
// A non-object input cannot carry the keys and is replaced by the marker object alone.
func dryRunPassThrough(input map[string]any, intent map[string]any) map[string]any {
	output := cloneJSONObject(input)
	intended, _ := output[DryRunIntendedKey].([]any)
	output[DryRunMarkerKey] = true
	output[DryRunIntendedKey] = append(intended, intent)
	return output
}

// dryRunSecretKeys mirror the engine's builtin audit redaction list (credentials only).
var dryRunSecretKeys = map[string]struct{}{
	"authorization": {}, "proxy_authorization": {}, "cookie": {}, "set_cookie": {},
	"password": {}, "passwd": {}, "secret": {}, "token": {}, "key": {},
}

// dryRunPlainKeys are param names that match a secret suffix but never hold a secret.
var dryRunPlainKeys = map[string]struct{}{"result_key": {}, "step_key": {}, "idempotency_key": {}}

func dryRunSecretKey(key string) bool {
	key = strings.ReplaceAll(strings.ToLower(strings.TrimSpace(key)), "-", "_")
	if _, ok := dryRunPlainKeys[key]; ok {
		return false
	}
	if _, ok := dryRunSecretKeys[key]; ok {
		return true
	}
	for _, suffix := range []string{"_key", "_secret", "_token", "_password"} {
		if strings.HasSuffix(key, suffix) {
			return true
		}
	}
	return false
}

// redactDryRunValue deep-copies value with every secret-named key's value replaced, except a
// credential reference, which is kept so the operator sees which credential would be used.
func redactDryRunValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, child := range typed {
			if child != nil && dryRunSecretKey(key) && !isCredentialRefValue(child) {
				out[key] = dryRunRedacted
				continue
			}
			out[key] = redactDryRunValue(child)
		}
		return out
	case map[string]string:
		out := make(map[string]any, len(typed))
		for key, child := range typed {
			out[key] = child
		}
		return redactDryRunValue(out)
	case []any:
		out := make([]any, len(typed))
		for i, child := range typed {
			out[i] = redactDryRunValue(child)
		}
		return out
	}
	return value
}

func isCredentialRefValue(value any) bool {
	text, ok := value.(string)
	return ok && workflowmodels.IsWorkflowCredentialRef(text)
}
