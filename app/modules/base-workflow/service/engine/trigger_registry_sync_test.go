package engine

// Plans T55/T56/T63: reconciling Sync, cross-instance reload from trigger-change events, trigger
// dedupe_key run names, and panic recovery in the MQTT and model-trigger workers.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/modules/base-workflow/models"
)

func newTestRegistry() *workflowTriggerRegistry {
	return &workflowTriggerRegistry{
		registered: map[string]registeredTrigger{},
		kafkaTopic: map[string]string{},
		mqttTopics: map[string]*mqttTopicSubscription{},
		tcpPorts:   map[int]string{},
	}
}

func webhookTrigger(id int64) models.WorkflowTriggerRecord {
	workflowID := int64(1)
	trigger := models.WorkflowTriggerRecord{WorkflowID: &workflowID, TriggerType: "webhook", Topic: "t", IsActive: true}
	trigger.ID = id
	return trigger
}

func triggerRow(id int64) orm.RecordMap {
	return orm.RecordMap{"id": id, "workflow_id": int64(1), "trigger_type": "webhook", "topic": "t", "is_active": true}
}

// Sync removes registrations of its own database whose row is gone or inactive, keeps the
// others (including a row that no longer decodes), and leaves other databases alone.
func TestSyncReconcilesRemovedTriggers(t *testing.T) {
	previous := loadActiveTriggerRows
	t.Cleanup(func() { loadActiveTriggerRows = previous })
	registry := newTestRegistry()
	for _, id := range []int64{1, 2, 3} {
		if err := registry.Register(dbContext("tenant_a"), webhookTrigger(id)); err != nil {
			t.Fatal(err)
		}
	}
	if err := registry.Register(dbContext("tenant_b"), webhookTrigger(2)); err != nil {
		t.Fatal(err)
	}
	broken := orm.RecordMap{"id": int64(3), "workflow_id": "not-a-number"}
	loadActiveTriggerRows = func(context.Context) ([]orm.RecordMap, error) {
		return []orm.RecordMap{triggerRow(1), broken}, nil
	}
	if err := registry.Sync(dbContext("tenant_a")); err != nil {
		t.Fatal(err)
	}
	got := registry.keysForDB("tenant_a")
	if strings.Join(got, ",") != "tenant_a|1,tenant_a|3" {
		t.Fatalf("tenant_a keys after sync = %v, want 1 kept, 2 removed, undecodable 3 kept", got)
	}
	if len(registry.keysForDB("tenant_b")) != 1 {
		t.Fatal("sync of tenant_a must not touch tenant_b")
	}

	// A failed read removes nothing.
	loadActiveTriggerRows = func(context.Context) ([]orm.RecordMap, error) { return nil, errors.New("db down") }
	if err := registry.Sync(dbContext("tenant_a")); err == nil {
		t.Fatal("expected the read error")
	}
	if len(registry.keysForDB("tenant_a")) != 2 {
		t.Fatal("a failed read must not unregister anything")
	}
}

func TestSameTriggerRegistration(t *testing.T) {
	a := webhookTrigger(1)
	a.Config = map[string]any{"consumer_group": "g", "n": int64(1)}
	b := webhookTrigger(1)
	b.Config = map[string]any{"n": float64(1), "consumer_group": "g"}
	if !sameTriggerRegistration(a, b) {
		t.Fatal("same config with another key order or number type must count as unchanged")
	}
	b.Config["consumer_group"] = "h"
	if sameTriggerRegistration(a, b) {
		t.Fatal("a config change must re-register")
	}
	c := webhookTrigger(1)
	c.Topic = "other"
	if sameTriggerRegistration(webhookTrigger(1), c) {
		t.Fatal("a topic change must re-register")
	}
}

// The watcher starts at the newest event, then reloads each settled event's trigger once, stops at
// the first event that may still be uncommitted, and resumes from there on the next tick.
func TestPollTriggerChangesReloadsSettledEvents(t *testing.T) {
	previousCtx, previousFetch, previousLast, previousLoad := triggerDBContext, fetchTriggerChangeEvents, lastTriggerChangeID, loadTriggerRow
	t.Cleanup(func() {
		triggerDBContext, fetchTriggerChangeEvents, lastTriggerChangeID, loadTriggerRow = previousCtx, previousFetch, previousLast, previousLoad
	})
	triggerDBContext = func(_ context.Context, dbName string) (context.Context, error) { return dbContext(dbName), nil }
	lastTriggerChangeID = func(context.Context, string) (int64, error) { return 40, nil }
	now := time.Unix(1700000000, 0)
	var fetchedAfter []int64
	fetchTriggerChangeEvents = func(_ context.Context, _ string, afterID int64, _ time.Time) ([]triggerChangeEvent, error) {
		fetchedAfter = append(fetchedAfter, afterID)
		return []triggerChangeEvent{
			{ID: 41, TriggerID: 7, CreatedAt: now.Add(-time.Minute)},
			{ID: 42, TriggerID: 7, CreatedAt: now.Add(-time.Minute)},
			{ID: 43, TriggerID: 8, CreatedAt: now.Add(-time.Minute)},
			{ID: 44, TriggerID: 9, CreatedAt: now}, // not settled yet
		}, nil
	}
	var loaded []int64
	loadTriggerRow = func(_ context.Context, id int64) (*models.WorkflowTriggerRecord, bool, error) {
		loaded = append(loaded, id)
		if id == 8 {
			return nil, false, nil // deleted on another instance
		}
		trigger := webhookTrigger(id)
		return &trigger, true, nil
	}

	registry := newTestRegistry()
	if err := registry.Register(dbContext("tenant_a"), webhookTrigger(8)); err != nil {
		t.Fatal(err)
	}
	if err := registry.pollTriggerChanges(context.Background(), "tenant_a", now); err != nil {
		t.Fatal(err)
	}
	if len(fetchedAfter) != 0 || len(loaded) != 0 {
		t.Fatal("the first pass only sets the cursor")
	}
	if err := registry.pollTriggerChanges(context.Background(), "tenant_a", now); err != nil {
		t.Fatal(err)
	}
	if !equalInt64s(fetchedAfter, []int64{40}) {
		t.Fatalf("fetched after %v, want [40]", fetchedAfter)
	}
	if !equalInt64s(loaded, []int64{7, 8}) {
		t.Fatalf("reloaded %v, want 7 once and 8 (9 is not settled)", loaded)
	}
	if keys := registry.keysForDB("tenant_a"); strings.Join(keys, ",") != "tenant_a|7" {
		t.Fatalf("keys = %v, want 7 registered and deleted 8 removed", keys)
	}
	if err := registry.pollTriggerChanges(context.Background(), "tenant_a", now); err != nil {
		t.Fatal(err)
	}
	if !equalInt64s(fetchedAfter, []int64{40, 43}) {
		t.Fatalf("second fetch after %v, want to resume after 43", fetchedAfter)
	}
}

func TestRegisterFromWritePublishesChange(t *testing.T) {
	previous := publishTriggerChangedEvent
	t.Cleanup(func() { publishTriggerChangedEvent = previous })
	var published []map[string]any
	publishTriggerChangedEvent = func(_ context.Context, payload map[string]any) error {
		published = append(published, payload)
		return errors.New("bus down") // logged only
	}
	registry := newTestRegistry()
	if err := registry.RegisterFromWrite(dbContext("tenant_a"), webhookTrigger(5)); err != nil {
		t.Fatal(err)
	}
	if err := registry.UnregisterFromWrite(dbContext("tenant_a"), 5); err != nil {
		t.Fatal(err)
	}
	if len(published) != 2 || published[0]["db"] != "tenant_a" || published[0]["id"] != int64(5) {
		t.Fatalf("published %v, want two {db: tenant_a, id: 5} events", published)
	}
}

func TestTriggerDedupeRunName(t *testing.T) {
	trigger := mqttTrigger(3, "dev/+/evt")
	if _, ok := triggerDedupeRunName(trigger, "db", []byte(`{"id":"x"}`)); ok {
		t.Fatal("no dedupe_key: no dedupe name")
	}
	trigger.Config = map[string]any{"dedupe_key": "$.data.msg_id"}
	name, ok := triggerDedupeRunName(trigger, "db", []byte(`{"data":{"msg_id":"abc"}}`))
	if !ok || name != "mqtt:db:dev/+/evt:abc" {
		t.Fatalf("name = %q %v", name, ok)
	}
	name, ok = triggerDedupeRunName(trigger, "db", []byte(`{"data":{"msg_id":12345678901234567890}}`))
	if !ok || name != "mqtt:db:dev/+/evt:12345678901234567890" {
		t.Fatalf("numbers must keep their exact digits, got %q", name)
	}
	for _, payload := range []string{`{"data":{}}`, `{"data":{"msg_id":""}}`, `{"data":{"msg_id":{"a":1}}}`, `not json`, `{"data":"str"}`} {
		if _, ok := triggerDedupeRunName(trigger, "db", []byte(payload)); ok {
			t.Fatalf("payload %s must fall back to the normal run name", payload)
		}
	}
	long := strings.Repeat("a", maxDedupeValueLen+1)
	name, _ = triggerDedupeRunName(trigger, "db", []byte(`{"data":{"msg_id":"`+long+`"}}`))
	if !strings.HasPrefix(name, "mqtt:db:dev/+/evt:sha256:") || len(name) > 120 {
		t.Fatalf("long values must be hashed, got %q", name)
	}
}

// A redelivered MQTT frame whose dedupe run exists is handled without running the workflow.
func TestMQTTDedupeSkipsExistingRun(t *testing.T) {
	previousCtx, previousExists := triggerDBContext, workflowRunNameExists
	t.Cleanup(func() { triggerDBContext, workflowRunNameExists = previousCtx, previousExists })
	triggerDBContext = func(_ context.Context, dbName string) (context.Context, error) { return dbContext(dbName), nil }
	var checked string
	workflowRunNameExists = func(_ context.Context, name string) (bool, error) {
		checked = name
		return true, nil
	}
	trigger := mqttTrigger(3, "evt")
	trigger.Config = map[string]any{"dedupe_key": "id"}
	if err := executeMQTTTriggerInDB(context.Background(), "db", trigger, []byte(`{"id":"m1"}`)); err != nil {
		t.Fatalf("existing run must count as handled, got %v", err)
	}
	if checked != "mqtt:db:evt:m1" {
		t.Fatalf("checked run %q", checked)
	}
}

func TestMQTTEntryRecoversPanic(t *testing.T) {
	previous := triggerDBContext
	t.Cleanup(func() { triggerDBContext = previous })
	triggerDBContext = func(context.Context, string) (context.Context, error) { panic("bad handler") }
	err := runMQTTEntry(registeredTrigger{key: "db|3", dbName: "db", trigger: mqttTrigger(3, "evt")}, []byte(`{}`))
	if err == nil || !strings.Contains(err.Error(), "panicked") {
		t.Fatalf("panic must become the trigger's error, got %v", err)
	}
}

func TestModelTriggerWorkerRecoversPanic(t *testing.T) {
	swapModelTriggerRunner(t, func(modelTriggerJob) { panic("bad job") })
	runModelTriggerJobSafely(modelTriggerJob{dbName: "db"}) // must not panic
}

func TestConfigIntParsesAnyIntegerString(t *testing.T) {
	if got := configInt(map[string]any{"port": " 10500 "}, "port", 1); got != 10500 {
		t.Fatalf("configInt = %d, want 10500", got)
	}
	if got := configInt(map[string]any{"port": "x"}, "port", 7); got != 7 {
		t.Fatalf("configInt = %d, want fallback 7", got)
	}
}

// A trigger whose workflow cannot run (never published, template, inactive) is not registered:
// a Kafka consumer would never commit the ErrFlowNotFound delivery and stall its partition. Health
// explains why, and the trigger registers once the workflow is published (SyncWorkflowFromWrite).
func TestRegisterSkipsWorkflowThatCannotRun(t *testing.T) {
	row := orm.RecordMap{"id": int64(1), "topic": "t", "is_active": true, "state": models.WorkflowStateDraft}
	prevGate, prevRead, prevIDs := triggerWorkflowBlockedReason, readTriggerFlowRows, loadWorkflowTriggerIDs
	prevLoad, prevPublish := loadTriggerRow, publishTriggerChangedEvent
	triggerWorkflowBlockedReason = workflowBlockedReason
	readTriggerFlowRows = func(context.Context, int64) ([]orm.RecordMap, error) {
		return []orm.RecordMap{cloneRecordMap(row)}, nil
	}
	trigger := webhookTrigger(71)
	loadWorkflowTriggerIDs = func(context.Context, int64) ([]int64, error) { return []int64{71}, nil }
	loadTriggerRow = func(context.Context, int64) (*models.WorkflowTriggerRecord, bool, error) {
		return &trigger, true, nil
	}
	var published []any
	publishTriggerChangedEvent = func(_ context.Context, payload map[string]any) error {
		published = append(published, payload["id"])
		return nil
	}
	t.Cleanup(func() {
		triggerWorkflowBlockedReason, readTriggerFlowRows, loadWorkflowTriggerIDs = prevGate, prevRead, prevIDs
		loadTriggerRow, publishTriggerChangedEvent = prevLoad, prevPublish
	})
	registry := &workflowTriggerRegistry{
		registered: map[string]registeredTrigger{},
		kafkaTopic: map[string]string{},
		mqttTopics: map[string]*mqttTopicSubscription{},
	}
	ctx := dbContext("gate_db")
	key := triggerKey("gate_db", 71)

	for _, tc := range []struct {
		mutate func()
		reason string
	}{
		{func() {}, triggerBlockedNotPublished},
		{func() { row["is_template"] = true }, triggerBlockedTemplate},
		{func() { row["is_template"], row["is_active"] = false, false }, triggerBlockedInactive},
	} {
		tc.mutate()
		if err := registry.Register(ctx, trigger); err != nil {
			t.Fatalf("%s: %v", tc.reason, err)
		}
		if _, ok := registry.registered[key]; ok {
			t.Fatalf("%s: trigger must not be registered", tc.reason)
		}
		if got := triggerHealth.snapshot(key); got.Registered || got.LastError != tc.reason {
			t.Fatalf("health = %#v, want unregistered with %q", got, tc.reason)
		}
	}

	// Publish: the workflow can run now; its triggers are reloaded and announced to other instances.
	row["is_active"], row["state"], row["published_snapshot"] = true, models.WorkflowStatePublished, map[string]any{"version": 1}
	registry.SyncWorkflowFromWrite(ctx, 1)
	if _, ok := registry.registered[key]; !ok {
		t.Fatal("trigger must register once the workflow is published")
	}
	if got := triggerHealth.snapshot(key); !got.Registered || got.LastError != "" {
		t.Fatalf("health after publish = %#v, want registered without error", got)
	}
	if len(published) != 1 || published[0] != int64(71) {
		t.Fatalf("published events = %v, want trigger 71", published)
	}
}
