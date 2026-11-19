package mqtt

import (
	"errors"
	"testing"

	"git.thebase.vn/base_hub/internal/config"
)

// A SUBACK of 0x80 must surface as an error; granted QoS levels must not.
func TestSubackRefusal(t *testing.T) {
	if err := subackRefusal(map[string]byte{"base/evt": 1, "base/txn": 1}); err != nil {
		t.Fatalf("granted subscriptions reported as refused: %v", err)
	}
	if err := subackRefusal(map[string]byte{"base/evt": 0x80}); err == nil {
		t.Fatal("0x80 SUBACK was not reported as an error")
	}
	if err := subackRefusal(map[string]byte{"base/evt": 0}); err == nil {
		t.Fatal("QoS 0 downgrade was not reported as an error")
	}
	if err := subackRefusal(map[string]byte{"base/evt": 2}); err != nil {
		t.Fatalf("QoS 2 grant reported as refused: %v", err)
	}
	if err := subackRefusal(nil); err != nil {
		t.Fatalf("empty result reported as refused: %v", err)
	}
}

// A broken TLS config must come back as an error and leave no client behind, not exit the process.
func TestInitMQTTInvalidTLSReturnsError(t *testing.T) {
	prev := *config.Get()
	prevClient := MqttClient
	t.Cleanup(func() { config.Set(prev); MqttClient = prevClient })
	MqttClient = nil
	cfg := prev
	cfg.MqttClient.UseSSL = true
	cfg.MqttClient.TLSCAFile = "/nonexistent/ca.pem"
	config.Set(cfg)
	if err := InitMQTT(); err == nil {
		t.Fatal("expected error for unreadable CA file")
	}
	if MqttClient != nil {
		t.Fatal("failed init must not publish a client")
	}
}

// One refused topic among many must not force a re-login; session failures and mostly-refused sets must.
func TestSyncResultNeedsRelogin(t *testing.T) {
	cases := []struct {
		name string
		res  syncResult
		want bool
	}{
		{"all ok", syncResult{total: 50, ok: 50}, false},
		{"one refused of 50", syncResult{total: 50, ok: 49, refused: 1}, false},
		{"half refused", syncResult{total: 4, ok: 2, refused: 2}, false},
		{"most refused", syncResult{total: 4, ok: 1, refused: 3}, true},
		{"single topic refused", syncResult{total: 1, refused: 1}, true},
		{"one timeout", syncResult{total: 50, ok: 49, sessionFailed: 1}, true},
		{"empty", syncResult{}, false},
	}
	for _, tc := range cases {
		if got := tc.res.needsRelogin(); got != tc.want {
			t.Errorf("%s: needsRelogin=%v want %v", tc.name, got, tc.want)
		}
	}
}

// A SUBACK refusal is classified as refused (not a session failure), and Snapshot reports topic stats.
func TestRefusalClassificationAndSnapshot(t *testing.T) {
	if err := subackRefusal(map[string]byte{"base/evt": 0x80}); !errors.Is(err, errSubscribeRefused) {
		t.Fatalf("0x80 not classified as refusal: %v", err)
	}
	t.Cleanup(func() { dropTopicMetric("test/topic") })
	noteInbound("test/topic", 10)
	noteTopicState("test/topic", nil)
	RegisterTopicStatsProvider(func() map[string]TopicRuntimeStats {
		return map[string]TopicRuntimeStats{"test/topic": {QueueDepth: 3, QueueCap: 1024}}
	})
	t.Cleanup(func() { RegisterTopicStatsProvider(nil) })
	snap := Snapshot()
	for _, row := range snap["topics"].([]map[string]any) {
		if row["topic"] != "test/topic" {
			continue
		}
		if row["received"].(int64) != 1 || row["subscribed"] != true {
			t.Fatalf("unexpected topic row: %v", row)
		}
		if rt := row["runtime"].(TopicRuntimeStats); rt.QueueDepth != 3 {
			t.Fatalf("runtime stats not merged: %v", rt)
		}
		return
	}
	t.Fatal("test/topic missing from snapshot")
}
