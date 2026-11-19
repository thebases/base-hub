package routes

// File overview:
// - Responsibility: one Postgres LISTEN connection per database, fanned out in process to every
//   /bus/ws and /bus/sse stream of that database (plan T70, BUG-M-15).
// - Key symbols: `serviceBusHub`, `busHub`, `serviceBusSubscriber`, `serviceBusPGXConnect`.
// - Why: each stream used to hold its own pooled connection in LISTEN, so streams competed with
//   /rpc for the pool. The listener opens its own connection outside the pool (pool.Close never
//   waits on it), starts with the first stream of a database and stops with the last one.
// - Fan-out policy: a subscriber's wake channel has capacity 1 and sends never block, so a slow
//   stream can never stall the listener. Extra NOTIFYs while a wake is pending are coalesced, which
//   loses nothing: the wake only means "re-poll from your cursor" and the rows are the source of
//   truth. After every (re)connect all subscribers are woken once, because NOTIFYs sent while no
//   connection listened are gone. Reconnects back off from 500 ms to 30 s.

import (
	"context"
	"sync"
	"time"

	"git.thebase.vn/base_hub/internal/services/log"
	busmodels "git.thebase.vn/base_hub/modules/base-bus/models"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const (
	serviceBusListenRetryMin = 500 * time.Millisecond
	serviceBusListenRetryMax = 30 * time.Second
	serviceBusListenCloseMax = 5 * time.Second
)

// serviceBusNotifyConn is the part of *pgx.Conn the listener uses; tests swap in a fake.
type serviceBusNotifyConn interface {
	WaitForNotification(ctx context.Context) (*pgconn.Notification, error)
	Close(ctx context.Context) error
}

// serviceBusConnectFunc opens a connection that is already LISTENing.
type serviceBusConnectFunc func(ctx context.Context) (serviceBusNotifyConn, error)

// serviceBusSubscriber is one stream's registration. wake has capacity 1.
type serviceBusSubscriber struct {
	channels map[string]struct{}
	wake     chan struct{}
}

// serviceBusListener is the per-database listener state; subs is guarded by serviceBusHub.mu.
type serviceBusListener struct {
	connect serviceBusConnectFunc
	cancel  context.CancelFunc
	subs    map[*serviceBusSubscriber]struct{}
}

type serviceBusHub struct {
	mu      sync.Mutex
	dbs     map[string]*serviceBusListener
	baseCtx context.Context
	wg      sync.WaitGroup
}

// busHub listeners stop with Shutdown (shutdownCtx), as streams do.
var busHub = newServiceBusHub(shutdownCtx)

func newServiceBusHub(baseCtx context.Context) *serviceBusHub {
	return &serviceBusHub{dbs: map[string]*serviceBusListener{}, baseCtx: baseCtx}
}

// subscribe registers a stream for channels on dbName, starting the database's listener with
// connect if none runs. The returned func unsubscribes; it is idempotent.
func (h *serviceBusHub) subscribe(dbName string, channels []string, connect serviceBusConnectFunc) (*serviceBusSubscriber, func()) {
	sub := &serviceBusSubscriber{channels: make(map[string]struct{}, len(channels)), wake: make(chan struct{}, 1)}
	for _, channel := range channels {
		sub.channels[channel] = struct{}{}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	listener := h.dbs[dbName]
	if listener == nil {
		ctx, cancel := context.WithCancel(h.baseCtx)
		listener = &serviceBusListener{connect: connect, cancel: cancel, subs: map[*serviceBusSubscriber]struct{}{}}
		h.dbs[dbName] = listener
		h.wg.Add(1)
		go h.run(ctx, dbName, listener)
	}
	listener.subs[sub] = struct{}{}
	return sub, func() { h.unsubscribe(dbName, listener, sub) }
}

// unsubscribe drops sub and stops the listener when it was the last subscriber. A listener that
// was already replaced is only cancelled by its own last subscriber, never the new one.
func (h *serviceBusHub) unsubscribe(dbName string, listener *serviceBusListener, sub *serviceBusSubscriber) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(listener.subs, sub)
	if len(listener.subs) > 0 {
		return
	}
	listener.cancel()
	if h.dbs[dbName] == listener {
		delete(h.dbs, dbName)
	}
}

// run keeps one LISTEN connection open until ctx ends, reconnecting with capped backoff. The
// backoff resets after a connection that worked, so a brief blip retries fast.
func (h *serviceBusHub) run(ctx context.Context, dbName string, listener *serviceBusListener) {
	defer h.wg.Done()
	delay := serviceBusListenRetryMin
	for {
		connected, err := h.listen(ctx, listener)
		if ctx.Err() != nil {
			return
		}
		if connected {
			delay = serviceBusListenRetryMin
		}
		log.Warnf("bus listener %q: %v; reconnecting in %s", dbName, err, delay)
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		delay = min(delay*2, serviceBusListenRetryMax)
	}
}

// listen serves one connection until it fails or ctx ends; connected reports it got that far.
func (h *serviceBusHub) listen(ctx context.Context, listener *serviceBusListener) (connected bool, err error) {
	conn, err := listener.connect(ctx)
	if err != nil {
		return false, err
	}
	defer closeServiceBusNotifyConn(conn)
	// NOTIFYs sent while nothing listened are lost: make every stream re-poll once.
	h.dispatch(listener, "")
	for {
		notification, err := conn.WaitForNotification(ctx)
		if err != nil {
			return true, err
		}
		h.dispatch(listener, notification.Payload)
	}
}

// closeServiceBusNotifyConn closes with its own short deadline: the listener's ctx is usually
// already cancelled here, and this is a raw connection, not tenant-scoped work.
func closeServiceBusNotifyConn(conn serviceBusNotifyConn) {
	ctx, cancel := context.WithTimeout(context.Background(), serviceBusListenCloseMax)
	defer cancel()
	_ = conn.Close(ctx)
}

// dispatch wakes the subscribers of channel ("" wakes all). Sends never block: a pending wake
// already covers this notification.
func (h *serviceBusHub) dispatch(listener *serviceBusListener, channel string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for sub := range listener.subs {
		if _, ok := sub.channels[channel]; ok || channel == "" {
			select {
			case sub.wake <- struct{}{}:
			default:
			}
		}
	}
}

// serviceBusPGXConnect opens a LISTEN connection outside the pool with the pool's settings.
func serviceBusPGXConnect(config *pgx.ConnConfig) serviceBusConnectFunc {
	return func(ctx context.Context) (serviceBusNotifyConn, error) {
		conn, err := pgx.ConnectConfig(ctx, config.Copy())
		if err != nil {
			return nil, err
		}
		if _, err := conn.Exec(ctx, "LISTEN "+busmodels.ServiceBusNotifyChannel); err != nil {
			closeServiceBusNotifyConn(conn)
			return nil, err
		}
		return conn, nil
	}
}
