package kafkaconsumer

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/segmentio/kafka-go"
)

// fakeReader replays msgs, then cancels the loop's context and returns its error.
type fakeReader struct {
	mu        sync.Mutex
	msgs      []kafka.Message
	committed []int64
	cancel    context.CancelFunc
}

func (f *fakeReader) FetchMessage(ctx context.Context) (kafka.Message, error) {
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

func (f *fakeReader) CommitMessages(_ context.Context, msgs ...kafka.Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, m := range msgs {
		f.committed = append(f.committed, m.Offset)
	}
	return nil
}

func runLoop(t *testing.T, msgs []kafka.Message, h Handler, opts ConsumerOptions) *fakeReader {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := &fakeReader{msgs: msgs, cancel: cancel}
	var d *inMemoryDeduper
	if opts.DedupeTTL > 0 {
		d = newInMemoryDeduper(opts.DedupeTTL, 100)
	}
	consumeLoop(ctx, r, "t", h, opts, d)
	return r
}

func TestFailedMessageIsNotDedupeMarkedSoRedeliveryRuns(t *testing.T) {
	calls := 0
	h := func(context.Context, kafka.Message) error {
		calls++
		if calls == 1 {
			return errors.New("boom")
		}
		return nil
	}
	same := func(off int64) kafka.Message {
		return kafka.Message{Topic: "t", Key: []byte("k"), Value: []byte("v"), Offset: off}
	}
	runLoop(t, []kafka.Message{same(1), same(2), same(3)}, h, ConsumerOptions{DedupeTTL: time.Minute})
	// 1 fails (not marked), 2 succeeds (marked), 3 is a duplicate of 2.
	if calls != 2 {
		t.Fatalf("expected handler to run for the failed message's redelivery, calls=%d", calls)
	}
}

func TestCommitHappensAfterHandlerNotOnRead(t *testing.T) {
	var sawCommitDuringHandler bool
	var r *fakeReader
	h := func(context.Context, kafka.Message) error {
		r.mu.Lock()
		sawCommitDuringHandler = len(r.committed) > 0
		r.mu.Unlock()
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r = &fakeReader{msgs: []kafka.Message{{Offset: 7}}, cancel: cancel}
	consumeLoop(ctx, r, "t", h, ConsumerOptions{}, nil)
	if sawCommitDuringHandler || len(r.committed) != 1 || r.committed[0] != 7 {
		t.Fatalf("offset must be committed once, after the handler: during=%v committed=%v", sawCommitDuringHandler, r.committed)
	}
}

func TestHandlerRetriesAndPanicRecovery(t *testing.T) {
	calls := 0
	h := func(context.Context, kafka.Message) error {
		calls++
		if calls == 1 {
			panic("kaboom")
		}
		if calls == 2 {
			return errors.New("transient")
		}
		return nil
	}
	r := runLoop(t, []kafka.Message{{Offset: 1}}, h, ConsumerOptions{HandlerMaxAttempts: 3, MinBackoff: time.Millisecond, MaxBackoff: time.Millisecond})
	if calls != 3 || len(r.committed) != 1 {
		t.Fatalf("expected 3 attempts then commit, calls=%d committed=%v", calls, r.committed)
	}
}

func TestShutdownDuringHandlerLeavesOffsetUncommitted(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	r := &fakeReader{msgs: []kafka.Message{{Offset: 1}}, cancel: cancel}
	h := func(ctx context.Context, _ kafka.Message) error {
		cancel()
		return ctx.Err()
	}
	consumeLoop(ctx, r, "t", h, ConsumerOptions{}, nil)
	if len(r.committed) != 0 {
		t.Fatalf("offset committed despite shutdown mid-handler: %v", r.committed)
	}
}

func TestDeduperEvictsOldestAndHonoursRemark(t *testing.T) {
	d := newInMemoryDeduper(time.Hour, 10)
	msg := func(i int) kafka.Message { return kafka.Message{Key: []byte{byte(i)}, Value: []byte("v")} }
	for i := 0; i < 11; i++ {
		d.Mark("t", msg(i))
	}
	if d.Seen("t", msg(0)) {
		t.Fatal("oldest key should have been evicted past the cap")
	}
	if !d.Seen("t", msg(10)) {
		t.Fatal("newest key must survive eviction")
	}

	// A re-marked key keeps its newest timestamp even after its old queue entry pops.
	d2 := newInMemoryDeduper(50*time.Millisecond, 100)
	d2.Mark("t", msg(1))
	time.Sleep(30 * time.Millisecond)
	d2.Mark("t", msg(1))
	time.Sleep(30 * time.Millisecond)
	d2.Mark("t", msg(2)) // triggers purge of the stale first mark
	if !d2.Seen("t", msg(1)) {
		t.Fatal("re-marked key must stay seen after its superseded entry expires")
	}
}
