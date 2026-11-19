package routes

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

// fakeNotifyConn feeds notifications from a channel; closing fail ends the connection.
type fakeNotifyConn struct {
	notes  chan string
	fail   chan struct{}
	closed atomic.Bool
}

func (f *fakeNotifyConn) WaitForNotification(ctx context.Context) (*pgconn.Notification, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-f.fail:
		return nil, errors.New("connection lost")
	case payload := <-f.notes:
		return &pgconn.Notification{Payload: payload}, nil
	}
}

func (f *fakeNotifyConn) Close(context.Context) error {
	f.closed.Store(true)
	return nil
}

type fakeConnector struct {
	mu    sync.Mutex
	conns []*fakeNotifyConn
	made  chan *fakeNotifyConn
}

func newFakeConnector() *fakeConnector { return &fakeConnector{made: make(chan *fakeNotifyConn, 8)} }

func (f *fakeConnector) connect(context.Context) (serviceBusNotifyConn, error) {
	conn := &fakeNotifyConn{notes: make(chan string), fail: make(chan struct{})}
	f.mu.Lock()
	f.conns = append(f.conns, conn)
	f.mu.Unlock()
	f.made <- conn
	return conn, nil
}

func waitWake(t *testing.T, sub *serviceBusSubscriber, want bool) {
	t.Helper()
	select {
	case <-sub.wake:
		if !want {
			t.Fatal("unexpected wake")
		}
	case <-time.After(100 * time.Millisecond):
		if want {
			t.Fatal("expected a wake")
		}
	}
}

// One connection per DB, wakes routed by channel, coalesced without blocking, and the listener
// goroutine exits (connection closed) when the last subscriber leaves.
func TestServiceBusHubSharesOneListenerPerDB(t *testing.T) {
	hub := newServiceBusHub(t.Context())
	connector := newFakeConnector()

	subA, unsubA := hub.subscribe("db1", []string{"servicebus:global", "servicebus:user:1"}, connector.connect)
	subB, unsubB := hub.subscribe("db1", []string{"servicebus:global", "servicebus:user:2"}, connector.connect)
	conn := <-connector.made
	// The (re)connect resync wakes everyone once.
	waitWake(t, subA, true)
	waitWake(t, subB, true)

	conn.notes <- "servicebus:user:2"
	conn.notes <- "nobody:listens"
	waitWake(t, subB, true)
	waitWake(t, subA, false)

	// Nobody drains subA: further NOTIFYs coalesce into its single pending wake and never block.
	for range 5 {
		conn.notes <- "servicebus:global"
	}
	// The listener is sequential: once it takes this sentinel, every earlier dispatch is done.
	conn.notes <- "nobody:listens"
	waitWake(t, subA, true)
	waitWake(t, subA, false)

	unsubA()
	unsubA() // idempotent
	unsubB()
	hub.wg.Wait()
	if !conn.closed.Load() {
		t.Fatal("listener connection must be closed after the last unsubscribe")
	}
	if len(connector.conns) != 1 {
		t.Fatalf("connections opened = %d, want 1", len(connector.conns))
	}
	hub.mu.Lock()
	defer hub.mu.Unlock()
	if len(hub.dbs) != 0 {
		t.Fatal("listener entry must be removed")
	}
}

// A lost connection reconnects and wakes every subscriber so NOTIFYs missed meanwhile are re-polled.
func TestServiceBusHubReconnectsAndResyncs(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	hub := newServiceBusHub(ctx)
	connector := newFakeConnector()
	sub, unsub := hub.subscribe("db1", []string{"servicebus:global"}, connector.connect)
	defer unsub()

	first := <-connector.made
	waitWake(t, sub, true)
	close(first.fail)
	select {
	case <-connector.made:
	case <-time.After(2 * time.Second):
		t.Fatal("listener did not reconnect")
	}
	waitWake(t, sub, true)

	// Shutdown (base ctx cancel) stops the listener even with subscribers left.
	cancel()
	hub.wg.Wait()
}
