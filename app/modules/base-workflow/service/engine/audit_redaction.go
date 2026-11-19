package engine

// File overview:
// - Responsibility: what a workflow run stores about its payloads (plan T45). Every run write goes
//   through markRun/createWorkflowRun, which pass the values through auditRunValues here.
// - Key symbols: `auditPolicy`, `newAuditPolicy`, `contextWithAuditPolicy`, `auditRunValues`.
//
// Rules:
//   - input_payload and every step_runs[] input/output snapshot are always stored through the
//     policy (snapshots are display-only: DAG resume reads only step status and attempt counts).
//   - output_payload is the resume checkpoint (resumeStateForRun / resumeStateForDAGRun rebuild the
//     payload from it), so it stays complete while the run can still resume (pending, running,
//     waiting_retry) and goes through the policy once the run reaches a final status.
//   - Policy writes are idempotent: markRun re-sends the whole step_runs[] list on every update.
//   - A stored step snapshot larger than maxStepSnapshotBytes (after the policy) is replaced by
//     {"_truncated":true,"bytes":N,"sha256":"..."} (plan T58), so run rows stop growing with the
//     payload of every attempt. output_payload (the resume checkpoint) is never truncated.

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"

	"git.thebase.vn/base_hub/internal/config"

	"git.thebase.vn/base_hub/internal/orm"
	workflowmodels "git.thebase.vn/base_hub/modules/base-workflow/models"
)

// auditRedactedValue replaces the value of a sensitive key in full mode.
const auditRedactedValue = "[redacted]"

// auditMarkerKey tags the stored shape of a hash/none payload, so it is recognized (and not
// hashed again) when the same snapshot is written twice.
const auditMarkerKey = "_audit"

// builtinAuditRedactKeys are always hidden, compared after normalizeAuditKey. They cover HTTP
// credentials and card data (ISO 8583: PAN, track 2, PIN block, CVV).
var builtinAuditRedactKeys = map[string]struct{}{
	"authorization": {}, "proxy_authorization": {}, "cookie": {}, "set_cookie": {},
	"password": {}, "passwd": {}, "secret": {}, "token": {}, "key": {},
	"pan": {}, "card_number": {}, "track2": {}, "track_2": {}, "track2_data": {},
	"pin": {}, "pin_block": {}, "pinblock": {}, "cvv": {}, "cvv2": {}, "cvc": {}, "cvc2": {},
}

// builtinAuditRedactSuffixes hide any key ending with them (api_key, _aes_key, client_secret, ...).
var builtinAuditRedactSuffixes = []string{"_key", "_secret", "_token", "_password"}

// auditPolicy is resolved once per run: the trigger's mode plus the workflow's extra keys.
type auditPolicy struct {
	mode  string
	keys  map[string]struct{}
	paths map[string]struct{}
}

// normalizeAuditKey makes key matching case-insensitive and treats "-" like "_" (header names).
func normalizeAuditKey(key string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(key)), "-", "_")
}

// newAuditPolicy builds the policy for mode (unknown → full) and the workflow's
// audit_redact_keys: comma, semicolon or newline separated; an entry with a dot is a path from the
// payload root, any other entry a key name matched at any depth.
func newAuditPolicy(mode, extraKeys string) auditPolicy {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if !workflowmodels.IsWorkflowAuditPayloadMode(mode) {
		mode = workflowmodels.WorkflowAuditPayloadFull
	}
	policy := auditPolicy{mode: mode}
	for _, entry := range strings.FieldsFunc(extraKeys, func(r rune) bool { return r == ',' || r == ';' || r == '\n' || r == '\r' }) {
		// Empty segments ("customer.", "a..b") are dropped so the entry still matches something.
		parts := make([]string, 0, 2)
		for _, part := range strings.Split(entry, ".") {
			if part = normalizeAuditKey(part); part != "" {
				parts = append(parts, part)
			}
		}
		if len(parts) == 0 {
			continue
		}
		normalized := strings.Join(parts, ".")
		if len(parts) > 1 {
			if policy.paths == nil {
				policy.paths = map[string]struct{}{}
			}
			policy.paths[normalized] = struct{}{}
			continue
		}
		if policy.keys == nil {
			policy.keys = map[string]struct{}{}
		}
		policy.keys[normalized] = struct{}{}
	}
	return policy
}

// sensitive reports whether the value at path (dotted, normalized) under key must be hidden.
func (p auditPolicy) sensitive(path, key string) bool {
	if _, ok := builtinAuditRedactKeys[key]; ok {
		return true
	}
	for _, suffix := range builtinAuditRedactSuffixes {
		if strings.HasSuffix(key, suffix) {
			return true
		}
	}
	if _, ok := p.keys[key]; ok {
		return true
	}
	_, ok := p.paths[path]
	return ok
}

// apply returns the stored form of one audit payload. It never mutates payload.
func (p auditPolicy) apply(payload map[string]any) map[string]any {
	if len(payload) == 0 || isAuditMarker(payload) || isTruncatedSnapshot(payload) {
		return payload
	}
	switch p.mode {
	case workflowmodels.WorkflowAuditPayloadNone:
		return map[string]any{auditMarkerKey: workflowmodels.WorkflowAuditPayloadNone}
	case workflowmodels.WorkflowAuditPayloadHash:
		// encoding/json sorts map keys, so equal payloads digest equally.
		encoded, err := json.Marshal(payload)
		if err != nil {
			return map[string]any{auditMarkerKey: workflowmodels.WorkflowAuditPayloadNone}
		}
		stored := map[string]any{
			auditMarkerKey: workflowmodels.WorkflowAuditPayloadHash,
			"bytes":        len(encoded),
		}
		// Keyed digest: a plain sha256 of a card message can be brute-forced (the PAN space is
		// small and the other fields are guessable). Without a server key only the size is kept.
		if key := auditDigestKey(); len(key) > 0 {
			mac := hmac.New(sha256.New, key)
			mac.Write(encoded)
			stored[auditDigestField] = hex.EncodeToString(mac.Sum(nil))
		}
		return stored
	}
	redacted, _ := p.redact(payload, "").(map[string]any)
	return redacted
}

// redact copies value with every sensitive key's value replaced. Array elements keep their
// parent's path (paths have no index segment).
func (p auditPolicy) redact(value any, path string) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, child := range typed {
			normalizedKey := normalizeAuditKey(key)
			childPath := normalizedKey
			if path != "" {
				childPath = path + "." + normalizedKey
			}
			if child != nil && p.sensitive(childPath, normalizedKey) {
				out[key] = auditRedactedValue
				continue
			}
			out[key] = p.redact(child, childPath)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, child := range typed {
			out[i] = p.redact(child, path)
		}
		return out
	default:
		return value
	}
}

// auditDigestField holds the keyed digest of a hash-mode payload.
const auditDigestField = "hmac_sha256"

// auditDigestKey derives the hash-mode HMAC key from app.encryptKey (nil when unset). Swappable in tests.
var auditDigestKey = func() []byte {
	secret := strings.TrimSpace(config.Get().App.EncryptKey)
	if secret == "" {
		return nil
	}
	sum := sha256.Sum256([]byte("base-workflow/audit-digest\x00" + secret))
	return sum[:]
}

// isAuditMarker recognizes a payload already stored in hash or none form. It checks the exact
// stored shape: a caller-sent {"_audit":"none", "pan": ...} must still be redacted.
func isAuditMarker(payload map[string]any) bool {
	switch payload[auditMarkerKey] {
	case workflowmodels.WorkflowAuditPayloadNone:
		return len(payload) == 1
	case workflowmodels.WorkflowAuditPayloadHash:
	default:
		return false
	}
	switch payload["bytes"].(type) {
	case int, float64: // int in memory, float64 after a jsonb read
	default:
		return false
	}
	digest, hasDigest := payload[auditDigestField]
	if !hasDigest {
		return len(payload) == 2
	}
	text, ok := digest.(string)
	if !ok || len(text) != 2*sha256.Size || len(payload) != 3 {
		return false
	}
	_, err := hex.DecodeString(text)
	return err == nil
}

type auditPolicyContextKey struct{}

func contextWithAuditPolicy(ctx context.Context, policy auditPolicy) context.Context {
	return context.WithValue(workflowContextOrBackground(ctx), auditPolicyContextKey{}, policy)
}

// auditPolicyFromContext returns the run's policy; without one (ExecuteSteps, tests) the built-in
// keys are still hidden.
func auditPolicyFromContext(ctx context.Context) auditPolicy {
	if ctx != nil {
		if policy, ok := ctx.Value(auditPolicyContextKey{}).(auditPolicy); ok {
			return policy
		}
	}
	return newAuditPolicy(workflowmodels.WorkflowAuditPayloadFull, "")
}

// bindRunAuditPolicy resolves the run's mode (stored mode on resume, else the trigger's, else the
// transport default) and binds the policy. It returns the mode to persist on the run row.
func bindRunAuditPolicy(ctx context.Context, flow workflowmodels.FlowRecord, storedMode, triggerType string) (context.Context, string) {
	mode := strings.TrimSpace(storedMode)
	if !workflowmodels.IsWorkflowAuditPayloadMode(mode) {
		mode = workflowmodels.WorkflowAuditModeFromContext(ctx)
	}
	if mode == "" {
		mode = workflowmodels.DefaultWorkflowAuditMode(triggerType)
	}
	return contextWithAuditPolicy(ctx, newAuditPolicy(mode, flow.AuditRedactKeys)), mode
}

// auditRunStatusFinal reports the run statuses after which output_payload is no longer read to resume.
func auditRunStatusFinal(status string) bool {
	switch status {
	case workflowmodels.WorkflowRunStatusSucceeded, workflowmodels.WorkflowRunStatusFailed,
		workflowmodels.WorkflowRunStatusDeadLetter, workflowmodels.WorkflowRunStatusCancelled,
		workflowmodels.WorkflowRunStatusSkipped:
		return true
	}
	return false
}

// auditRunValues returns a copy of a run write with its payloads stored through the run's policy.
func auditRunValues(ctx context.Context, values orm.RecordMap) orm.RecordMap {
	policy := auditPolicyFromContext(ctx)
	out := make(orm.RecordMap, len(values))
	for key, value := range values {
		out[key] = value
	}
	if payload, ok := values["input_payload"].(map[string]any); ok {
		out["input_payload"] = policy.apply(payload)
	}
	if payload, ok := values["output_payload"].(map[string]any); ok {
		status, _ := values["status"].(string)
		if auditRunStatusFinal(status) {
			out["output_payload"] = policy.apply(payload)
		}
	}
	if stepRuns, ok := values["step_runs"].([]workflowmodels.WorkflowStepRun); ok && stepRuns != nil {
		stored := make([]workflowmodels.WorkflowStepRun, len(stepRuns))
		for i, stepRun := range stepRuns {
			stepRun.InputSnapshot = capStepSnapshot(policy.apply(stepRun.InputSnapshot))
			stepRun.OutputSnapshot = capStepSnapshot(policy.apply(stepRun.OutputSnapshot))
			stored[i] = stepRun
		}
		out["step_runs"] = stored
	}
	return out
}

// maxStepSnapshotBytes caps one stored step_runs[] snapshot (its JSON encoding).
const maxStepSnapshotBytes = 16 * 1024

// Keys of a truncated snapshot.
const (
	snapshotTruncatedKey = "_truncated"
	snapshotBytesKey     = "bytes"
	snapshotDigestKey    = "sha256"
)

// capStepSnapshot replaces a stored snapshot above maxStepSnapshotBytes with its size and sha256.
// It runs after the audit policy, so the digest is over the already redacted form (never over a
// secret the policy hides), and it is idempotent: a truncated snapshot read back from the run row
// and written again is kept as is.
func capStepSnapshot(snapshot map[string]any) map[string]any {
	if len(snapshot) == 0 || isTruncatedSnapshot(snapshot) {
		return snapshot
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil || len(encoded) <= maxStepSnapshotBytes {
		return snapshot
	}
	sum := sha256.Sum256(encoded)
	return map[string]any{
		snapshotTruncatedKey: true,
		snapshotBytesKey:     len(encoded),
		snapshotDigestKey:    hex.EncodeToString(sum[:]),
	}
}

// isTruncatedSnapshot recognizes the exact stored shape of a truncated snapshot (three keys), so
// a caller payload that merely contains "_truncated" is still redacted.
func isTruncatedSnapshot(payload map[string]any) bool {
	if len(payload) != 3 || payload[snapshotTruncatedKey] != true {
		return false
	}
	switch payload[snapshotBytesKey].(type) {
	case int, float64: // int in memory, float64 after a jsonb read
	default:
		return false
	}
	digest, ok := payload[snapshotDigestKey].(string)
	if !ok || len(digest) != 2*sha256.Size {
		return false
	}
	_, err := hex.DecodeString(digest)
	return err == nil
}
