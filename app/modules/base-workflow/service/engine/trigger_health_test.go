package engine

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"git.thebase.vn/base_hub/internal/config"
)

func TestTriggerHealthDeliveryOutcomes(t *testing.T) {
	key := "health_a|1"
	triggerHealth.deliveryStart(key)
	triggerHealth.deliveryEnd(key, nil)
	triggerHealth.deliveryStart(key)
	triggerHealth.deliveryEnd(key, fmt.Errorf("%w: attempt 1", ErrRunRetryScheduled))
	got := triggerHealth.snapshot(key)
	if got.Events != 2 || got.Failures != 0 || got.LastError != "" || got.LastEventAt == 0 {
		t.Fatalf("success + scheduled retry: %#v, want 2 events, no failure", got)
	}
	triggerHealth.deliveryStart(key)
	triggerHealth.deliveryEnd(key, errors.New("boom"))
	got = triggerHealth.snapshot(key)
	if got.Events != 3 || got.Failures != 1 || got.LastError != "boom" || got.LastErrorAt == 0 {
		t.Fatalf("failure: %#v", got)
	}
}

// TestTriggerHealthRegisterUnregister drives the registry: registration, a delivery, a subscribe
// error and unregistration, with the same trigger id in two databases staying separate.
func TestTriggerHealthRegisterUnregister(t *testing.T) {
	prevEnable := config.Get().MqttClient.Enable
	prevSubscribe, prevRetry := triggerRegistrySubscribe, mqttSubscribeRetry
	mqttSubscribeRetry = 10 * time.Millisecond
	config.Update(func(c *config.AppConfig) { c.MqttClient.Enable = true })
	var mu sync.Mutex
	subscribeErr := errors.New("broker down")
	triggerRegistrySubscribe = func(string, byte, func([]byte)) (func() error, error) {
		mu.Lock()
		defer mu.Unlock()
		if subscribeErr != nil {
			return nil, subscribeErr
		}
		return func() error { return nil }, nil
	}
	t.Cleanup(func() {
		config.Update(func(c *config.AppConfig) { c.MqttClient.Enable = prevEnable })
		triggerRegistrySubscribe, mqttSubscribeRetry = prevSubscribe, prevRetry
	})

	registry := &workflowTriggerRegistry{
		registered: map[string]registeredTrigger{},
		kafkaTopic: map[string]string{},
		mqttTopics: map[string]*mqttTopicSubscription{},
	}
	if err := registry.Register(dbContext("health_t1"), mqttTrigger(91, "health/evt")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return triggerHealth.snapshot("health_t1|91").LastError != "" })
	got := triggerHealthForDB("health_t1", []int64{91})[91]
	// Not subscribed yet: health must not read "Listening".
	if got.Registered || got.LastError == "" {
		t.Fatalf("subscribe failing: %#v, want registered=false with last_error", got)
	}
	if other := triggerHealthForDB("health_t2", []int64{91})[91]; other.Registered || other.LastError != "" {
		t.Fatalf("other database leaked health: %#v", other)
	}

	// The retry succeeds once the broker is back; the stale subscribe error is cleared.
	mu.Lock()
	subscribeErr = nil
	mu.Unlock()
	waitFor(t, func() bool { return triggerHealth.snapshot("health_t1|91").Registered })
	if got := triggerHealth.snapshot("health_t1|91"); got.LastError != "" || got.RegisteredAt == 0 {
		t.Fatalf("after subscribe: %#v, want registered with no error", got)
	}
	// A second trigger on the already-subscribed topic is listening at once.
	if err := registry.Register(dbContext("health_t1"), mqttTrigger(93, "health/evt")); err != nil {
		t.Fatal(err)
	}
	if got := triggerHealth.snapshot("health_t1|93"); !got.Registered {
		t.Fatalf("join subscribed topic: %#v, want registered", got)
	}

	if err := registry.Unregister(dbContext("health_t1"), 91); err != nil {
		t.Fatal(err)
	}
	if got := triggerHealth.snapshot("health_t1|91"); got.Registered {
		t.Fatalf("after unregister: %#v, want registered=false", got)
	}
}

func TestTriggerHealthRegisterFailureRecorded(t *testing.T) {
	registry := &workflowTriggerRegistry{
		registered: map[string]registeredTrigger{},
		kafkaTopic: map[string]string{},
		mqttTopics: map[string]*mqttTopicSubscription{},
	}
	bad := mqttTrigger(92, "")
	if err := registry.Register(dbContext("health_t3"), bad); err == nil {
		t.Fatal("expected empty topic error")
	}
	got := triggerHealth.snapshot("health_t3|92")
	if got.Registered || got.LastError == "" {
		t.Fatalf("failed register: %#v, want registered=false with error", got)
	}
}

// MQTT disabled in config: the trigger is kept in the registry but health reports it as not
// registered with the reason, never as listening.
func TestTriggerHealthMQTTDisabledNotRegistered(t *testing.T) {
	prevEnable := config.Get().MqttClient.Enable
	config.Update(func(c *config.AppConfig) { c.MqttClient.Enable = false })
	t.Cleanup(func() { config.Update(func(c *config.AppConfig) { c.MqttClient.Enable = prevEnable }) })
	registry := &workflowTriggerRegistry{
		registered: map[string]registeredTrigger{},
		kafkaTopic: map[string]string{},
		mqttTopics: map[string]*mqttTopicSubscription{},
	}
	if err := registry.Register(dbContext("health_t4"), mqttTrigger(94, "health/off")); err != nil {
		t.Fatal(err)
	}
	got := triggerHealth.snapshot("health_t4|94")
	if got.Registered || got.LastError == "" {
		t.Fatalf("mqtt disabled: %#v, want registered=false with last_error", got)
	}
	if _, ok := registry.registered["health_t4|94"]; !ok {
		t.Fatal("trigger dropped from the registry, want kept for a later subscribe")
	}
}
