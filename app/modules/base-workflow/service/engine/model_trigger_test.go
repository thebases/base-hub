package engine

// File overview:
// - Responsibility: plan T47, model triggers fire only for writes in their own database, run in that
//   database, stop on recursion / chain depth, and never spawn more than the bounded per-DB workers.

import (
	"context"
	"testing"
	"time"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/modules/base-workflow/models"
)

func modelTriggerTestCtx(dbName string, chain []int64) context.Context {
	ctx := orm.ContextWithEnvironment(context.Background(), orm.NewEnvironmentWithDBName(nil, dbName, 2, nil, false))
	if chain != nil {
		ctx = contextWithModelTriggerChain(ctx, chain)
	}
	return ctx
}

func captureModelTriggerJobs(t *testing.T) chan modelTriggerJob {
	t.Helper()
	jobs := make(chan modelTriggerJob, 16)
	swapModelTriggerRunner(t, func(job modelTriggerJob) { jobs <- job })
	return jobs
}

func swapModelTriggerRunner(t *testing.T, run func(modelTriggerJob)) {
	t.Helper()
	previous := modelTriggerRunner.Load()
	modelTriggerRunner.Store(&run)
	t.Cleanup(func() { modelTriggerRunner.Store(previous) })
}

func registerTestModelTrigger(t *testing.T, dbName string, id int64, model string) {
	t.Helper()
	workflowID := int64(1)
	trigger := models.WorkflowTriggerRecord{
		BaseModel:   orm.BaseModel{ID: id},
		WorkflowID:  &workflowID,
		TriggerType: "model",
		IsActive:    true,
		Config:      map[string]any{"model": model, "operation": orm.RecordEventAfterWrite},
	}
	key := triggerKey(dbName, id)
	if err := registerModelTrigger(dbName, key, trigger); err != nil {
		t.Fatalf("registerModelTrigger: %v", err)
	}
	t.Cleanup(func() { unregisterModelTrigger(model, key) })
}

func expectNoModelTriggerJob(t *testing.T, jobs chan modelTriggerJob) {
	t.Helper()
	select {
	case job := <-jobs:
		t.Fatalf("unexpected model trigger run: %+v", job)
	case <-time.After(50 * time.Millisecond):
	}
}

func receiveModelTriggerJob(t *testing.T, jobs chan modelTriggerJob) modelTriggerJob {
	t.Helper()
	select {
	case job := <-jobs:
		return job
	case <-time.After(2 * time.Second):
		t.Fatalf("model trigger did not run")
		return modelTriggerJob{}
	}
}

func TestModelTriggerFiresOnlyInItsDatabase(t *testing.T) {
	jobs := captureModelTriggerJobs(t)
	const model = "tbl.t47_isolation"
	registerTestModelTrigger(t, "t47_tenant_a", 11, model)

	dispatchModelTriggerEvent(modelTriggerTestCtx("t47_tenant_b", nil), orm.RecordEventAfterWrite, model, "5", orm.RecordMap{"id": 5})
	expectNoModelTriggerJob(t, jobs)

	dispatchModelTriggerEvent(modelTriggerTestCtx("t47_tenant_a", nil), orm.RecordEventAfterWrite, model, "5", orm.RecordMap{"id": 5})
	job := receiveModelTriggerJob(t, jobs)
	if job.dbName != "t47_tenant_a" || job.trigger.ID != 11 || len(job.chain) != 1 || job.chain[0] != 11 {
		t.Fatalf("job = db %q trigger %d chain %v, want t47_tenant_a / 11 / [11]", job.dbName, job.trigger.ID, job.chain)
	}

	// The same trigger id in another database is a separate registration.
	registerTestModelTrigger(t, "t47_tenant_b", 11, model)
	dispatchModelTriggerEvent(modelTriggerTestCtx("t47_tenant_b", nil), orm.RecordEventAfterWrite, model, "5", orm.RecordMap{"id": 5})
	if job := receiveModelTriggerJob(t, jobs); job.dbName != "t47_tenant_b" {
		t.Fatalf("job ran in %q, want t47_tenant_b", job.dbName)
	}
	expectNoModelTriggerJob(t, jobs)
}

func TestModelTriggerChainStopsRecursion(t *testing.T) {
	jobs := captureModelTriggerJobs(t)
	const model = "tbl.t47_recursion"
	registerTestModelTrigger(t, "t47_tenant_r", 21, model)
	fire := func(chain []int64) {
		dispatchModelTriggerEvent(modelTriggerTestCtx("t47_tenant_r", chain), orm.RecordEventAfterWrite, model, "1", orm.RecordMap{"id": 1})
	}

	fire([]int64{21}) // the workflow wrote the model it watches
	expectNoModelTriggerJob(t, jobs)

	fire([]int64{1, 2, 3}) // depth limit
	expectNoModelTriggerJob(t, jobs)

	fire([]int64{7})
	if job := receiveModelTriggerJob(t, jobs); len(job.chain) != 2 || job.chain[0] != 7 || job.chain[1] != 21 {
		t.Fatalf("chain = %v, want [7 21]", job.chain)
	}
}

func TestModelTriggerQueueIsBounded(t *testing.T) {
	// A pre-seeded queue with no workers: the bound is checked without running any job.
	const dbName = "t47_tenant_queue"
	modelTriggerQueues.mu.Lock()
	modelTriggerQueues.byDB[dbName] = make(chan modelTriggerJob, modelTriggerQueuePerDB)
	modelTriggerQueues.mu.Unlock()
	t.Cleanup(func() {
		modelTriggerQueues.mu.Lock()
		delete(modelTriggerQueues.byDB, dbName)
		modelTriggerQueues.mu.Unlock()
	})

	job := modelTriggerJob{dbName: dbName, trigger: models.WorkflowTriggerRecord{BaseModel: orm.BaseModel{ID: 31}}}
	for i := 0; i < modelTriggerQueuePerDB; i++ {
		if !enqueueModelTriggerRun(job) {
			t.Fatalf("job %d refused before the queue was full", i)
		}
	}
	if enqueueModelTriggerRun(job) {
		t.Fatalf("job accepted past the queue bound")
	}
}
