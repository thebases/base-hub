package engine

// Plans T95 (replay) and T100 (cancel, timeout, concurrency limit).

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"git.thebase.vn/base_hub/internal/orm"
	workflowmodels "git.thebase.vn/base_hub/modules/base-workflow/models"
)

func controlTestFlow() workflowmodels.FlowRecord {
	return workflowmodels.FlowRecord{BaseModel: orm.BaseModel{ID: 1}, Topic: "control.topic", Version: 1, IsActive: true}
}

func twoSteps() []workflowmodels.Step {
	return []workflowmodels.Step{{Key: "first", Action: "step_first"}, {Key: "second", Action: "step_second"}}
}

func statusOf(update orm.RecordMap) string {
	status, _ := update["status"].(string)
	return status
}

// --- replay guards (T95) ---

func TestReplayableRunGuards(t *testing.T) {
	base := workflowmodels.WorkflowRunRecord{BaseModel: orm.BaseModel{ID: 5}, TriggerType: workflowmodels.WorkflowTriggerTypeKafka, AuditMode: workflowmodels.WorkflowAuditPayloadFull}
	cases := []struct {
		name   string
		mutate func(*workflowmodels.WorkflowRunRecord)
		code   int // 0 = allowed
	}{
		{"dead_letter", func(r *workflowmodels.WorkflowRunRecord) { r.Status = workflowmodels.WorkflowRunStatusDeadLetter }, 0},
		{"failed", func(r *workflowmodels.WorkflowRunRecord) { r.Status = workflowmodels.WorkflowRunStatusFailed }, 0},
		{"succeeded", func(r *workflowmodels.WorkflowRunRecord) { r.Status = workflowmodels.WorkflowRunStatusSucceeded }, 0},
		{"cancelled", func(r *workflowmodels.WorkflowRunRecord) { r.Status = workflowmodels.WorkflowRunStatusCancelled }, 0},
		{"running", func(r *workflowmodels.WorkflowRunRecord) { r.Status = workflowmodels.WorkflowRunStatusRunning }, orm.ErrConflict},
		{"waiting_retry", func(r *workflowmodels.WorkflowRunRecord) { r.Status = workflowmodels.WorkflowRunStatusRetryWaiting }, orm.ErrConflict},
		{"tcp", func(r *workflowmodels.WorkflowRunRecord) {
			r.Status = workflowmodels.WorkflowRunStatusFailed
			r.TriggerType = workflowmodels.WorkflowTriggerTypeTCP
		}, orm.ErrInvalidParams},
		{"hash mode", func(r *workflowmodels.WorkflowRunRecord) {
			r.Status = workflowmodels.WorkflowRunStatusFailed
			r.AuditMode = workflowmodels.WorkflowAuditPayloadHash
		}, orm.ErrInvalidParams},
		{"none mode", func(r *workflowmodels.WorkflowRunRecord) {
			r.Status = workflowmodels.WorkflowRunStatusFailed
			r.AuditMode = workflowmodels.WorkflowAuditPayloadNone
		}, orm.ErrInvalidParams},
		{"dry run", func(r *workflowmodels.WorkflowRunRecord) {
			r.Status = workflowmodels.WorkflowRunStatusSucceeded
			r.DryRun = true
		}, orm.ErrInvalidParams},
		{"pre-T45 webhook run defaults to full", func(r *workflowmodels.WorkflowRunRecord) {
			r.Status = workflowmodels.WorkflowRunStatusFailed
			r.AuditMode = ""
			r.TriggerType = workflowmodels.WorkflowTriggerTypeWebhook
		}, 0},
	}
	for _, tc := range cases {
		run := base
		tc.mutate(&run)
		err := replayableRun(&run)
		if tc.code == 0 {
			if err != nil {
				t.Errorf("%s: unexpected refusal %v", tc.name, err)
			}
			continue
		}
		var typed *orm.Error
		if !errors.As(err, &typed) || typed.Code != tc.code {
			t.Errorf("%s: err = %v, want code %d", tc.name, err, tc.code)
		}
	}
}

func TestReplayInputFromStep(t *testing.T) {
	flow := controlTestFlow()
	steps := []workflowmodels.Step{{Key: "a", Action: "x"}, {Key: "b", Action: "x"}, {Key: "c", Action: "x"}}
	parent := func(rows ...workflowmodels.WorkflowStepRun) *workflowmodels.WorkflowRunRecord {
		return &workflowmodels.WorkflowRunRecord{InputPayload: map[string]any{"in": 1.0}, StepRuns: rows}
	}
	ok := workflowmodels.WorkflowStepRun{StepKey: "a", Status: workflowmodels.WorkflowRunStatusSucceeded, OutputSnapshot: map[string]any{"from_a": true}}

	if input, index, err := replayInput(parent(), &flow, steps, ""); err != nil || index != 0 || !strings.Contains(string(input), `"in"`) {
		t.Fatalf("whole replay = %s, %d, %v", input, index, err)
	}
	if input, index, err := replayInput(parent(ok), &flow, steps, "b"); err != nil || index != 1 || !strings.Contains(string(input), "from_a") {
		t.Fatalf("from b = %s, %d, %v; want a's output at index 1", input, index, err)
	}
	if _, index, err := replayInput(parent(), &flow, steps, "a"); err != nil || index != 0 {
		t.Fatalf("from first step = %d, %v; want the run input", index, err)
	}

	refused := map[string]struct {
		run  *workflowmodels.WorkflowRunRecord
		flow workflowmodels.FlowRecord
		step string
	}{
		"unknown step":     {parent(ok), flow, "nope"},
		"no previous row":  {parent(), flow, "b"},
		"previous failed":  {parent(workflowmodels.WorkflowStepRun{StepKey: "a", Status: workflowmodels.WorkflowRunStatusFailed, OutputSnapshot: map[string]any{"x": 1}}), flow, "b"},
		"truncated":        {parent(workflowmodels.WorkflowStepRun{StepKey: "a", Status: workflowmodels.WorkflowRunStatusSucceeded, OutputSnapshot: capStepSnapshot(map[string]any{"big": strings.Repeat("x", maxStepSnapshotBytes)})}), flow, "b"},
		"redacted output":  {parent(workflowmodels.WorkflowStepRun{StepKey: "a", Status: workflowmodels.WorkflowRunStatusSucceeded, OutputSnapshot: map[string]any{"nested": map[string]any{"token": auditRedactedValue}}}), flow, "b"},
		"dag workflow":     {parent(ok), workflowmodels.FlowRecord{ExecutionMode: workflowmodels.WorkflowExecutionModeDAG}, "b"},
		"redacted input":   {&workflowmodels.WorkflowRunRecord{InputPayload: map[string]any{"password": auditRedactedValue}}, flow, ""},
		"hash-mode marker": {&workflowmodels.WorkflowRunRecord{InputPayload: map[string]any{auditMarkerKey: workflowmodels.WorkflowAuditPayloadNone}}, flow, ""},
	}
	for name, tc := range refused {
		_, _, err := replayInput(tc.run, &tc.flow, steps, tc.step)
		var typed *orm.Error
		if !errors.As(err, &typed) || typed.Code != orm.ErrInvalidParams {
			t.Errorf("%s: err = %v, want InvalidParams", name, err)
		}
	}
	// A step that continued on error passed its payload on unchanged: accepted.
	continued := workflowmodels.WorkflowStepRun{StepKey: "a", Status: workflowmodels.WorkflowRunStatusFailed, ContinueOnError: true, OutputSnapshot: map[string]any{"kept": 1}}
	if _, _, err := replayInput(parent(continued), &flow, steps, "b"); err != nil {
		t.Fatalf("continue_on_error previous step refused: %v", err)
	}
}

func TestReplayRunCreatesLinkedRunFromStep(t *testing.T) {
	rec := &stepTestRecorder{}
	svc := stepTestService(rec, nil, nil)
	var created workflowmodels.WorkflowRunRecord
	svc.createRun = func(_ context.Context, record workflowmodels.WorkflowRunRecord) (*orm.InsertResult, error) {
		created = record
		return &orm.InsertResult{InsertedID: int64(51), Acknowledged: true}, nil
	}
	flow := controlTestFlow()
	svc.loadActiveFlow = func(context.Context, string) (*workflowmodels.FlowRecord, error) { return &flow, nil }
	svc.loadFlowActions = func(context.Context, int64) ([]workflowmodels.WorkflowActionRecord, error) {
		return []workflowmodels.WorkflowActionRecord{
			{StepKey: "first", ActionCode: "step_first", ActionVersion: 1, Sequence: 1},
			{StepKey: "second", ActionCode: "step_second", ActionVersion: 1, Sequence: 2},
		}, nil
	}
	svc.readRun = func(_ context.Context, id int64) (*workflowmodels.WorkflowRunRecord, error) {
		if id == 51 {
			return &workflowmodels.WorkflowRunRecord{BaseModel: orm.BaseModel{ID: 51}, Name: created.Name, Status: workflowmodels.WorkflowRunStatusSucceeded}, nil
		}
		return &workflowmodels.WorkflowRunRecord{
			BaseModel: orm.BaseModel{ID: 9}, Name: "orig", Topic: "control.topic", Status: workflowmodels.WorkflowRunStatusDeadLetter,
			TriggerType: workflowmodels.WorkflowTriggerTypeKafka, AuditMode: workflowmodels.WorkflowAuditPayloadFull,
			InputPayload: map[string]any{"in": 1.0},
			StepRuns: []workflowmodels.WorkflowStepRun{
				{StepKey: "first", Status: workflowmodels.WorkflowRunStatusSucceeded, OutputSnapshot: map[string]any{"step1": "done"}},
				{StepKey: "second", Status: workflowmodels.WorkflowRunStatusFailed, Error: "boom"},
			},
		}, nil
	}
	caller := orm.ContextWithEnvironment(context.Background(), orm.NewEnvironmentWithDBName(nil, "db", 8, nil, false))
	result, err := svc.ReplayRun(caller, 9, "second")
	if err != nil {
		t.Fatalf("ReplayRun: %v", err)
	}
	if created.ParentRunID == nil || *created.ParentRunID != 9 || !strings.HasPrefix(created.Name, "orig:replay:") || !created.CallerBound {
		t.Fatalf("created run = parent %v name %q caller_bound %v", created.ParentRunID, created.Name, created.CallerBound)
	}
	if created.InputPayload["step1"] != "done" {
		t.Fatalf("replay input = %v, want first step's stored output", created.InputPayload)
	}
	if rec.count("step_first") != 0 || rec.count("step_second") != 1 {
		t.Fatalf("calls = %v, want only the second step", rec.calls)
	}
	if result["run_id"] != int64(51) || result["parent_run_id"] != int64(9) || result["status"] != workflowmodels.WorkflowRunStatusSucceeded {
		t.Fatalf("result = %v", result)
	}
}

// --- cancel (T100) ---

// blockingHandlers make step_first wait for its ctx and report when it started.
func blockingHandlers(started chan<- struct{}) map[string]ActionHandler {
	return map[string]ActionHandler{"step_first": func(ctx context.Context, req ActionRequest) ([]byte, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	}}
}

func TestCancelInProcessStopsRunCancelled(t *testing.T) {
	rec := &stepTestRecorder{}
	started := make(chan struct{})
	svc := stepTestService(rec, nil, blockingHandlers(started))
	ctx := context.Background()
	done := make(chan error, 1)
	go func() {
		done <- svc.executeFlow(ctx, flowRuntime{flow: controlTestFlow(), steps: twoSteps()}, []byte(`{}`))
	}()
	<-started
	if !cancelLocalRun(ctx, 50) {
		t.Fatal("run 50 is not in the local cancel registry")
	}
	err := <-done
	if !errors.Is(err, ErrRunCancelled) {
		t.Fatalf("executeFlow = %v, want ErrRunCancelled", err)
	}
	if got := statusOf(rec.last()); got != workflowmodels.WorkflowRunStatusCancelled {
		t.Fatalf("final status = %q, want cancelled", got)
	}
	if rec.count("step_second") != 0 {
		t.Fatal("a step ran after the cancel")
	}
	if cancelLocalRun(ctx, 50) {
		t.Fatal("finished run is still registered")
	}
}

func TestCancelFlagSeenAtStepBoundary(t *testing.T) {
	rec := &stepTestRecorder{}
	svc := stepTestService(rec, nil, nil)
	// The flag appears once the first step ran (as if cancel_run ran on another instance).
	svc.renewLease = func(context.Context, int64, string) (bool, error) {
		if rec.count("step_first") > 0 {
			return true, errRunCancelRequested
		}
		return true, nil
	}
	err := svc.executeFlow(context.Background(), flowRuntime{flow: controlTestFlow(), steps: twoSteps()}, []byte(`{}`))
	if !errors.Is(err, ErrRunCancelled) {
		t.Fatalf("executeFlow = %v, want ErrRunCancelled", err)
	}
	last := rec.last()
	if statusOf(last) != workflowmodels.WorkflowRunStatusCancelled || last["next_retry_at"] != int64(0) {
		t.Fatalf("final write = %v, want cancelled with no retry", last)
	}
	if rec.count("step_second") != 0 {
		t.Fatal("the step after the boundary ran")
	}
}

// A run that was flagged and then reaped/claimed is closed cancelled by ResumeRun, never executed.
func TestResumeRunClosesCancelRequestedRun(t *testing.T) {
	rec := &stepTestRecorder{}
	svc := stepTestService(rec, nil, nil)
	svc.renewLease = func(context.Context, int64, string) (bool, error) { return true, errRunCancelRequested }
	svc.readRun = func(context.Context, int64) (*workflowmodels.WorkflowRunRecord, error) {
		return &workflowmodels.WorkflowRunRecord{BaseModel: orm.BaseModel{ID: 50}, Topic: "control.topic", FlowVersion: 1,
			Status: workflowmodels.WorkflowRunStatusRetryWaiting, CurrentStep: "first"}, nil
	}
	if err := svc.ResumeRun(context.Background(), 50); !errors.Is(err, ErrRunCancelled) {
		t.Fatalf("ResumeRun = %v, want ErrRunCancelled", err)
	}
	if statusOf(rec.last()) != workflowmodels.WorkflowRunStatusCancelled || len(rec.calls) != 0 {
		t.Fatalf("last = %v calls = %v, want cancelled without steps", rec.last(), rec.calls)
	}
}

// --- timeout (T100) ---

func TestMaxRunSecondsEndsRunFailedWithoutRetry(t *testing.T) {
	rec := &stepTestRecorder{}
	started := make(chan struct{})
	svc := stepTestService(rec, nil, blockingHandlers(started))
	flow := controlTestFlow()
	flow.MaxRunSeconds = 1
	start := time.Now()
	err := svc.executeFlow(context.Background(), flowRuntime{flow: flow, steps: twoSteps()}, []byte(`{}`))
	if !errors.Is(err, ErrRunTimedOut) {
		t.Fatalf("executeFlow = %v, want ErrRunTimedOut", err)
	}
	if time.Since(start) > 10*time.Second {
		t.Fatal("timeout did not stop the run")
	}
	last := rec.last()
	if statusOf(last) != workflowmodels.WorkflowRunStatusFailed || last["next_retry_at"] != int64(0) {
		t.Fatalf("final write = %v, want failed with no retry", last)
	}
	if msg, _ := last["last_error"].(string); !strings.Contains(msg, "max_run_seconds") {
		t.Fatalf("last_error = %q, want the timeout", msg)
	}
	for _, update := range rec.updates {
		if statusOf(update) == workflowmodels.WorkflowRunStatusRetryWaiting {
			t.Fatal("a timed-out run was parked for retry")
		}
	}
}

// --- concurrency limit (T100) ---

func limitedFlow() workflowmodels.FlowRecord {
	flow := controlTestFlow()
	flow.MaxConcurrentRuns = 1
	return flow
}

func rejectingService(rec *stepTestRecorder) *Service {
	svc := stepTestService(rec, nil, nil)
	svc.admit = func(_ context.Context, a runAdmission) (string, error) {
		if a.limit != 1 || a.workflowID != 1 || a.owner == "" || a.parkUntil <= 0 {
			return "", errors.New("bad admission request")
		}
		return workflowmodels.WorkflowRunRejected, nil
	}
	return svc
}

func TestConcurrencyLimitParksSudoRunWithoutAttempt(t *testing.T) {
	rec := &stepTestRecorder{}
	svc := rejectingService(rec)
	sudo := orm.ContextWithEnvironment(context.Background(), orm.NewEnvironmentWithDBName(nil, "db", 0, nil, true))
	err := svc.executeFlow(sudo, flowRuntime{flow: limitedFlow(), steps: twoSteps()}, []byte(`{"a":1}`))
	if !isRunRetryScheduled(err) || errors.Is(err, workflowmodels.ErrWorkflowRunConcurrencyLimit) {
		t.Fatalf("executeFlow = %v, want a retry-scheduled acceptance", err)
	}
	last := rec.last()
	if statusOf(last) != workflowmodels.WorkflowRunStatusRetryWaiting || last["current_step"] != "first" ||
		last["attempt_count"] != 0 || last["next_retry_at"].(int64) <= 0 || len(rec.calls) != 0 {
		t.Fatalf("park = %v calls %v; want waiting_retry at the first step, no attempt", last, rec.calls)
	}
	if _, ok := last["input_payload"]; !ok {
		t.Fatal("parked run lost its input payload")
	}
}

func TestConcurrencyLimitRefusesCallerBoundRun(t *testing.T) {
	rec := &stepTestRecorder{}
	svc := rejectingService(rec)
	caller := orm.ContextWithEnvironment(context.Background(), orm.NewEnvironmentWithDBName(nil, "db", 8, nil, false))
	caller = workflowmodels.ContextWithWorkflowRequestID(caller, "wh:db:1:u8:key1")
	err := svc.executeFlow(caller, flowRuntime{flow: limitedFlow(), steps: twoSteps()}, []byte(`{}`))
	if !errors.Is(err, workflowmodels.ErrWorkflowRunConcurrencyLimit) {
		t.Fatalf("executeFlow = %v, want ErrWorkflowRunConcurrencyLimit", err)
	}
	if typed := orm.ErrorFrom(err); typed.Status != 409 {
		t.Fatalf("classified status = %d, want 409", typed.Status)
	}
	last := rec.last()
	if statusOf(last) != workflowmodels.WorkflowRunStatusFailed || last["name"] != "wh:db:1:u8:key1:rejected:50" {
		t.Fatalf("refused run write = %v; want failed and the request id given back", last)
	}
}

func TestConcurrencyLimitFailsTCPRunFast(t *testing.T) {
	rec := &stepTestRecorder{}
	svc := rejectingService(rec)
	ctx := workflowmodels.ContextWithWorkflowOutputSink(context.Background(), &workflowmodels.WorkflowOutput{})
	err := svc.executeFlow(ctx, flowRuntime{flow: limitedFlow(), steps: twoSteps()}, []byte(`{}`))
	if !errors.Is(err, workflowmodels.ErrWorkflowRunConcurrencyLimit) || statusOf(rec.last()) != workflowmodels.WorkflowRunStatusFailed {
		t.Fatalf("tcp over limit = %v, last %v; want failed fast", err, rec.last())
	}
}

func TestConcurrencyLimitParkedResumeRunsNoStep(t *testing.T) {
	rec := &stepTestRecorder{}
	svc := stepTestService(rec, nil, nil)
	flow := limitedFlow()
	svc.admit = func(context.Context, runAdmission) (string, error) { return workflowmodels.WorkflowRunParked, nil }
	svc.loadFlowVersion = func(context.Context, string, int) (*workflowmodels.FlowRecord, error) { return &flow, nil }
	svc.loadFlowActions = func(context.Context, int64) ([]workflowmodels.WorkflowActionRecord, error) {
		return []workflowmodels.WorkflowActionRecord{{StepKey: "first", ActionCode: "step_first", ActionVersion: 1}}, nil
	}
	svc.readRun = func(context.Context, int64) (*workflowmodels.WorkflowRunRecord, error) {
		return &workflowmodels.WorkflowRunRecord{BaseModel: orm.BaseModel{ID: 50}, Topic: "control.topic", FlowVersion: 1,
			Status: workflowmodels.WorkflowRunStatusRetryWaiting, CurrentStep: "first"}, nil
	}
	if err := svc.ResumeRun(context.Background(), 50); !isRunRetryScheduled(err) {
		t.Fatalf("ResumeRun = %v, want retry scheduled", err)
	}
	if len(rec.calls) != 0 || len(rec.updates) != 0 {
		t.Fatalf("parked resume wrote %v and ran %v; admit_run already parked it", rec.updates, rec.calls)
	}
}

func TestAdmitRunSkipsUnlimitedWorkflows(t *testing.T) {
	svc := &Service{now: time.Now, admit: func(context.Context, runAdmission) (string, error) {
		t.Fatal("admit_run called for an unlimited workflow")
		return "", nil
	}}
	got, err := svc.admitRun(context.Background(), flowRuntime{flow: controlTestFlow(), runID: 1})
	if err != nil || got != workflowmodels.WorkflowRunAdmitted {
		t.Fatalf("admitRun = %q, %v", got, err)
	}
	svc.admit = func(context.Context, runAdmission) (string, error) { return workflowmodels.WorkflowRunLost, nil }
	if _, err := svc.admitRun(context.Background(), flowRuntime{flow: limitedFlow(), runID: 1}); !errors.Is(err, ErrRunLeaseLost) {
		t.Fatalf("lost admission = %v, want ErrRunLeaseLost", err)
	}
}
