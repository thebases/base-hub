package engine

import (
	"context"
	"sync"
	"testing"
	"time"

	"git.thebase.vn/base_hub/internal/config"
	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/modules/base-workflow/models"
)

// dbContext binds an environment naming dbName, the way an import or RPC context does.
func dbContext(dbName string) context.Context {
	return orm.ContextWithEnvironment(context.Background(), orm.NewEnvironmentWithDBName(nil, dbName, 0, nil, true))
}

func mqttTrigger(id int64, topic string) models.WorkflowTriggerRecord {
	workflowID := int64(1)
	trigger := models.WorkflowTriggerRecord{WorkflowID: &workflowID, TriggerType: "mqtt", Topic: topic, IsActive: true}
	trigger.ID = id
	return trigger
}

// TestTriggerRegistryMQTTFanOutAcrossDatabases: the same trigger id on one topic in two tenant
// databases shares a single broker subscription, which is dropped only with the last trigger.
func TestTriggerRegistryMQTTFanOutAcrossDatabases(t *testing.T) {
	previousEnable := config.Get().MqttClient.Enable
	previousSubscribe := triggerRegistrySubscribe
	config.Update(func(c *config.AppConfig) { c.MqttClient.Enable = true })
	var mu sync.Mutex
	subscribes, unsubscribes := 0, 0
	triggerRegistrySubscribe = func(string, byte, func([]byte)) (func() error, error) {
		mu.Lock()
		defer mu.Unlock()
		subscribes++
		return func() error {
			mu.Lock()
			defer mu.Unlock()
			unsubscribes++
			return nil
		}, nil
	}
	t.Cleanup(func() {
		config.Update(func(c *config.AppConfig) { c.MqttClient.Enable = previousEnable })
		triggerRegistrySubscribe = previousSubscribe
	})

	registry := &workflowTriggerRegistry{
		registered: map[string]registeredTrigger{},
		kafkaTopic: map[string]string{},
		mqttTopics: map[string]*mqttTopicSubscription{},
	}
	if err := registry.Register(dbContext("tenant_a"), mqttTrigger(7, "base/evt")); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(dbContext("tenant_b"), mqttTrigger(7, "base/evt")); err != nil {
		t.Fatal(err)
	}
	// Re-syncing an unchanged row must not churn the subscription.
	if err := registry.Register(dbContext("tenant_a"), mqttTrigger(7, "base/evt")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		registry.mu.Lock()
		defer registry.mu.Unlock()
		return registry.mqttTopics["base/evt"] != nil && registry.mqttTopics["base/evt"].unsubscribe != nil
	})
	if len(registry.registered) != 2 || len(registry.mqttTopics["base/evt"].keys) != 2 {
		t.Fatalf("registered=%d keys=%d, want 2 tenants on one topic", len(registry.registered), len(registry.mqttTopics["base/evt"].keys))
	}

	if err := registry.Unregister(dbContext("tenant_a"), 7); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	if subscribes != 1 || unsubscribes != 0 {
		t.Fatalf("after first unregister: subscribes=%d unsubscribes=%d, want 1/0", subscribes, unsubscribes)
	}
	mu.Unlock()
	// A deactivated row unregisters too, and the last one drops the broker subscription.
	inactive := mqttTrigger(7, "base/evt")
	inactive.IsActive = false
	if err := registry.Register(dbContext("tenant_b"), inactive); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if unsubscribes != 1 || len(registry.mqttTopics) != 0 || len(registry.registered) != 0 {
		t.Fatalf("after last unregister: unsubscribes=%d topics=%d registered=%d", unsubscribes, len(registry.mqttTopics), len(registry.registered))
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not reached")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestDecodeRecordCollapsesMany2onePairs: the runtime read renders many2one as [2]any{id, name}; trigger
// and flow rows with a company_id must still decode (this used to abort the whole DB's sync).
func TestDecodeRecordCollapsesMany2onePairs(t *testing.T) {
	row := orm.RecordMap{
		"id": int64(1), "workflow_id": [2]any{int64(4), "EDC MQTT base/evt"}, "company_id": []any{int64(1), "Main"},
		"trigger_type": "mqtt", "topic": "base/evt", "is_active": true,
	}
	trigger, err := decodeRecord[models.WorkflowTriggerRecord](row)
	if err != nil {
		t.Fatal(err)
	}
	if trigger.WorkflowID == nil || *trigger.WorkflowID != 4 || trigger.CompanyID == nil || *trigger.CompanyID != 1 {
		t.Fatalf("decoded workflow_id=%v company_id=%v", trigger.WorkflowID, trigger.CompanyID)
	}
	flow, err := decodeRecord[models.FlowRecord](orm.RecordMap{"id": int64(4), "company_id": [2]any{int64(1), "Main"}, "is_active": true})
	if err != nil || flow.ID != 4 {
		t.Fatalf("flow decode = %+v, %v", flow, err)
	}
}

// TestMQTTHandlerDoesNotRunWorkflowInline: the broker callback only enqueues, so a slow workflow
// cannot hold the paho router (and every later QoS 1 ack) hostage, and frames keep their order.
func TestMQTTHandlerDoesNotRunWorkflowInline(t *testing.T) {
	previousEnable := config.Get().MqttClient.Enable
	previousSubscribe := triggerRegistrySubscribe
	previousContext := triggerDBContext
	config.Update(func(c *config.AppConfig) { c.MqttClient.Enable = true })
	var handler func([]byte)
	ready := make(chan struct{})
	triggerRegistrySubscribe = func(_ string, _ byte, h func([]byte)) (func() error, error) {
		handler = h
		close(ready)
		return func() error { return nil }, nil
	}
	release := make(chan struct{})
	started := make(chan struct{}, 4)
	triggerDBContext = func(context.Context, string) (context.Context, error) {
		started <- struct{}{}
		<-release
		return nil, context.Canceled
	}
	t.Cleanup(func() {
		config.Update(func(c *config.AppConfig) { c.MqttClient.Enable = previousEnable })
		triggerRegistrySubscribe = previousSubscribe
		triggerDBContext = previousContext
	})

	registry := &workflowTriggerRegistry{
		registered: map[string]registeredTrigger{},
		kafkaTopic: map[string]string{},
		mqttTopics: map[string]*mqttTopicSubscription{},
	}
	if err := registry.Register(dbContext("tenant_a"), mqttTrigger(1, "base/txn")); err != nil {
		t.Fatal(err)
	}
	<-ready
	done := make(chan struct{})
	go func() {
		handler([]byte(`{"a":1}`))
		handler([]byte(`{"a":2}`))
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handler blocked on workflow execution")
	}
	<-started
	close(release)
	registry.Shutdown()
}

// TestMQTTHandlerShedsWhenQueueStaysFull: a stuck worker must not hold the shared paho router
// forever; after mqttEnqueueWait the frame is dropped, counted, and visible in the topic stats.
func TestMQTTHandlerShedsWhenQueueStaysFull(t *testing.T) {
	previousEnable := config.Get().MqttClient.Enable
	previousSubscribe := triggerRegistrySubscribe
	previousContext := triggerDBContext
	config.Update(func(c *config.AppConfig) { c.MqttClient.Enable = true })
	var handler func([]byte)
	ready := make(chan struct{})
	triggerRegistrySubscribe = func(_ string, _ byte, h func([]byte)) (func() error, error) {
		handler = h
		close(ready)
		return func() error { return nil }, nil
	}
	release := make(chan struct{})
	started := make(chan struct{}, 1)
	triggerDBContext = func(context.Context, string) (context.Context, error) {
		select {
		case started <- struct{}{}:
		default:
		}
		<-release
		return nil, context.Canceled
	}
	t.Cleanup(func() {
		config.Update(func(c *config.AppConfig) { c.MqttClient.Enable = previousEnable })
		triggerRegistrySubscribe = previousSubscribe
		triggerDBContext = previousContext
	})
	registry := &workflowTriggerRegistry{
		registered: map[string]registeredTrigger{},
		kafkaTopic: map[string]string{},
		mqttTopics: map[string]*mqttTopicSubscription{},
	}
	if err := registry.Register(dbContext("tenant_a"), mqttTrigger(1, "base/txn")); err != nil {
		t.Fatal(err)
	}
	<-ready
	handler([]byte("0"))
	<-started // the worker now holds frame 0 and is stuck
	for i := 0; i < mqttInboundQueueSize; i++ {
		handler([]byte("x"))
	}
	begin := time.Now()
	handler([]byte("overflow"))
	if took := time.Since(begin); took < mqttEnqueueWait/2 || took > 5*time.Second {
		t.Fatalf("overflow frame waited %s, want about %s", took, mqttEnqueueWait)
	}
	stats := registry.mqttStats()["base/txn"]
	if stats.Dropped != 1 || stats.QueueDepth != mqttInboundQueueSize || stats.Enqueued != int64(mqttInboundQueueSize)+1 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
	close(release)
	registry.Shutdown()
}
