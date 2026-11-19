package engine

// Plan T54: a Kafka offset is committed only once the delivery is accepted (success, scheduled
// retry, or a failure recorded on a run row); otherwise the same message is handled again.

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"git.thebase.vn/base_hub/modules/base-workflow/models"
	"github.com/segmentio/kafka-go"
)

type fakeTriggerReader struct {
	mu        sync.Mutex
	msgs      []kafka.Message
	committed []int64
	cancel    context.CancelFunc
}

func (f *fakeTriggerReader) FetchMessage(ctx context.Context) (kafka.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.msgs) == 0 {
		f.cancel()
		return kafka.Message{}, ctx.Err()
	}
	m := f.msgs[0]
	f.msgs = f.msgs[1:]
	return m, nil
}

func (f *fakeTriggerReader) CommitMessages(_ context.Context, msgs ...kafka.Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, m := range msgs {
		f.committed = append(f.committed, m.Offset)
	}
	return nil
}

func fastKafkaBackoff(t *testing.T) {
	t.Helper()
	previousMin, previousMax := kafkaHandleBackoffMin, kafkaHandleBackoffMax
	kafkaHandleBackoffMin, kafkaHandleBackoffMax = time.Millisecond, 2*time.Millisecond
	t.Cleanup(func() { kafkaHandleBackoffMin, kafkaHandleBackoffMax = previousMin, previousMax })
}

// A rejected message is handled again (not skipped) and committed only after it is accepted; the
// next message is fetched only then, so the partition never commits past an unaccepted offset.
func TestKafkaTriggerLoopRetriesRejectedMessageBeforeCommitting(t *testing.T) {
	fastKafkaBackoff(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	msg := func(off int64) kafka.Message { return kafka.Message{Topic: "t", Value: []byte("v"), Offset: off} }
	r := &fakeTriggerReader{msgs: []kafka.Message{msg(1), msg(2)}, cancel: cancel}
	var handled []int64
	handler := func(_ context.Context, m kafka.Message) error {
		handled = append(handled, m.Offset)
		if m.Offset == 1 && len(handled) < 3 {
			return errors.New("database down")
		}
		return nil
	}
	kafkaTriggerLoop(ctx, r, "t", handler, nil)
	if want := []int64{1, 1, 1, 2}; !equalInt64s(handled, want) {
		t.Fatalf("handled offsets %v, want %v (offset 1 retried until accepted)", handled, want)
	}
	if want := []int64{1, 2}; !equalInt64s(r.committed, want) {
		t.Fatalf("committed %v, want %v", r.committed, want)
	}
}

// Shutting down while a message keeps failing leaves it uncommitted (redelivered after restart).
func TestKafkaTriggerLoopDoesNotCommitOnShutdown(t *testing.T) {
	fastKafkaBackoff(t)
	ctx, cancel := context.WithCancel(context.Background())
	r := &fakeTriggerReader{msgs: []kafka.Message{{Topic: "t", Offset: 5}}, cancel: cancel}
	calls := 0
	handler := func(context.Context, kafka.Message) error {
		calls++
		if calls == 3 {
			cancel()
		}
		return errors.New("still failing")
	}
	kafkaTriggerLoop(ctx, r, "t", handler, nil)
	if len(r.committed) != 0 {
		t.Fatalf("committed %v, want nothing", r.committed)
	}
	if calls < 3 {
		t.Fatalf("handler calls = %d, want the message retried", calls)
	}
}

func TestKafkaTriggerLoopRecoversHandlerPanic(t *testing.T) {
	fastKafkaBackoff(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := &fakeTriggerReader{msgs: []kafka.Message{{Topic: "t", Offset: 9}}, cancel: cancel}
	calls := 0
	handler := func(context.Context, kafka.Message) error {
		calls++
		if calls == 1 {
			panic("boom")
		}
		return nil
	}
	kafkaTriggerLoop(ctx, r, "t", handler, nil)
	if calls != 2 || !equalInt64s(r.committed, []int64{9}) {
		t.Fatalf("calls=%d committed=%v, want the panic retried then committed", calls, r.committed)
	}
}

func TestKafkaDeliveryOutcome(t *testing.T) {
	previous := workflowRunNameExists
	t.Cleanup(func() { workflowRunNameExists = previous })
	runExists := false
	var lookupErr error
	workflowRunNameExists = func(context.Context, string) (bool, error) { return runExists, lookupErr }
	ctx := context.Background()
	failed := errors.New("step failed")

	if err := kafkaDeliveryOutcome(ctx, "n", nil); err != nil {
		t.Fatalf("success must commit, got %v", err)
	}
	if err := kafkaDeliveryOutcome(ctx, "n", ErrRunRetryScheduled); err != nil {
		t.Fatalf("scheduled retry must commit, got %v", err)
	}
	if err := kafkaDeliveryOutcome(ctx, "n", failed); !errors.Is(err, failed) {
		t.Fatalf("failure without a run row must not commit, got %v", err)
	}
	runExists = true
	if err := kafkaDeliveryOutcome(ctx, "n", failed); err != nil {
		t.Fatalf("failure recorded on a run row must commit, got %v", err)
	}
	lookupErr = errors.New("db down")
	if err := kafkaDeliveryOutcome(ctx, "n", failed); !errors.Is(err, failed) {
		t.Fatalf("an unknown run state must not commit, got %v", err)
	}
}

func TestKafkaTriggerGroupAndKernelTopics(t *testing.T) {
	trigger := models.WorkflowTriggerRecord{TriggerType: "kafka", Topic: "ipn"}
	trigger.ID = 12
	if got := kafkaTriggerGroupID("tenant_a", trigger); got != "wf-tenant_a-12" {
		t.Fatalf("default group = %q, want wf-tenant_a-12", got)
	}
	trigger.Config = map[string]any{"consumer_group": " custom "}
	if got := kafkaTriggerGroupID("tenant_a", trigger); got != "custom" {
		t.Fatalf("configured group = %q, want custom", got)
	}
	if !kafkaTopicOwnedByKernel("ipn", []string{"other", " ipn "}) {
		t.Fatal("a topic of kafka.topics must be refused")
	}
	if kafkaTopicOwnedByKernel("ipn.workflow", []string{"ipn"}) {
		t.Fatal("other topics are allowed")
	}
}

func equalInt64s(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
