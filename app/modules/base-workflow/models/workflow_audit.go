package models

// File overview:
// - Responsibility: the audit storage mode of a workflow run (plan T45): the trigger config key
//   `audit_payload`, its validation and the context value the engine reads when it writes run rows.
// - Key symbols: `WorkflowAuditPayload*`, `NormalizeTriggerAuditPayload`, `ContextWithWorkflowAuditMode`.

import (
	"context"
	"strings"

	"git.thebase.vn/base_hub/internal/orm"
)

// Audit modes for the payloads stored on base.workflow.run (input_payload, output_payload once the
// run is finished, step_runs snapshots):
//   - full: the payload with sensitive keys replaced (default for every transport except tcp);
//   - hash: only a sha256 of the payload and its size (default for tcp: ISO 8583 card data);
//   - none: nothing.
const (
	WorkflowAuditPayloadFull = "full"
	WorkflowAuditPayloadHash = "hash"
	WorkflowAuditPayloadNone = "none"

	// TriggerConfigAuditPayload is the trigger config key that selects the audit mode.
	TriggerConfigAuditPayload = "audit_payload"
)

// IsWorkflowAuditPayloadMode reports whether mode is one of full, hash or none.
func IsWorkflowAuditPayloadMode(mode string) bool {
	switch mode {
	case WorkflowAuditPayloadFull, WorkflowAuditPayloadHash, WorkflowAuditPayloadNone:
		return true
	}
	return false
}

// DefaultWorkflowAuditMode is the mode used when the trigger sets none: tcp payloads carry card data
// (PAN, track 2, PIN block) and are hashed, everything else is stored with sensitive keys redacted.
func DefaultWorkflowAuditMode(triggerType string) string {
	if strings.EqualFold(strings.TrimSpace(triggerType), WorkflowTriggerTypeTCP) {
		return WorkflowAuditPayloadHash
	}
	return WorkflowAuditPayloadFull
}

// NormalizeTriggerAuditPayload copies a valid `audit_payload` from src into dst. A blank value is
// dropped (the transport default applies); anything else than full, hash or none is refused.
func NormalizeTriggerAuditPayload(src, dst map[string]any) error {
	raw, ok := src[TriggerConfigAuditPayload]
	if !ok || isBlankConfigValue(raw) {
		return nil
	}
	mode, isString := raw.(string)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if !isString || !IsWorkflowAuditPayloadMode(mode) {
		return orm.InvalidParamsf("trigger audit_payload must be full, hash or none")
	}
	dst[TriggerConfigAuditPayload] = mode
	return nil
}

// WorkflowAuditModeFromTriggerConfig returns the trigger's audit mode, or its transport default.
func WorkflowAuditModeFromTriggerConfig(triggerType string, config map[string]any) string {
	if mode, ok := config[TriggerConfigAuditPayload].(string); ok {
		mode = strings.ToLower(strings.TrimSpace(mode))
		if IsWorkflowAuditPayloadMode(mode) {
			return mode
		}
	}
	return DefaultWorkflowAuditMode(triggerType)
}

type workflowAuditModeContextKey struct{}

// ContextWithWorkflowAuditMode binds the audit mode chosen by the trigger that starts a run.
// An unknown or empty mode leaves ctx unchanged.
func ContextWithWorkflowAuditMode(ctx context.Context, mode string) context.Context {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if !IsWorkflowAuditPayloadMode(mode) {
		return ctx
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, workflowAuditModeContextKey{}, mode)
}

// WorkflowAuditModeFromContext returns the bound audit mode, or "" when none is bound.
func WorkflowAuditModeFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	mode, _ := ctx.Value(workflowAuditModeContextKey{}).(string)
	return mode
}
