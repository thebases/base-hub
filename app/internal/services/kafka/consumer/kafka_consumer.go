package kafkaconsumer

// File overview:
// - Responsibility: owns long-lived Kafka consumer flow for kafka consumer, including polling, retry/backoff, and delegation into business handlers.
// - Key symbols: `DefaultConsumerOptions`, `StartConsumersPerTopic`, `Handler`, `ConsumerOptions`.
// - Pattern: Threads `context.Context` through the main execution paths so cancellation, auth scope, and transaction state can follow the work end to end.
// - Pattern: Protects shared mutable state with explicit synchronization or worker coordination rather than relying on unsupervised goroutine side effects.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"git.thebase.vn/base_hub/internal/config"
	"git.thebase.vn/base_hub/internal/services/log"
	"github.com/segmentio/kafka-go"
)

/* ----------------------------- Public API ----------------------------- */

type Handler func(ctx context.Context, msg kafka.Message) error

type ConsumerOptions struct {
	GroupID        string
	MinBytes       int
	MaxBytes       int
	MaxWait        time.Duration
	CommitInterval time.Duration
	MinBackoff     time.Duration
	MaxBackoff     time.Duration

	// Logging
	PrettyJSON bool // pretty-print JSON values if true
	MaxLogLen  int  // truncate value logs beyond this (# of bytes); 0 = no truncation

	// HandlerMaxAttempts is how many times a failing handler is run for one message
	// before the consumer gives up and moves on (<=1 = no retry). Handlers are not
	// yet idempotent per delivery (workflow runs get a fresh request id), so the
	// default stays 1; raise it only for handlers known to tolerate re-execution.
	HandlerMaxAttempts int

	// Dedup
	DedupeTTL      time.Duration // e.g., 5 * time.Minute; 0 = disable dedupe
	DedupeMaxItems int           // e.g., 50_000 (cap memory). Ignored if DedupeTTL==0
}

// DefaultConsumerOptions selects the consumer options handled by this package.
func DefaultConsumerOptions() ConsumerOptions {
	dedupeTTL := 5 * time.Minute
	dedupeMaxItems := 50_000
	if !config.Get().Kafka.UseDedupe {
		dedupeTTL = 0
		dedupeMaxItems = 0
	}
	return ConsumerOptions{
		GroupID:        "ipn-consumer-group",
		MinBytes:       10e3,
		MaxBytes:       10e6,
		MaxWait:        1 * time.Second,
		CommitInterval: 1 * time.Second,
		MinBackoff:     500 * time.Millisecond,
		MaxBackoff:     10 * time.Second,
		PrettyJSON:     true,
		MaxLogLen:      4096, // ~4 KB
		DedupeTTL:      dedupeTTL,
		DedupeMaxItems: dedupeMaxItems,
	}
}

// StartConsumersPerTopic spins up one reader per topic (no FlowWorkerPool but use sync package instead)
// - topics: e.g. []string{"ipn_napas","ipn_mb","ipn_bidv"}
// - handlers: map[topic]Handler
// Package sync provides basic synchronization primitives such as mutual exclusion locks.
// Other than the Once and WaitGroup types, most are intended for use by low-level library routines.
// Higher-level synchronization is better done via channels and communication.
func StartConsumersPerTopic(
	parent context.Context,
	brokers []string,
	topics []string,
	handlers map[string]Handler,
	opts ConsumerOptions,
) (cancelAll context.CancelFunc, wg *sync.WaitGroup) {
	ctx, cancel := context.WithCancel(parent)
	wg = &sync.WaitGroup{}

	// Check number of topic that consuming.
	// if no topic stop listening
	if len(topics) == 0 {
		log.Warn("kafka: no topics provided; nothing to start")
		return cancel, wg
	}

	// In-memory deduper (optional)
	// Define Deduper for hanlding the dedupe message from kafka
	var d *inMemoryDeduper
	if opts.DedupeTTL > 0 {
		max := opts.DedupeMaxItems
		if max <= 0 {
			max = 50_000
		}
		d = newInMemoryDeduper(opts.DedupeTTL, max)
	}

	// create iterable for topics
	for _, t := range topics {
		topic := t               // capture
		h, ok := handlers[topic] // set handler for each topic
		// if there's no hanlder for the topic, by pass this topic
		if !ok || h == nil {
			log.Warnf("kafka: no handler registered for topic=%s; skipping consumer", topic)
			continue
		}

		// add 1 to sync.WaitGroup counter
		// because a new topic will be handled
		wg.Add(1)
		// Launch the background work separately so the current request path can keep moving.
		go func(topic string, handler Handler) {
			// Define that following command will be called at the end of code block
			defer wg.Done()

			// Create a new Kafka Reader
			reader := kafka.NewReader(kafka.ReaderConfig{
				Brokers:         brokers,
				GroupID:         opts.GroupID,
				Topic:           topic,
				MinBytes:        opts.MinBytes,
				MaxBytes:        opts.MaxBytes,
				MaxWait:         opts.MaxWait,
				CommitInterval:  opts.CommitInterval,
				ReadLagInterval: -1,
			})
			// Define that following command will be called at the end of code block
			defer func() {
				if err := reader.Close(); err != nil {
					log.Errorf("kafka: reader close error (topic=%s): %v", topic, err)
				}
			}()
			log.Debugf("kafka: consumer started (group=%s, topic=%s)", opts.GroupID, topic)
			consumeLoop(ctx, reader, topic, handler, opts, d)
		}(topic, h)
	}
	return cancel, wg
}

// messageReader is the slice of *kafka.Reader the consume loop needs, so the
// loop can be tested against a fake reader.
type messageReader interface {
	FetchMessage(ctx context.Context) (kafka.Message, error)
	CommitMessages(ctx context.Context, msgs ...kafka.Message) error
}

// consumeLoop fetches, handles, then commits (at-least-once): the offset is
// committed only after the handler ran, never on read. A handler that keeps
// failing is retried up to opts.HandlerMaxAttempts and then skipped (logged,
// not dedupe-marked, so a producer re-send is still processed) because a
// consumer-group offset is cumulative and an uncommitted poison message
// would otherwise wedge its partition.
func consumeLoop(ctx context.Context, reader messageReader, topic string, handler Handler, opts ConsumerOptions, d *inMemoryDeduper) {
	minBackoff := opts.MinBackoff
	if minBackoff <= 0 {
		minBackoff = 500 * time.Millisecond
	}
	maxBackoff := opts.MaxBackoff
	if maxBackoff <= 0 {
		maxBackoff = 10 * time.Second
	}
	backoff := minBackoff

	for {
		msg, err := reader.FetchMessage(ctx)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				log.Debugf("kafka: stopping consumer (topic=%s)", topic)
				return
			}
			log.Errorf("kafka: read error (topic=%s): %v (backoff=%s)", topic, err, backoff)
			if !sleepCtx(ctx, backoff) {
				return
			}
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
			continue
		}
		backoff = minBackoff

		// Only successfully handled messages are marked, so a redelivery after a
		// failure is processed instead of dropped as a duplicate.
		if d != nil && d.Seen(topic, msg) {
			log.Warnf("kafka: duplicate skipped (topic=%s key=%s offset=%d)", topic, string(msg.Key), msg.Offset)
			commitMessage(ctx, reader, topic, msg)
			continue
		}

		log.Debugf(
			"kafka: received message topic=%s partition=%d offset=%d key=%s value=%s",
			msg.Topic, msg.Partition, msg.Offset, string(msg.Key), stringifyValue(msg.Value, opts.PrettyJSON, opts.MaxLogLen),
		)

		if err := runHandler(ctx, handler, msg, opts.HandlerMaxAttempts, minBackoff, maxBackoff); err != nil {
			if ctx.Err() != nil {
				// Shutting down mid-handler: leave the offset uncommitted so the message is redelivered.
				return
			}
			log.Errorf("kafka: handler failed, skipping message (topic=%s, partition=%d, offset=%d, key:%s): %v", topic, msg.Partition, msg.Offset, string(msg.Key), err)
		} else {
			if d != nil {
				d.Mark(topic, msg)
			}
			log.Debugf("kafka: handler success (topic=%s, offset=%d, key:%s)", topic, msg.Offset, string(msg.Key))
		}
		commitMessage(ctx, reader, topic, msg)
	}
}

func commitMessage(ctx context.Context, reader messageReader, topic string, msg kafka.Message) {
	if err := reader.CommitMessages(ctx, msg); err != nil && ctx.Err() == nil {
		log.Errorf("kafka: commit failed (topic=%s, partition=%d, offset=%d): %v", topic, msg.Partition, msg.Offset, err)
	}
}

// runHandler runs handler with panic recovery and bounded, context-aware retries.
func runHandler(ctx context.Context, handler Handler, msg kafka.Message, attempts int, minBackoff, maxBackoff time.Duration) error {
	if attempts < 1 {
		attempts = 1
	}
	delay := minBackoff
	var err error
	for attempt := 1; attempt <= attempts; attempt++ {
		err = safeHandle(ctx, handler, msg)
		if err == nil || attempt == attempts {
			return err
		}
		log.Warnf("kafka: handler attempt %d/%d failed (topic=%s, offset=%d): %v", attempt, attempts, msg.Topic, msg.Offset, err)
		if !sleepCtx(ctx, delay) {
			return ctx.Err()
		}
		delay *= 2
		if delay > maxBackoff {
			delay = maxBackoff
		}
	}
	return err
}

func safeHandle(ctx context.Context, handler Handler, msg kafka.Message) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("handler panic: %v", r)
		}
	}()
	return handler(ctx, msg)
}

// sleepCtx waits d or until ctx is done; it reports whether the full wait elapsed.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

/* ------------------------ In-memory deduper ------------------------- */

type dedupeEntry struct {
	key string
	ts  int64
}

// inMemoryDeduper keeps a map for lookups and an insertion-ordered queue for
// expiry. Marks arrive in time order, so the queue front is always the oldest
// entry: purge and eviction pop from the front instead of sorting the map.
type inMemoryDeduper struct {
	ttl      time.Duration
	maxItems int
	mu       sync.Mutex
	seen     map[string]int64 // key -> latest unixNano mark
	queue    []dedupeEntry    // marks in time order; may hold superseded entries
	head     int
}

// newInMemoryDeduper constructs and returns the in memory deduper handled by this package.
func newInMemoryDeduper(ttl time.Duration, maxItems int) *inMemoryDeduper {
	return &inMemoryDeduper{
		ttl:      ttl,
		maxItems: maxItems,
		seen:     make(map[string]int64, maxItems/2),
	}
}

// Seen reports whether msg was already handled successfully within the TTL.
// It never records anything; call Mark after the handler succeeds.
func (d *inMemoryDeduper) Seen(topic string, msg kafka.Message) bool {
	fp := fingerprint(topic, msg)
	d.mu.Lock()
	defer d.mu.Unlock()
	ts, ok := d.seen[fp]
	return ok && time.Since(time.Unix(0, ts)) <= d.ttl
}

// Mark records msg as handled so later deliveries within the TTL are skipped.
func (d *inMemoryDeduper) Mark(topic string, msg kafka.Message) {
	now := time.Now().UnixNano()
	fp := fingerprint(topic, msg)
	d.mu.Lock()
	defer d.mu.Unlock()

	d.purgeExpiredLocked(now)
	d.seen[fp] = now
	d.queue = append(d.queue, dedupeEntry{key: fp, ts: now})

	// Size cap: evict the oldest 10% if we exceed max
	if d.maxItems > 0 && len(d.seen) > d.maxItems {
		d.evictOldestLocked(len(d.seen) / 10)
	}
	d.compactLocked()
}

// popLocked removes the oldest queue entry and drops its key unless a later
// Mark superseded it.
func (d *inMemoryDeduper) popLocked() {
	entry := d.queue[d.head]
	d.head++
	if d.seen[entry.key] == entry.ts {
		delete(d.seen, entry.key)
	}
}

func (d *inMemoryDeduper) purgeExpiredLocked(now int64) {
	cutoff := now - d.ttl.Nanoseconds()
	for d.head < len(d.queue) && d.queue[d.head].ts < cutoff {
		d.popLocked()
	}
}

// evictOldestLocked drops n live keys, oldest first.
func (d *inMemoryDeduper) evictOldestLocked(n int) {
	for n > 0 && d.head < len(d.queue) {
		entry := d.queue[d.head]
		live := d.seen[entry.key] == entry.ts
		d.popLocked()
		if live {
			n--
		}
	}
}

// compactLocked reclaims the consumed queue prefix once it dominates the slice.
func (d *inMemoryDeduper) compactLocked() {
	if d.head > 1024 && d.head*2 > len(d.queue) {
		d.queue = append([]dedupeEntry(nil), d.queue[d.head:]...)
		d.head = 0
	}
}

func fingerprint(topic string, msg kafka.Message) string {
	sum := sha256.Sum256(msg.Value)
	return topic + "|" + string(msg.Key) + "|" + hex.EncodeToString(sum[:])
}

/* ------------------------------ Utils ------------------------------- */

func stringifyValue(b []byte, pretty bool, maxLen int) string {
	// Try JSON prettify
	if pretty {
		var v any
		if json.Unmarshal(b, &v) == nil {
			out, err := json.MarshalIndent(v, "", "  ")
			if err == nil {
				return truncate(string(out), maxLen)
			}
		}
	}
	// Fallback to raw bytes as string
	return truncate(string(b), maxLen)
}

func truncate(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	if max <= 3 {
		return s[:max]
	}
	return fmt.Sprintf("%s...", s[:max-3])
}
