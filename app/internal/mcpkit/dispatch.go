package mcpkit

// File overview:
// - Responsibility: the generic runtime-model dispatch primitives every MCP
//   tool group (base.*, website.*, workflow.*, and any future module's mcp/
//   package) funnels through instead of reimplementing ACL/record-rule/
//   company-scope enforcement per module.
// - Key symbols: RequestContext, RunInTenantTx, ExecuteRuntimeMethod,
//   LookupRuntimeMethod, RecordMode, RecordDispatch, RecordArgs,
//   ValidatedSearchLimit.
// - Pattern: extracted verbatim (only renamed to exported identifiers) out of
//   modules/base-mcp/routes/mcp_route.go so every module's mcp/dispatch.go
//   can reach the same choke point without importing modules/base-mcp.

import (
	"context"
	"errors"
	"fmt"

	"git.thebase.vn/base_hub/internal/modelruntime"
	"git.thebase.vn/base_hub/internal/orm"
	"github.com/gofiber/fiber/v3"
	"github.com/uptrace/bun"
)

// RequestContext binds the authenticated MCP caller into the same ORM
// environment shape /rpc builds, without elevating to sudo unless the
// matched user is already a superadmin. When ctx already carries an
// environment with a bound DB handle (e.g. an open transaction started by
// RunInTenantTx), that handle is reused instead of discarded, so every nested
// runtime call inside the same tools/call joins the same transaction. A
// fresh identity-derived environment is still built every time so RBAC
// context (login/groups/company) stays correct even when reusing a tx handle.
func RequestContext(ctx context.Context, identity *orm.RBACIdentity) context.Context {
	env := orm.EnvironmentForIdentity(identity)
	if active := orm.EnvironmentFromContext(ctx); active != nil && active.DB() != nil {
		env = env.WithDB(active.DB())
	}
	return orm.ContextWithEnvironment(ctx, env)
}

// RunInTenantTx opens a DB transaction bound to identity's tenant environment
// and runs fn inside it; every ExecuteRuntimeMethod call fn makes (directly
// or via a tool dispatch func) automatically joins the same transaction
// because RequestContext reuses whatever DB handle is already bound to its
// ctx. This is the multi-step/atomicity primitive backing per-id batch
// writes and base.batch.execute: if any step fails, every write already done
// earlier in the same call rolls back instead of leaving partial state.
func RunInTenantTx(ctx context.Context, identity *orm.RBACIdentity, fn func(context.Context) error) error {
	requestCtx := RequestContext(ctx, identity)
	env := orm.EnvironmentFromContext(requestCtx)
	if db, ok := env.DB().(*bun.DB); ok {
		// This call owns the transaction, so it owns the after-commit queue:
		// record events of the batch fire only once the whole batch committed.
		return orm.WithAfterCommitScope(requestCtx, func(scopeCtx context.Context) error {
			return db.RunInTx(scopeCtx, nil, func(txCtx context.Context, tx bun.Tx) error {
				return fn(orm.ContextWithEnvironment(txCtx, env.WithDB(tx)))
			})
		})
	}
	// Already inside a transaction (nested RunInTenantTx call) or no concrete
	// *bun.DB handle available; run directly rather than opening a second,
	// unrelated transaction.
	return fn(requestCtx)
}

// ExecuteRuntimeMethod is the single choke point every MCP tool funnels
// through to reach the runtime dispatch pipeline: model ACL check, then
// modelruntime.Service.Execute. Centralizing it here (instead of each caller
// repeating EnsureRuntimeModelAccess + Execute) keeps ACL/record-rule/company
// scope enforcement identical across every tool group.
func ExecuteRuntimeMethod(ctx context.Context, identity *orm.RBACIdentity, meta *orm.ModelMetadata, method orm.RuntimeMethod, ids []string, args []any, kwargs map[string]any) (any, error) {
	requestCtx := RequestContext(ctx, identity)
	if method.Internal {
		return nil, NewToolError(fiber.StatusNotFound, "method_not_found", orm.ErrInternalMethodNotCallable(meta.Name, method.Name).Error())
	}
	if err := orm.EnsureRuntimeModelAccess(requestCtx, meta, method, ids); err != nil {
		return nil, NewToolError(fiber.StatusForbidden, "permission_denied", err.Error())
	}
	// Tool arguments are untrusted: drop the ORM environment overrides (applied after the ACL check).
	kwargs = orm.SanitizeClientKwargs(kwargs)
	result, err := modelruntime.NewService().Execute(orm.ContextWithClientReadCap(requestCtx), modelruntime.Request{
		Model:  meta.Name,
		Method: method.Name,
		IDs:    ids,
		Args:   args,
		Kwargs: kwargs,
	})
	if err != nil {
		return nil, NewToolError(fiber.StatusBadRequest, "invalid_operation", err.Error())
	}
	return orm.RedactSecretFieldsForClient(meta, method.Name, result), nil
}

// LookupRuntimeMethod resolves a model + runtime method pair with a bare
// orm.LookupModel check -- no fallback/knowledge-hint behavior (see
// ResolveRuntimeMethod in fallback.go for that). Used by every call site that
// passes a hardcoded, compile-guaranteed model/method name (e.g. a module's
// own pinned-wrapper tools), where a lookup miss can only mean a real bug,
// never a caller typo worth hinting at.
func LookupRuntimeMethod(modelName, methodName string) (*orm.ModelMetadata, orm.RuntimeMethod, error) {
	meta, ok := orm.LookupModel(modelName)
	if !ok {
		return nil, orm.RuntimeMethod{}, NewToolError(fiber.StatusNotFound, "model_not_found", fmt.Sprintf("model %q is not registered", modelName))
	}
	method, ok := meta.LookupRuntimeMethod(methodName)
	if !ok {
		return nil, orm.RuntimeMethod{}, NewToolError(fiber.StatusNotFound, "method_not_found", fmt.Sprintf("runtime method %q is not registered on model %q", methodName, modelName))
	}
	return meta, method, nil
}

// RecordMode selects which generic record.* tool shape RecordDispatch/
// RecordArgs are backing (search/read/create/write/unlink), or a
// model-metadata-only call with no id requirement.
type RecordMode int

const (
	RecordModeSearch RecordMode = iota
	RecordModeRead
	RecordModeCreate
	RecordModeWrite
	RecordModeUnlink
	RecordModeModelOnly
)

// SearchReadDefaultLimit/SearchReadMaxLimit bound arguments.limit for every
// search_read-shaped tool (base.record.search_read, website.record.search_read,
// workflow.record.search_read). An absent limit defaults, a present one
// clamps rather than erroring outright on "too large" -- see
// ValidatedSearchLimit.
const (
	SearchReadDefaultLimit = 20
	SearchReadMaxLimit     = 200
)

// ValidatedSearchLimit resolves and clamps arguments.limit for a
// search_read-shaped call: absent -> SearchReadDefaultLimit, present but not
// a positive number -> invalid_params error, present and over the ceiling ->
// silently clamped to SearchReadMaxLimit.
func ValidatedSearchLimit(arguments map[string]any) (int64, error) {
	v, ok := arguments["limit"]
	if !ok {
		return SearchReadDefaultLimit, nil
	}
	n, numOK := Int64FromArgument(v)
	if !numOK || n <= 0 {
		return 0, NewToolError(fiber.StatusUnprocessableEntity, "invalid_params", "arguments.limit must be a positive integer")
	}
	if n > SearchReadMaxLimit {
		n = SearchReadMaxLimit
	}
	return n, nil
}

// RecordArgs normalizes each generic record tool's MCP arguments into the
// same args/kwargs shape the standard runtime method bodies already parse,
// so domains, values, and field lists behave identically to /rpc across every
// tool group -- except limit, which always clamps, and every typed argument,
// which is validated instead of silently dropped when malformed.
func RecordArgs(arguments map[string]any, mode RecordMode) ([]any, map[string]any, error) {
	switch mode {
	case RecordModeSearch:
		kwargs := map[string]any{}
		domain, err := RequireArrayArg(arguments, "domain")
		if err != nil {
			return nil, nil, err
		}
		if domain != nil {
			kwargs["domain"] = domain
		}
		fields, err := RequireArrayArg(arguments, "fields")
		if err != nil {
			return nil, nil, err
		}
		if fields != nil {
			kwargs["fields"] = fields
		}
		limit, err := ValidatedSearchLimit(arguments)
		if err != nil {
			return nil, nil, err
		}
		kwargs["limit"] = limit
		if offset, ok := arguments["offset"]; ok {
			n, numOK := Int64FromArgument(offset)
			if !numOK || n < 0 {
				return nil, nil, NewToolError(fiber.StatusUnprocessableEntity, "invalid_params", "arguments.offset must be a non-negative integer")
			}
			kwargs["offset"] = n
		}
		if order, ok := arguments["order"]; ok {
			orderStr, isStr := order.(string)
			if !isStr {
				return nil, nil, NewToolError(fiber.StatusUnprocessableEntity, "invalid_params", "arguments.order must be a string")
			}
			kwargs["order"] = orderStr
		}
		return nil, kwargs, nil
	case RecordModeRead:
		kwargs := map[string]any{}
		fields, err := RequireArrayArg(arguments, "fields")
		if err != nil {
			return nil, nil, err
		}
		if fields != nil {
			kwargs["fields"] = fields
		}
		return nil, kwargs, nil
	case RecordModeCreate, RecordModeWrite:
		values, err := RequireObjectArg(arguments, "values")
		if err != nil {
			return nil, nil, err
		}
		if values == nil {
			return nil, nil, NewToolError(fiber.StatusUnprocessableEntity, "invalid_params", "arguments.values is required and must be an object")
		}
		return []any{values}, nil, nil
	case RecordModeUnlink:
		kwargs := map[string]any{}
		if confirmDropTable, ok := arguments["confirm_drop_table"]; ok {
			b, isBool := confirmDropTable.(bool)
			if !isBool {
				return nil, nil, NewToolError(fiber.StatusUnprocessableEntity, "invalid_params", "arguments.confirm_drop_table must be a boolean")
			}
			kwargs["confirm_drop_table"] = b
		}
		return nil, kwargs, nil
	default:
		return nil, nil, nil
	}
}

// RecordDispatch maps one generic <group>.record.* tool onto the exact
// runtime-method dispatch pipeline used by POST /rpc/{model}/{method}: model
// lookup, runtime method lookup, EnsureRuntimeModelAccess, then
// modelruntime.Service.Execute. Callers (base.record.*, website.record.*,
// workflow.record.*) validate their own model allowlist before calling this.
func RecordDispatch(ctx context.Context, identity *orm.RBACIdentity, arguments map[string]any, methodName string, mode RecordMode) (any, error) {
	modelName := StringFromAny(arguments["model"])
	if modelName == "" {
		return nil, NewToolError(fiber.StatusUnprocessableEntity, "invalid_params", "arguments.model is required")
	}
	meta, method, err := ResolveRuntimeMethod(ctx, identity, modelName, methodName)
	if err != nil {
		return nil, err
	}

	ids, err := RequireIDsArg(arguments)
	if err != nil {
		return nil, err
	}
	if method.UsesPathID && len(ids) == 0 && mode != RecordModeModelOnly {
		return nil, NewToolError(fiber.StatusUnprocessableEntity, "invalid_params", fmt.Sprintf("arguments.ids is required for %q", methodName))
	}

	// Per-id batch write: arguments.values as an array (one object per id,
	// same order as arguments.ids) instead of one object applied to every id.
	// Runs every id's write inside one DB transaction via RunInTenantTx so a
	// mid-batch failure rolls back records already written earlier in the
	// same call, rather than leaving a partially-applied batch.
	if mode == RecordModeWrite {
		if perIDValues, isBatch, err := perIDWriteValues(arguments, ids); err != nil {
			return nil, err
		} else if isBatch {
			return batchWriteRecords(ctx, identity, meta, method, ids, perIDValues)
		}
	}

	args, kwargs, err := RecordArgs(arguments, mode)
	if err != nil {
		return nil, err
	}
	return ExecuteRuntimeMethod(ctx, identity, meta, method, ids, args, kwargs)
}

// perIDWriteValues detects the per-id batch write form of arguments.values (a
// JSON array instead of a single object) and validates it 1:1 against ids.
// Returns isBatch=false (not an error) when values is absent or is the
// existing single-object form, so RecordDispatch falls through to the
// unchanged apply-to-all-ids path.
func perIDWriteValues(arguments map[string]any, ids []string) ([]map[string]any, bool, error) {
	raw, ok := arguments["values"]
	if !ok {
		return nil, false, nil
	}
	list, isList := raw.([]any)
	if !isList {
		return nil, false, nil
	}
	if len(list) != len(ids) {
		return nil, false, NewToolError(fiber.StatusUnprocessableEntity, "invalid_params", fmt.Sprintf(
			"arguments.values has %d entries but arguments.ids has %d; a per-record batch write needs exactly one values object per id, in the same order",
			len(list), len(ids),
		))
	}
	out := make([]map[string]any, len(list))
	for i, item := range list {
		obj, ok := item.(map[string]any)
		if !ok {
			return nil, false, NewToolError(fiber.StatusUnprocessableEntity, "invalid_params", fmt.Sprintf("arguments.values[%d] must be an object", i))
		}
		out[i] = obj
	}
	return out, true, nil
}

// batchWriteRecords applies values[i] to ids[i] one at a time, all inside a
// single DB transaction (RunInTenantTx) so the batch is atomic: any record's
// write failing rolls back every write already done earlier in the same call
// instead of leaving some ids updated and others not.
func batchWriteRecords(ctx context.Context, identity *orm.RBACIdentity, meta *orm.ModelMetadata, method orm.RuntimeMethod, ids []string, values []map[string]any) (any, error) {
	results := make([]any, len(ids))
	err := RunInTenantTx(ctx, identity, func(txCtx context.Context) error {
		for i, id := range ids {
			record, err := ExecuteRuntimeMethod(txCtx, identity, meta, method, []string{id}, []any{values[i]}, nil)
			if err != nil {
				return err
			}
			results[i] = record
		}
		return nil
	})
	if err != nil {
		var toolErr *ToolError
		if errors.As(err, &toolErr) {
			return nil, toolErr
		}
		return nil, NewToolError(fiber.StatusBadRequest, "invalid_operation", err.Error())
	}
	return results, nil
}
