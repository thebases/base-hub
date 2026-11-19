package engine

import (
	"context"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	workflowmodels "git.thebase.vn/base_hub/modules/base-workflow/models"
)

func TestObserveStepRecordsActionAndStatus(t *testing.T) {
	step := workflowmodels.Step{Action: "no_such_action_code"}
	before := testutil.CollectAndCount(workflowStepDuration)
	observeStep(step, metricStatusSuccess, 20*time.Millisecond)
	observeStep(step, metricStatusSuccess, 20*time.Millisecond)
	observeStep(step, metricStatusSkipped, 0)
	if got := testutil.CollectAndCount(workflowStepDuration); got < before {
		t.Fatalf("series count shrank: %d -> %d", before, got)
	}
	// Unknown codes are folded into "other" so the label stays bounded.
	if got := metricAction(step); got != metricLabelOther {
		t.Fatalf("metricAction = %q, want other", got)
	}
	if got := stepOutcomeStatus(context.Canceled); got != metricStatusFailed {
		t.Fatalf("stepOutcomeStatus(err) = %q", got)
	}
}

func TestCountRunTransitionOnlyFinalStatuses(t *testing.T) {
	ctx := workflowmodels.ContextWithWorkflowTriggerType(context.Background(), workflowmodels.WorkflowTriggerTypeKafka)
	succeeded := workflowRunsTotal.WithLabelValues("kafka", workflowmodels.WorkflowRunStatusSucceeded)
	retry := workflowRunsTotal.WithLabelValues("kafka", workflowmodels.WorkflowRunStatusRetryWaiting)
	other := workflowRunsTotal.WithLabelValues(metricLabelOther, workflowmodels.WorkflowRunStatusDeadLetter)
	s0, r0, o0 := testutil.ToFloat64(succeeded), testutil.ToFloat64(retry), testutil.ToFloat64(other)

	countRunTransition(ctx, workflowmodels.WorkflowRunStatusRunning)
	countRunTransition(ctx, workflowmodels.WorkflowRunStatusPending)
	countRunTransition(ctx, workflowmodels.WorkflowRunStatusSucceeded)
	countRunTransition(ctx, workflowmodels.WorkflowRunStatusRetryWaiting)
	countRunTransition(context.Background(), workflowmodels.WorkflowRunStatusDeadLetter)

	if d := testutil.ToFloat64(succeeded) - s0; d != 1 {
		t.Fatalf("succeeded delta = %v", d)
	}
	if d := testutil.ToFloat64(retry) - r0; d != 1 {
		t.Fatalf("waiting_retry delta = %v", d)
	}
	if d := testutil.ToFloat64(other) - o0; d != 1 {
		t.Fatalf("other/dead_letter delta = %v", d)
	}
}

// TestAttemptStepAndMarkRunIncrementSeries drives the real choke points with the Service test double.
func TestAttemptStepAndMarkRunIncrementSeries(t *testing.T) {
	rec := &stepTestRecorder{}
	svc := stepTestService(rec, nil, nil).withDefaults()
	ctx := workflowmodels.ContextWithWorkflowTriggerType(context.Background(), workflowmodels.WorkflowTriggerTypeWebhook)
	ctx = contextWithRunLease(ctx, 50, "owner")

	runs := workflowRunsTotal.WithLabelValues("webhook", workflowmodels.WorkflowRunStatusSucceeded)
	okBefore := testutil.CollectAndCount(workflowStepDuration)
	runsBefore := testutil.ToFloat64(runs)

	step := workflowmodels.Step{Action: "custom_test_action"}
	gate := stepGate{run: true, handler: func(_ context.Context, req ActionRequest) ([]byte, error) { return req.Payload, nil }}
	res, err := svc.attemptStep(ctx, workflowmodels.FlowRecord{}, stepAttempt{name: "n", key: "k", step: step, gate: gate, payload: []byte(`{}`), startAttempt: 1})
	if err != nil || res.err != nil {
		t.Fatalf("attemptStep: %v / %v", err, res.err)
	}
	if got := testutil.CollectAndCount(workflowStepDuration); got < okBefore || got == 0 {
		t.Fatalf("no step series recorded: %d", got)
	}

	if err := svc.markRun(ctx, "t", 50, svc.runUpdate(workflowmodels.WorkflowRunStatusRunning, "k", nil, nil, 1, nil, 0)); err != nil {
		t.Fatal(err)
	}
	if err := svc.markRun(ctx, "t", 50, svc.runUpdate(workflowmodels.WorkflowRunStatusSucceeded, "", nil, nil, 1, nil, 0)); err != nil {
		t.Fatal(err)
	}
	if d := testutil.ToFloat64(runs) - runsBefore; d != 1 {
		t.Fatalf("webhook/succeeded delta = %v, want 1 (running write must not count)", d)
	}
}
