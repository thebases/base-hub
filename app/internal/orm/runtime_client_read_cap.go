package orm

// File overview:
// - Responsibility: row cap for list reads requested directly by a client transport
//   (plan T36, SEC-D-21).
// - Key symbols: `ContextWithClientReadCap`, `ClampClientReadLimit`, `applyClientReadCap`.
// - Pattern: the transport marks the context; ExecuteRuntimeMethod turns the mark into a cap for
//   that one top-level call and clears it for every nested call, so server-side searches made on
//   the caller's behalf (record-rule id lists, exports, compute) still read every row.

import (
	"context"

	"git.thebase.vn/base_hub/internal/config"
)

type clientReadCapPendingKey struct{}

type clientReadCapActiveKey struct{}

// clientReadCapCall names the top-level call the cap applies to.
type clientReadCapCall struct{ model string }

// ContextWithClientReadCap marks ctx as a client transport call (/rpc, REST alias, MCP). The next
// ExecuteRuntimeMethod on ctx applies server.maxReadLimit to its list method.
// A Go context value: a client cannot set or clear it.
func ContextWithClientReadCap(ctx context.Context) context.Context {
	return context.WithValue(ctx, clientReadCapPendingKey{}, true)
}

// scopeClientReadCap is run by ExecuteRuntimeMethod: a pending mark becomes the active cap of
// this call, and any call already under an active cap (a nested call) runs uncapped.
func scopeClientReadCap(ctx context.Context, model *ModelMetadata) context.Context {
	if pending, _ := ctx.Value(clientReadCapPendingKey{}).(bool); pending {
		ctx = context.WithValue(ctx, clientReadCapPendingKey{}, false)
		return context.WithValue(ctx, clientReadCapActiveKey{}, clientReadCapCall{model: model.Name})
	}
	if _, active := ctx.Value(clientReadCapActiveKey{}).(clientReadCapCall); active {
		return context.WithValue(ctx, clientReadCapActiveKey{}, nil)
	}
	return ctx
}

// clientReadCapFor returns the cap for a kernel search on model, or 0 when the call is not the
// top-level client call (server-side caller, nested call, sudo environment).
func clientReadCapFor(ctx context.Context, model *ModelMetadata) int {
	call, ok := ctx.Value(clientReadCapActiveKey{}).(clientReadCapCall)
	if !ok || model == nil || call.model != model.Name {
		return 0
	}
	if env := EnvironmentFromContext(ctx); env != nil && env.IsSudo() {
		return 0
	}
	cfg := config.Get()
	if cfg == nil {
		return config.DefaultMaxReadLimit
	}
	return cfg.Server.ResolvedMaxReadLimit()
}

// applyClientReadCap clamps filter["__limit"] for a capped client call: no limit or a larger one
// becomes the cap. Only list methods use it; read_group/cohort_data return grouped results, not
// rows, so they stay uncapped (their in-memory grouping cost is a separate follow-up).
func applyClientReadCap(ctx context.Context, model *ModelMetadata, filter RecordMap) {
	if limit := ClampClientReadLimit(ctx, model, int(toInt64(filter["__limit"]))); limit > 0 {
		filter["__limit"] = limit
	}
}

// ClampClientReadLimit returns the row limit a model override of a list method (search_read,
// name_search, ...) must use: requested unchanged for server-side calls, else requested bounded
// by server.maxReadLimit (0 or a larger value becomes the cap). Overrides that read through
// RuntimeRepository* call it so a client cannot list a whole table through them.
func ClampClientReadLimit(ctx context.Context, model *ModelMetadata, requested int) int {
	limit := clientReadCapFor(ctx, model)
	if limit <= 0 || (requested > 0 && requested <= limit) {
		return requested
	}
	return limit
}
