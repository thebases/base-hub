package kafkaconsumer

// File overview:
// - Responsibility: owns long-lived Kafka consumer flow for kafka handler, including polling, retry/backoff, and delegation into business handlers.
// - Key symbols: `KafkaHandlers`, `isFlowNotFound`, `NapasIPN`, `MBIPN`.
// - Pattern: Threads `context.Context` through the main execution paths so cancellation, auth scope, and transaction state can follow the work end to end.
// - Pattern: Treats streaming transport as infrastructure: retry/backoff, dedupe, and handler delegation are kept separate from the business logic that consumes each message.

import (
	"context"
	"encoding/json"
	"errors"
	"log"

	"git.thebase.vn/base_hub/internal/config"
	"git.thebase.vn/base_hub/internal/services/banketl"
	"git.thebase.vn/base_hub/internal/services/etlflow"
	"github.com/segmentio/kafka-go"
)

// var kafkaRoutes = map[string]Handler{
// 			"ipn_napas": func(ctx context.Context, msg kafka.Message) error {
// 				var ev NapasIPN

// 				return json.Unmarshal(msg.Value, &ev) // plus your business logic
// 			},
// 			"ipn_mb": func(ctx context.Context, msg kafka.Message) error {
// 				var ev MBIPN
// 				return json.Unmarshal(msg.Value, &ev)
// 			},
// 			"ipn_bidv": func(ctx context.Context, msg kafka.Message) error {
// 				var ev BIDVIPN
// 				return json.Unmarshal(msg.Value, &ev)
// 			},
// 		}

type NapasIPN struct {
	TxnID  string  `json:"txn_id"`
	Amount float64 `json:"amount"`
}
type MBIPN struct {
	RefID  string `json:"ref_id"`
	Status string `json:"status"`
}
type BIDVIPN struct {
	OrderID  string `json:"order_id"`
	BankCode string `json:"bank_code"`
}

// bankFlowHandler narrows banketl.Service down to the two flow entry points
// this consumer dispatches to, so kafka handler wiring can be tested against
// a fake bank-flow handler without touching banketl's dispatch/balance logic.
type bankFlowHandler interface {
	HandleNapasFlow(ctx context.Context, input []byte) error
	HandleMBFlow(ctx context.Context, input []byte) error
}

type handlerSet struct {
	bankService bankFlowHandler
	flowService *etlflow.Service
}

func KafkaHandlers() map[string]Handler {
	handlersForTopic := handlerSet{
		bankService: banketl.NewService(),
		flowService: etlflow.NewService(),
	}

	handlers := map[string]Handler{}
	for _, topic := range config.Get().Kafka.Topics {
		// Default every configured topic to metadata-backed ETL execution so new
		// flows can become live without adding more hardcoded transport wiring.
		handlers[topic] = handlersForTopic.handleMetadataOnly
	}

	handlers["ipn_napas"] = handlersForTopic.handleNapas
	handlers["bank_ipn_topic"] = handlersForTopic.handleMB
	handlers["ipn_bidv"] = handlersForTopic.handleBIDV
	return handlers
}

func (h handlerSet) handleMetadataOnly(ctx context.Context, msg kafka.Message) error {
	err := h.flowService.ExecuteTopic(ctx, msg.Topic, msg.Value)
	etlflow.LogFlowExecutionResult(msg.Topic, err)
	return flowHandlerResult(err)
}

// flowHandlerResult maps a workflow execution error to the consumer's result. A scheduled retry
// counts as handled: the run is stored and resume_due_runs owns its next attempt, so returning the
// error would make runHandler execute the topic again and start a second run for the message.
func flowHandlerResult(err error) error {
	if errors.Is(err, etlflow.ErrRunRetryScheduled) {
		return nil
	}
	return err
}

func (h handlerSet) handleNapas(ctx context.Context, msg kafka.Message) error {
	shouldFallback, err := h.shouldFallbackToLegacy(ctx, msg)
	if err != nil || !shouldFallback {
		return err
	}
	h.logLegacyFallback(msg)
	return h.bankService.HandleNapasFlow(ctx, msg.Value)
}

func (h handlerSet) handleMB(ctx context.Context, msg kafka.Message) error {
	shouldFallback, err := h.shouldFallbackToLegacy(ctx, msg)
	if err != nil || !shouldFallback {
		return err
	}
	h.logLegacyFallback(msg)
	return h.bankService.HandleMBFlow(ctx, msg.Value)
}

func (h handlerSet) handleBIDV(ctx context.Context, msg kafka.Message) error {
	shouldFallback, err := h.shouldFallbackToLegacy(ctx, msg)
	if err != nil || !shouldFallback {
		return err
	}
	var ev BIDVIPN
	return json.Unmarshal(msg.Value, &ev)
}

func (h handlerSet) shouldFallbackToLegacy(ctx context.Context, msg kafka.Message) (bool, error) {
	err := h.flowService.ExecuteTopic(ctx, msg.Topic, msg.Value)
	if err == nil {
		etlflow.LogFlowExecutionResult(msg.Topic, nil)
		return false, nil
	}
	if flowHandlerResult(err) == nil {
		etlflow.LogFlowExecutionResult(msg.Topic, err)
		return false, nil
	}
	// Keep metadata-backed ETL as the primary path; only the specific "flow not found" case should fall back to legacy handlers.
	if !isFlowNotFound(err) {
		etlflow.LogFlowExecutionResult(msg.Topic, err)
		return false, err
	}
	return true, nil
}

func (h handlerSet) logLegacyFallback(msg kafka.Message) {
	log.Printf("KafkaHandlers is received message on topic %s: %s", msg.Topic, string(msg.Value))
}

func isFlowNotFound(err error) bool {
	return errors.Is(err, etlflow.ErrFlowNotFound)
}
