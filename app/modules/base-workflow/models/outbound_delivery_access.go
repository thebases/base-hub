package models

// Guard for base.workflow.outbound.authorization_ref (plan T40).
//
// A manual retry resolves the row's authorization_ref and sends the secret to the row's
// target_ref. Outbound rows are not covered by the workflow authoring guard (the engine writes
// them on behalf of the caller), so a client could otherwise point a reference at any credential,
// or move an existing reference to its own URL, and call manual_retry. A non-sudo RPC caller
// therefore cannot set authorization_ref, and cannot change target_ref, headers or request_body
// on a row that has one. The engine sets the reference without an RPC identity (outbound worker)
// or through OutboundEngineContext (post_output_object running under the caller's env).

import (
	"context"
	"encoding/json"
	"strings"

	"git.thebase.vn/base_hub/internal/orm"
)

// outboundBoundFields are sent together with the resolved Authorization header on retry.
var outboundBoundFields = []string{"target_ref", "headers", "request_body"}

// outboundRestrictedCaller reports whether ctx is a non-sudo RPC caller.
func outboundRestrictedCaller(ctx context.Context) bool {
	env := orm.EnvironmentFromContext(ctx)
	if env == nil || env.IsSudo() {
		return false
	}
	_, rpc := env.ContextValue(orm.RBACContextAuthModeKey)
	return rpc
}

// OutboundEngineContext returns ctx with its environment switched to sudo (same user, database
// and context values), for the engine writing authorization_ref while it runs under a caller's
// env. A ctx without an environment is returned as is.
func OutboundEngineContext(ctx context.Context) context.Context {
	return WorkflowEngineContext(ctx)
}

// WorkflowEngineContext is the context the engine writes its own rows with (runs, deliveries):
// the caller's database, user and context values, with access checks bypassed (plan T46). A ctx
// without an environment is returned as is (default database).
func WorkflowEngineContext(ctx context.Context) context.Context {
	env := orm.EnvironmentFromContext(ctx)
	if env == nil || env.IsSudo() {
		return ctx
	}
	return orm.ContextWithEnvironment(ctx, env.WithUser(env.UserID(), true))
}

func validOutboundAuthorizationRef(raw any) error {
	ref := strings.TrimSpace(stringValue(raw))
	if ref != "" && !IsWorkflowCredentialRef(ref) {
		return orm.InvalidParamsf("%s authorization_ref must be a @cred:<name> or @syscred:<name> reference", OutboundDeliveryModelName)
	}
	return nil
}

func ensureOutboundCreateAllowed(ctx context.Context, values orm.RecordMap) error {
	raw, exists := values["authorization_ref"]
	if !exists {
		return nil
	}
	if outboundRestrictedCaller(ctx) && strings.TrimSpace(stringValue(raw)) != "" {
		return orm.Forbiddenf("permission denied: authorization_ref on %s is set by the workflow engine only", OutboundDeliveryModelName)
	}
	return validOutboundAuthorizationRef(raw)
}

func ensureOutboundWriteAllowed(ctx context.Context, rows []orm.RecordMap, values orm.RecordMap) error {
	if raw, exists := values["authorization_ref"]; exists {
		if err := validOutboundAuthorizationRef(raw); err != nil {
			return err
		}
	}
	if !outboundRestrictedCaller(ctx) {
		return nil
	}
	for _, row := range rows {
		if raw, exists := values["authorization_ref"]; exists && !sameOutboundValue(row["authorization_ref"], raw) {
			return orm.Forbiddenf("permission denied: authorization_ref on %s is set by the workflow engine only", OutboundDeliveryModelName)
		}
		if strings.TrimSpace(stringValue(row["authorization_ref"])) == "" {
			continue
		}
		for _, field := range outboundBoundFields {
			if raw, exists := values[field]; exists && !sameOutboundValue(row[field], raw) {
				return orm.Forbiddenf("permission denied: %s cannot change on a %s row that sends a credential", field, OutboundDeliveryModelName)
			}
		}
	}
	return nil
}

// guardedOutboundFindOrCreate wraps the kernel find_or_create, which writes straight to the
// repository and so would skip ensureOutboundCreateAllowed.
func guardedOutboundFindOrCreate() orm.RuntimeMethod {
	base, ok := orm.LookupRuntimeMethod("find_or_create")
	if !ok || base.Call == nil {
		panic("base-workflow models: default runtime method find_or_create is not registered")
	}
	call := base.Call
	base.Call = func(ctx context.Context, model *orm.ModelMetadata, ids []string, args []any, kwargs map[string]any) (any, error) {
		if outboundRestrictedCaller(ctx) {
			return nil, orm.Forbiddenf("permission denied: use create or write on %s", OutboundDeliveryModelName)
		}
		return call(ctx, model, ids, args, kwargs)
	}
	return base
}

// sameOutboundValue compares a stored value with an incoming one by their JSON form, so a jsonb
// object read back from the DB equals the same object sent by the client.
func sameOutboundValue(stored, incoming any) bool {
	if storedText, ok := stored.(string); ok {
		if incomingText, ok := incoming.(string); ok {
			return strings.TrimSpace(storedText) == strings.TrimSpace(incomingText)
		}
	}
	if stored == nil && incoming == nil {
		return true
	}
	left, errLeft := json.Marshal(stored)
	right, errRight := json.Marshal(incoming)
	return errLeft == nil && errRight == nil && string(left) == string(right)
}
