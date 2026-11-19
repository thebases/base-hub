package kafkaproducer

// File overview:
// - Responsibility: owns Kafka producer behavior for kafka producer, isolating topic publishing concerns from the business services that emit events.
// - Key symbols: `NewProducer`, `error`, `Producer`.
// - Pattern: Threads `context.Context` through the main execution paths so cancellation, auth scope, and transaction state can follow the work end to end.
// - Pattern: Treats streaming transport as infrastructure: retry/backoff, dedupe, and handler delegation are kept separate from the business logic that consumes each message.

import (
	"context"
	"errors"
	"time"

	"git.thebase.vn/base_hub/internal/services/log"
	"github.com/segmentio/kafka-go"
)

// Producer holds cluster config only (no writers kept).
type Producer struct {
	brokers []string
}

var Kafkabroker *Producer

// NewProducer initializes a producer without binding to a topic.
func NewProducer(brokers []string) *Producer {
	log.Debugf("Kafka producer initialized for brokers: %v", brokers)
	Kafkabroker = &Producer{brokers: brokers}
	return Kafkabroker
}

// Publish creates a writer for the given topic, sends one message, then closes it.
// Use 'key' for stable partitioning (optional). 'headers' may be nil.
func (p *Producer) Publish(ctx context.Context, topic string, key, value []byte, headers []kafka.Header) error {
	if p == nil {
		return errors.New("producer not initialized")
	}
	if topic == "" {
		return errors.New("topic required")
	}

	// Build a fresh writer for this publish
	w := &kafka.Writer{
		Addr:                   kafka.TCP(p.brokers...),
		Topic:                  topic,
		Balancer:               &kafka.Hash{},    // keep same-key ordering
		RequiredAcks:           kafka.RequireAll, // safer durability
		Async:                  false,            // report delivery result
		BatchTimeout:           10 * time.Millisecond,
		AllowAutoTopicCreation: true, // toggle if your cluster disallows this
		// Compression:         kafka.Snappy,         // enable if you want compression
	}
	defer func() {
		if err := w.Close(); err != nil {
			log.Warnf("kafka writer close warning for topic %s: %v", topic, err)
		}
	}()

	// Ensure we have a timeout if the caller didn’t set one
	var cancel context.CancelFunc
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		ctx, cancel = context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
	}

	msg := kafka.Message{
		Key:     key,
		Value:   value,
		Time:    time.Now(),
		Headers: headers,
	}

	// Retry a few times with simple backoff
	const (
		maxAttempts = 3
		baseDelay   = 200 * time.Millisecond
	)

	var err error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		err = w.WriteMessages(ctx, msg)
		if err == nil {
			return nil
		}
		if ctx.Err() != nil { // respect caller context
			break
		}
		backoff := time.Duration(attempt) * baseDelay
		log.Warnf("kafka publish to %s attempt %d/%d failed: %v (backoff %s)",
			topic, attempt, maxAttempts, err, backoff)
		select {
		case <-ctx.Done():
		case <-time.After(backoff):
		}
	}

	log.Errorf("kafka publish to %s failed after %d attempts: %v", topic, maxAttempts, err)
	return err
}

// Close exists for API symmetry; nothing to close in option B.
func (p *Producer) Close() error {
	return nil
}
