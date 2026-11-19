package engine

// File overview:
// - Responsibility: keeps every instance's trigger registry aligned with the trigger rows (plan
//   T55). A trigger write or unlink registers it on the instance that served the request and
//   publishes `workflow.trigger.changed {db, id}` on the service bus (`workflow:trigger`, a durable
//   row in that database's base_service_bus). Every instance follows that channel per watched
//   database and reloads the named row; a periodic full reconcile (Sync) catches anything a missed
//   or out-of-order event left behind.
// - Why polling: base-bus has no server-side subscriber API (its pg_notify / MQTT legs only wake
//   browser streams), so each instance reads the channel with busqueries.PollServiceBus, one
//   indexed query per watched database per tick.
// - Key symbols: `RegisterFromWrite`, `UnregisterFromWrite`, `StartTriggerWatcher`.

import (
	"context"
	"fmt"
	"runtime/debug"
	"sort"
	"sync"
	"time"

	"git.thebase.vn/base_hub/internal/modelruntime"
	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
	busmodels "git.thebase.vn/base_hub/modules/base-bus/models"
	busqueries "git.thebase.vn/base_hub/modules/base-bus/queries"
	"git.thebase.vn/base_hub/modules/base-workflow/models"
)

const (
	// WorkflowTriggerChangedEventType is published on workflowTriggerChannel when a trigger row
	// is written or unlinked; payload {"db": <logical db>, "id": <trigger id>}.
	WorkflowTriggerChangedEventType = "workflow.trigger.changed"
	// workflowTriggerChannel is server-side only: no channel authorizer is registered for it, so
	// browser clients are denied it (base-bus serviceBusChannelAllowed).
	workflowTriggerChannel = "workflow:trigger"
)

var (
	// triggerWatchInterval is how often each instance reads new trigger-change events.
	triggerWatchInterval = 5 * time.Second
	// triggerReconcileInterval is how often each instance runs a full Sync of every database.
	triggerReconcileInterval = 2 * time.Minute
	// triggerEventSettle delays an event until the transaction that wrote it has most likely
	// committed: the event row is inserted inside the trigger write, before its commit, and a
	// reload that ran first would still read the old row.
	triggerEventSettle = 2 * time.Second
)

// triggerWatchState is the per-instance follow state of the trigger channel.
type triggerWatchState struct {
	mu      sync.Mutex
	dbs     map[string]struct{}
	cursors map[string]int64
	started map[string]time.Time
}

// watchDatabase adds dbName (a database with the workflow module) to the followed set.
func (r *workflowTriggerRegistry) watchDatabase(dbName string) {
	r.watch.mu.Lock()
	defer r.watch.mu.Unlock()
	if r.watch.dbs == nil {
		r.watch.dbs = map[string]struct{}{}
	}
	r.watch.dbs[dbName] = struct{}{}
}

func (r *workflowTriggerRegistry) watchedDatabases() []string {
	r.watch.mu.Lock()
	defer r.watch.mu.Unlock()
	names := make([]string, 0, len(r.watch.dbs))
	for name := range r.watch.dbs {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// RegisterFromWrite is the trigger write hook: register on this instance, then tell the others.
// The event is published even when the local registration fails (e.g. a port conflict here may
// not exist on another instance), and a publish failure is only logged: the periodic reconcile
// converges the other instances anyway.
func (r *workflowTriggerRegistry) RegisterFromWrite(ctx context.Context, trigger models.WorkflowTriggerRecord) error {
	err := r.Register(ctx, trigger)
	publishWorkflowTriggerChanged(ctx, trigger.ID)
	return err
}

// UnregisterFromWrite is the trigger unlink/deactivate hook, symmetric to RegisterFromWrite.
func (r *workflowTriggerRegistry) UnregisterFromWrite(ctx context.Context, triggerID int64) error {
	err := r.Unregister(ctx, triggerID)
	publishWorkflowTriggerChanged(ctx, triggerID)
	return err
}

// loadWorkflowTriggerIDs lists the ids of every trigger row of one workflow, active or not;
// swappable in tests.
var loadWorkflowTriggerIDs = func(ctx context.Context, workflowID int64) ([]int64, error) {
	rows, err := modelruntime.NewService().SearchRead(ctx, models.WorkflowTriggerModelName, orm.RecordMap{
		"workflow_id": workflowID,
		"__order":     "id asc",
	}, []string{"id"})
	if err != nil {
		return nil, err
	}
	ids := make([]int64, 0, len(rows))
	for _, row := range rows {
		if id := modelTriggerRowID(row["id"]); id > 0 {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

// SyncWorkflowFromWrite is the workflow publish/activation hook: Register gates on whether the
// workflow can run, so a change of that (publish, is_active, is_template) reloads each of its
// triggers here and publishes the usual trigger change event so every other instance reloads them
// too. Failures are logged only; the periodic reconcile converges.
func (r *workflowTriggerRegistry) SyncWorkflowFromWrite(ctx context.Context, workflowID int64) {
	ids, err := loadWorkflowTriggerIDs(ctx, workflowID)
	if err != nil {
		log.Warnf("workflow: reload triggers of workflow %d: %v (the next reconcile catches up)", workflowID, err)
		return
	}
	for _, id := range ids {
		if err := r.reloadTrigger(ctx, id); err != nil {
			log.Warnf("workflow: reload trigger %d of workflow %d: %v", id, workflowID, err)
		}
		publishWorkflowTriggerChanged(ctx, id)
	}
}

// publishTriggerChangedEvent is the bus publish; swappable in tests.
var publishTriggerChangedEvent = func(ctx context.Context, payload map[string]any) error {
	_, err := orm.ExecuteRuntimeMethod(
		// publish is Internal; the engine writes the event row like its own rows (caller's
		// database and user, sudo), in the caller's transaction when there is one.
		models.WorkflowEngineContext(ctx),
		busmodels.ServiceBusModelName,
		"publish",
		nil,
		[]any{[]string{workflowTriggerChannel}, WorkflowTriggerChangedEventType, payload},
		nil,
	)
	return err
}

func publishWorkflowTriggerChanged(ctx context.Context, triggerID int64) {
	if triggerID <= 0 {
		return
	}
	payload := map[string]any{"db": triggerDBName(ctx), "id": triggerID}
	if err := publishTriggerChangedEvent(ctx, payload); err != nil {
		log.Warnf("workflow: publish %s for trigger %d: %v (other instances catch up on their next reconcile)", WorkflowTriggerChangedEventType, triggerID, err)
	}
}

// triggerChangeEvent is one followed bus row.
type triggerChangeEvent struct {
	ID        int64
	TriggerID int64
	CreatedAt time.Time
}

// fetchTriggerChangeEvents reads the trigger channel of one database after afterID (afterID 0:
// rows created after since); swappable in tests.
var fetchTriggerChangeEvents = func(ctx context.Context, dbName string, afterID int64, since time.Time) ([]triggerChangeEvent, error) {
	registered := orm.ResolveRegisteredDatabase(dbName)
	if registered == nil || registered.Pool == nil {
		return nil, fmt.Errorf("database %q is not available", dbName)
	}
	rows, err := busqueries.PollServiceBus(ctx, registered.Pool, []string{workflowTriggerChannel}, afterID, since)
	if err != nil {
		return nil, err
	}
	events := make([]triggerChangeEvent, 0, len(rows))
	for _, row := range rows {
		// Other event types on the channel become TriggerID 0 events: they reload nothing but
		// still advance the cursor, so a page full of them (polls are LIMITed) cannot stall it.
		triggerID := int64(0)
		if row.EventType == WorkflowTriggerChangedEventType {
			triggerID = modelTriggerRowID(row.Payload["id"])
		}
		events = append(events, triggerChangeEvent{ID: row.ID, TriggerID: triggerID, CreatedAt: row.CreatedAt})
	}
	return events, nil
}

// lastTriggerChangeID is the newest bus row id of one database (the starting cursor); swappable.
var lastTriggerChangeID = func(ctx context.Context, dbName string) (int64, error) {
	registered := orm.ResolveRegisteredDatabase(dbName)
	if registered == nil || registered.Pool == nil {
		return 0, fmt.Errorf("database %q is not available", dbName)
	}
	return busqueries.LastServiceBusID(ctx, registered.Pool)
}

// loadTriggerRow reads one trigger row by id (found=false when it no longer exists); swappable.
var loadTriggerRow = func(ctx context.Context, triggerID int64) (*models.WorkflowTriggerRecord, bool, error) {
	rows, err := modelruntime.NewService().Read(ctx, models.WorkflowTriggerModelName, []string{fmt.Sprint(triggerID)}, nil)
	if err != nil {
		return nil, false, err
	}
	if len(rows) == 0 {
		return nil, false, nil
	}
	trigger, err := decodeRecord[models.WorkflowTriggerRecord](rows[0])
	if err != nil {
		return nil, false, err
	}
	return trigger, true, nil
}

// reloadTrigger makes the registry match one trigger row of ctx's database.
func (r *workflowTriggerRegistry) reloadTrigger(ctx context.Context, triggerID int64) error {
	trigger, found, err := loadTriggerRow(ctx, triggerID)
	if err != nil {
		return err
	}
	if !found || !trigger.IsActive {
		return r.unregisterKey(triggerKey(triggerDBName(ctx), triggerID))
	}
	return r.Register(ctx, *trigger)
}

// pollTriggerChanges applies the settled trigger-change events of one database, in order, and
// advances that database's cursor past them. An event younger than triggerEventSettle stops the
// pass (it is read again next tick).
func (r *workflowTriggerRegistry) pollTriggerChanges(parent context.Context, dbName string, now time.Time) error {
	r.watch.mu.Lock()
	if r.watch.cursors == nil {
		r.watch.cursors = map[string]int64{}
		r.watch.started = map[string]time.Time{}
	}
	cursor, known := r.watch.cursors[dbName]
	since := r.watch.started[dbName]
	r.watch.mu.Unlock()

	ctx, err := triggerDBContext(parent, dbName)
	if err != nil {
		return err
	}
	if !known {
		// First pass for this database: the registry was just synced from the rows, so earlier
		// events are already reflected; follow from the newest one.
		last, err := lastTriggerChangeID(ctx, dbName)
		if err != nil {
			return err
		}
		r.watch.mu.Lock()
		r.watch.cursors[dbName] = last
		r.watch.started[dbName] = now
		r.watch.mu.Unlock()
		return nil
	}
	events, err := fetchTriggerChangeEvents(ctx, dbName, cursor, since)
	if err != nil {
		return err
	}
	reloaded := map[int64]struct{}{}
	for _, event := range events {
		if now.Sub(event.CreatedAt) < triggerEventSettle {
			break
		}
		cursor = event.ID
		if _, done := reloaded[event.TriggerID]; done || event.TriggerID <= 0 {
			continue
		}
		reloaded[event.TriggerID] = struct{}{}
		if err := r.reloadTrigger(ctx, event.TriggerID); err != nil {
			log.Errorf("workflow: reload trigger %d in %s: %v", event.TriggerID, dbName, err)
		}
	}
	r.watch.mu.Lock()
	r.watch.cursors[dbName] = cursor
	r.watch.mu.Unlock()
	return nil
}

// StartTriggerWatcher follows trigger changes made through other instances until the returned
// stop function is called (it waits for the loop to exit).
func (r *workflowTriggerRegistry) StartTriggerWatcher() func() {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		r.runTriggerWatcher(ctx)
	}()
	return func() {
		cancel()
		<-done
	}
}

func (r *workflowTriggerRegistry) runTriggerWatcher(ctx context.Context) {
	ticker := time.NewTicker(triggerWatchInterval)
	defer ticker.Stop()
	lastReconcile := time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			r.triggerWatchTick(ctx, now, now.Sub(lastReconcile) >= triggerReconcileInterval)
			if now.Sub(lastReconcile) >= triggerReconcileInterval {
				lastReconcile = now
			}
		}
	}
}

// triggerWatchTick runs one watcher pass; a panic is logged and the next tick runs normally.
func (r *workflowTriggerRegistry) triggerWatchTick(ctx context.Context, now time.Time, reconcile bool) {
	defer func() {
		if recovered := recover(); recovered != nil {
			log.Errorf("workflow: trigger watcher panicked: %v\n%s", recovered, debug.Stack())
		}
	}()
	if reconcile {
		r.SyncAllDatabases(ctx)
	}
	for _, dbName := range r.watchedDatabases() {
		if ctx.Err() != nil {
			return
		}
		if err := r.pollTriggerChanges(ctx, dbName, now); err != nil {
			log.Debugf("workflow: trigger change poll skipped db=%s: %v", dbName, err)
		}
	}
}
