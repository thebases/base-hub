package engine

import (
	"testing"

	"github.com/segmentio/kafka-go"
)

func TestKafkaDeliveryRunNameIsDeterministicPerOffset(t *testing.T) {
	msg := kafka.Message{Topic: "t", Partition: 2, Offset: 41}
	got := kafkaDeliveryRunName("db1", "g", msg)
	if got != "kafka:db1:g:t:2:41" {
		t.Fatalf("unexpected run name %q", got)
	}
	if got != kafkaDeliveryRunName("db1", "g", msg) {
		t.Fatal("name must be stable across redeliveries")
	}
	msg.Offset = 42
	if got == kafkaDeliveryRunName("db1", "g", msg) {
		t.Fatal("different offsets must produce different run names")
	}
}
