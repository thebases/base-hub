package engine

// File overview:
// - Responsibility: Prometheus metrics of the workflow engine (plan T102, WF-X-13).
// - Registration: collectors are package-level and registered once on the default registry, the
//   one `internal/monitoring` serves at /metrics through promhttp.Handler(). init runs once per
//   process, so tests cannot hit a duplicate-registration panic. internal/ never imports modules.
// - Cardinality: labels are bounded enums only (action codes of the builtin catalog, trigger types,
//   run statuses). Never label by run id, topic, payload value or database name.

import (
	"context"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	workflowmodels "git.thebase.vn/base_hub/modules/base-workflow/models"
)

const (
	metricStatusSuccess = "success"
	metricStatusFailed  = "failed"
	metricStatusSkipped = "skipped"
	metricLabelOther    = "other"
)

var (
	workflowStepDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "workflow_step_duration_seconds",
		Help:    "Duration of one workflow step attempt in seconds, by action code and status (success|failed|skipped).",
		Buckets: []float64{.005, .025, .1, .25, .5, 1, 2.5, 5, 10, 30, 60, 300},
	}, []string{"action", "status"})

	workflowRunsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "workflow_runs_total",
		Help: "Workflow run transitions into a terminal status, waiting_retry or waiting_approval, by trigger type and outcome.",
	}, []string{"trigger", "outcome"})

	// workflowDueRetryRuns is a per-process last-observed value: the number of due waiting_retry
	// runs the latest resume tick claimed (capped by the tick's limit, so a value equal to the
	// limit means "at least"). It is not a cluster-wide queue depth.
	workflowDueRetryRuns = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "workflow_due_retry_runs",
		Help: "Due waiting_retry runs claimed by the last resume_due_runs tick of this process (capped by its limit).",
	})
)

func init() {
	prometheus.MustRegister(workflowStepDuration, workflowRunsTotal, workflowDueRetryRuns)
}

// knownTriggerTypes bounds the trigger label; anything else is reported as "other".
var knownTriggerTypes = map[string]bool{
	workflowmodels.WorkflowTriggerTypeWebhook:    true,
	workflowmodels.WorkflowTriggerTypeKafka:      true,
	workflowmodels.WorkflowTriggerTypeMQTT:       true,
	workflowmodels.WorkflowTriggerTypeModel:      true,
	workflowmodels.WorkflowTriggerTypeTCP:        true,
	workflowmodels.WorkflowTriggerTypeManualTest: true,
}

// metricAction bounds the action label to codes the builtin catalog resolves.
func metricAction(step workflowmodels.Step) string {
	code := strings.TrimSpace(step.Action)
	if _, ok := workflowmodels.ResolveBuiltinWorkflowAction(code, step.ResolvedActionVersion()); ok {
		return code
	}
	return metricLabelOther
}

func observeStep(step workflowmodels.Step, status string, d time.Duration) {
	workflowStepDuration.WithLabelValues(metricAction(step), status).Observe(max(d, 0).Seconds())
}

// countRunTransition counts a run write that moves the run into a final status or waiting_retry.
// Running/pending writes (checkpoints, lease renewals) are ignored, and every run reaches a final
// status through exactly one markRun, so a run is never counted twice for one outcome.
func countRunTransition(ctx context.Context, status string) {
	switch status {
	case workflowmodels.WorkflowRunStatusSucceeded, workflowmodels.WorkflowRunStatusFailed,
		workflowmodels.WorkflowRunStatusDeadLetter, workflowmodels.WorkflowRunStatusCancelled,
		workflowmodels.WorkflowRunStatusSkipped, workflowmodels.WorkflowRunStatusRetryWaiting,
		workflowmodels.WorkflowRunStatusWaitingApproval:
	default:
		return
	}
	trigger := workflowmodels.WorkflowTriggerTypeFromContext(ctx)
	if !knownTriggerTypes[trigger] {
		trigger = metricLabelOther
	}
	workflowRunsTotal.WithLabelValues(trigger, status).Inc()
}
