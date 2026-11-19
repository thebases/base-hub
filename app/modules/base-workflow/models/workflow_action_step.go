package models

// File overview:
// - Responsibility: the `Step` execution DTO and the `WorkflowActionRecord` methods that convert
//   a persisted action row into that runtime shape for the retry engine. Split out of
//   `base_workflow_action.go` so the ORM model file stays limited to persistence/CRUD for
//   `base.workflow.action` and this engine-facing conversion logic lives on its own.
// - Key symbols: `Step`, `WorkflowActionRecord.ResolvedStep`, `MergeETLActionParams`.

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrWorkflowStepNonRetryable marks a step error that another attempt cannot fix (a bad signature,
// plan T103). A handler wraps it with %w; the engine then ends the step at once, as if its attempts
// were used up, so retries never hammer a deterministic failure.
var ErrWorkflowStepNonRetryable = errors.New("workflow: step error is not retryable")

// Step remains the execution DTO used by the retry engine and legacy
// compatibility wrappers. Persisted workflow actions are converted into this
// runtime shape before execution starts.
type Step struct {
	Key              string                 `json:"step_key,omitempty"`
	Name             string                 `json:"name,omitempty"`
	Type             string                 `json:"step_type,omitempty"`
	Action           string                 `json:"action"`
	ActionVersion    int                    `json:"action_version,omitempty"`
	TimeoutSeconds   int                    `json:"timeout_seconds,omitempty"`
	RetryPolicy      RetryPolicy            `json:"retry_policy,omitempty"`
	InputContract    map[string]interface{} `json:"input_contract,omitempty"`
	OutputContract   map[string]interface{} `json:"output_contract,omitempty"`
	ContinueOnError  bool                   `json:"continue_on_error,omitempty"`
	BranchLabel      string                 `json:"branch_label,omitempty"`
	ConditionStepKey string                 `json:"condition_step_key,omitempty"`
	Params           map[string]interface{} `json:"params,omitempty"`
	// OnErrorStepKey (plan T103, linear runs only) names a LATER step that runs when this step
	// fails terminally; the run continues there with the payload plus `_error`. Authored as
	// extra_params.on_error_step_key and kept here so the published snapshot pins it.
	OnErrorStepKey string `json:"on_error_step_key,omitempty"`
}

// StepOnErrorParam is the extra_params key holding Step.OnErrorStepKey.
const StepOnErrorParam = "on_error_step_key"

func (s Step) ResolvedName(index int) string {
	if s.Name != "" {
		return s.Name
	}
	if s.Action != "" {
		return fmt.Sprintf("%s#%d", s.Action, index+1)
	}
	return fmt.Sprintf("step_%d", index+1)
}

func (s Step) ResolvedKey(index int) string {
	if s.Key != "" {
		return s.Key
	}
	return s.ResolvedName(index)
}

func (s Step) ResolvedType() string {
	if stepType := strings.TrimSpace(s.Type); stepType != "" {
		return stepType
	}
	return WorkflowStepTypeAction
}

func (s Step) ResolvedActionVersion() int {
	if s.ActionVersion > 0 {
		return s.ActionVersion
	}
	return 1
}

// RetryPolicy is the single source of truth for retry attempts; a step that
// declares no policy (or a non-positive MaxAttempts) falls back to
// DefaultRetryPolicy instead of running unretried.
func (s Step) ResolvedMaxAttempts() int {
	if s.RetryPolicy.MaxAttempts > 0 {
		return s.RetryPolicy.MaxAttempts
	}
	return DefaultRetryPolicy().MaxAttempts
}

// RetryPolicy.RetryCoolDown is the single source of truth for the pause
// between attempts; falls back to DefaultRetryPolicy when unset.
func (s Step) ResolvedBackoff() time.Duration {
	if s.RetryPolicy.RetryCoolDown > 0 {
		return time.Duration(min(s.RetryPolicy.RetryCoolDown, maxStepRetryCoolDownSeconds)) * time.Second
	}
	return time.Duration(DefaultRetryPolicy().RetryCoolDown) * time.Second
}

// defaultStepTimeout applies when a step declares no timeout, matching the
// `default:30` DB column default on WorkflowActionRecord.TimeoutSeconds.
// Without this fallback, a step handler with no timeout configured can hang
// the request goroutine forever, stranding the run at status=running.
const defaultStepTimeout = 30 * time.Second

// Upper bounds applied when a step runs (plan T49), so a stored value cannot park a run for weeks
// or hold its lease with an endless step: 15 minutes per attempt, 1 day between attempts.
const (
	maxStepTimeoutSeconds       = 900
	maxStepRetryCoolDownSeconds = 86400
)

func (s Step) ResolvedTimeout() time.Duration {
	if s.TimeoutSeconds > 0 {
		return time.Duration(min(s.TimeoutSeconds, maxStepTimeoutSeconds)) * time.Second
	}
	return defaultStepTimeout
}

func (r WorkflowActionRecord) ResolvedStep() Step {
	params := MergeETLActionParams(r.ResolvedDefaultParams(), r.RuntimeParams())
	return Step{
		Key:              r.ResolvedStepKey(),
		Name:             strings.TrimSpace(r.Name),
		Type:             WorkflowStepTypeAction,
		Action:           r.ResolvedActionCode(),
		ActionVersion:    r.ResolvedActionVersion(),
		TimeoutSeconds:   r.TimeoutSeconds,
		RetryPolicy:      r.RetryPolicy,
		InputContract:    cloneActionParams(r.InputContract),
		OutputContract:   cloneActionParams(r.OutputContract),
		ContinueOnError:  r.ContinueOnError,
		BranchLabel:      strings.TrimSpace(r.BranchLabel),
		ConditionStepKey: strings.TrimSpace(r.ConditionStepKey),
		Params:           params,
		OnErrorStepKey:   strings.TrimSpace(stringValue(r.ExtraParams[StepOnErrorParam])),
	}
}

func (r WorkflowActionRecord) ResolvedDefaultParams() map[string]any {
	entry, ok := ResolveBuiltinWorkflowAction(r.ResolvedActionCode(), r.ResolvedActionVersion())
	if !ok {
		return map[string]any{}
	}
	return cloneActionParams(entry.DefaultParams)
}

func (r WorkflowActionRecord) RuntimeParams() map[string]any {
	params := cloneActionParams(r.ExtraParams)
	if params == nil {
		params = map[string]any{}
	}

	if modelName := strings.TrimSpace(r.ModelName); modelName != "" {
		params["model"] = modelName
	}
	if targetRef := strings.TrimSpace(r.TargetRef); targetRef != "" {
		params["target_ref"] = targetRef
		params["url"] = targetRef
	}
	if channel := strings.TrimSpace(r.Channel); channel != "" {
		params["channel"] = channel
	}
	if outputField := strings.TrimSpace(r.OutputField); outputField != "" {
		params["output_field"] = outputField
	}
	if hmacKey := strings.TrimSpace(r.HmacKey); hmacKey != "" {
		params["hmac_key"] = hmacKey
		params["key"] = hmacKey
	}
	if aesKey := strings.TrimSpace(r.AesKey); aesKey != "" {
		params["aes_key"] = aesKey
		params["key"] = aesKey
	}
	if len(r.KeyList) > 0 {
		params["key_list"] = append([]string(nil), r.KeyList...)
	}
	if len(r.Headers) > 0 {
		params["headers"] = cloneActionParams(r.Headers)
	}
	// Real Authorization column takes precedence when set; otherwise a pre-existing
	// extra_params.authorization value (the pre-column canvas write path) still applies.
	if authorization := strings.TrimSpace(r.Authorization); authorization != "" {
		params["authorization"] = authorization
	}
	if len(r.Template) > 0 {
		params["template"] = cloneActionParams(r.Template)
	}
	if r.FlattenInput {
		params["flatten"] = true
	}
	if r.ReturnSavedRecord {
		params["return_saved_record"] = true
	}
	if r.IncludeRaw {
		params["include_raw"] = true
	}
	if r.IncludeKey {
		params["include_key"] = true
	}
	return params
}

func (r WorkflowActionRecord) ResolvedActionCode() string {
	if code := strings.TrimSpace(r.ActionCode); code != "" {
		return code
	}
	return "parse_payload"
}

func (r WorkflowActionRecord) ResolvedActionVersion() int {
	if r.ActionVersion > 0 {
		return r.ActionVersion
	}
	return 1
}

func (r WorkflowActionRecord) ResolvedStepKey() string {
	if key := strings.TrimSpace(r.StepKey); key != "" {
		return key
	}
	return defaultWorkflowActionStepKey(r.ResolvedActionCode(), r.Name)
}

// MergeETLActionParams merges the etl action params handled by this package.
func MergeETLActionParams(defaults, overrides map[string]any) map[string]any {
	merged := cloneActionParams(defaults)
	for key, value := range overrides {
		merged[key] = value
	}
	if merged == nil {
		return map[string]any{}
	}
	return merged
}

// cloneActionParams clones the action params handled by this package.
func cloneActionParams(values map[string]any) map[string]any {
	if values == nil {
		return nil
	}
	cloned := make(map[string]any, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}
