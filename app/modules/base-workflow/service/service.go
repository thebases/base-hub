package service

// File overview:
// - Responsibility: thin facade over the workflow service split (engine / nodes / libs) so
//   external callers (workflow.go's runtime registration, base-etlflow, admin routes) keep a
//   single stable import path instead of reaching into the internal subpackages directly.
// - Key symbols: `InitRuntime`, `NewService`, `ActionHandler`, `ActionRequest`, `Service`.
// - Pattern: Mirrors internal/models/base_aliases.go's alias-and-delegate shape.

import (
	"context"

	"git.thebase.vn/base_hub/modules/base-workflow/service/engine"
	"git.thebase.vn/base_hub/modules/base-workflow/service/libs"
)

var (
	ErrFlowNotFound             = engine.ErrFlowNotFound
	ErrActionDefinitionNotFound = engine.ErrActionDefinitionNotFound
	ErrActionNotSupported       = engine.ErrActionNotSupported
	ErrRunRetryScheduled        = engine.ErrRunRetryScheduled
)

type ActionHandler = engine.ActionHandler
type ActionRequest = engine.ActionRequest
type Service = engine.Service

// NewService constructs and returns the core workflow engine service.
func NewService() *Service {
	return engine.NewService()
}

// InitRuntime wires the workflow engine's action/trigger runtime hooks and background jobs.
func InitRuntime() func() {
	return engine.InitRuntime()
}

// SyncWorkflowTriggers registers the active Kafka/MQTT/model triggers of every connected
// database. Called once after the server's database bootstrap so tenant databases discovered
// after module bootstrap get their triggers too; re-registering an existing trigger is a no-op.
func SyncWorkflowTriggers(ctx context.Context) {
	engine.DefaultTriggerRegistry().SyncAllDatabases(ctx)
}

// LogFlowExecutionResult logs the outcome of a flow execution handled by the engine.
func LogFlowExecutionResult(topic string, err error) {
	engine.LogFlowExecutionResult(topic, err)
}

type OutboundDeliveryWorkerConfig = libs.OutboundDeliveryWorkerConfig

// InitOutboundDeliveryWorker starts the shared outbound HTTP delivery worker pool.
func InitOutboundDeliveryWorker(cfg OutboundDeliveryWorkerConfig) {
	libs.InitOutboundDeliveryWorker(cfg)
}

// ShutdownOutboundDeliveryWorker stops the shared outbound HTTP delivery worker pool.
func ShutdownOutboundDeliveryWorker() {
	libs.ShutdownOutboundDeliveryWorker()
}

// HmacAction computes an HMAC-SHA256 signature over the JSON payload fields.
func HmacAction(key string, jsonData map[string]interface{}, keyList []string) (string, string, string, error) {
	return libs.HmacAction(key, jsonData, keyList)
}

// AesAction encrypts the JSON payload with AES-CBC.
func AesAction(key string, jsonData map[string]interface{}) (string, string, string, error) {
	return libs.AesAction(key, jsonData)
}
