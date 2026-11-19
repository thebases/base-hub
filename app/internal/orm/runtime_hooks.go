package orm

// File overview:
// - Responsibility: implements the metadata-driven ORM/runtime kernel for runtime hooks, where storage, runtime dispatch, and security rules stay centralized.
// - Key symbols: `RegisterCompute`, `RegisterConstraint`, `ComputeHandler`, `ConstraintHandler`.
// - Pattern: Leans on metadata-driven registration so storage mapping, runtime methods, and behavior hooks stay centralized instead of being scattered across handlers.
// - Pattern: Threads `context.Context` through the main execution paths so cancellation, auth scope, and transaction state can follow the work end to end.

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
)

type ComputeHandler func(ctx context.Context, records RecordSet, field FieldMetadata) (map[string]any, error)
type ConstraintHandler func(ctx context.Context, records RecordSet, changedValues RecordMap) error
type OnchangeHandler func(ctx context.Context, records RecordSet, values RecordMap, changedFields []string, fieldsSpec map[string]any) (RecordMap, error)
type AccessHandler func(ctx context.Context, env *Environment, model *ModelMetadata, operation string, ids []string, values RecordMap) error
type RecordRuleHandler func(ctx context.Context, env *Environment, model *ModelMetadata, operation string) (DomainNode, error)

// RecordEventHandler observes a record lifecycle event after the owning
// transaction has already committed successfully (see fireRecordEvent). It
// must never block: any external side effect (executing a workflow, calling
// out over HTTP) has to be dispatched via its own goroutine so the caller —
// RuntimeCreate/RuntimeWrite/RuntimeUnlink — returns to its own caller
// immediately.
type RecordEventHandler func(ctx context.Context, event string, modelName string, id string, record RecordMap)

// Record-lifecycle event names. "before_*" carries the pre-image snapshot
// (values before the mutation), "after_*" carries the post-image snapshot
// (values after the mutation). Every event fires only once the writing
// transaction has committed — there is no in-transaction "before" hook here,
// unlike RegisterConstraint, because a record-event handler is an eventing
// hook (fire-and-forget notification), not a validation gate: firing it
// in-transaction would risk notifying about a mutation a later step in the
// same transaction still rolls back.
const (
	RecordEventBeforeCreate = "before_create"
	RecordEventAfterCreate  = "after_create"
	RecordEventBeforeWrite  = "before_write"
	RecordEventAfterWrite   = "after_write"
	RecordEventBeforeUnlink = "before_unlink"
	RecordEventAfterUnlink  = "after_unlink"
)

type registeredConstraint struct {
	fields  []string
	handler ConstraintHandler
}

type registeredOnchange struct {
	fields  []string
	handler OnchangeHandler
}

var runtimeHookRegistry = struct {
	mu            sync.RWMutex
	computes      map[string]map[string]ComputeHandler
	constraints   map[string][]registeredConstraint
	onchanges     map[string][]registeredOnchange
	access        map[string]AccessHandler
	defaultAccess AccessHandler
	rules         map[string]RecordRuleHandler
	defaultRules  RecordRuleHandler
	recordEvents  map[string][]RecordEventHandler
}{
	computes:     map[string]map[string]ComputeHandler{},
	constraints:  map[string][]registeredConstraint{},
	onchanges:    map[string][]registeredOnchange{},
	access:       map[string]AccessHandler{},
	rules:        map[string]RecordRuleHandler{},
	recordEvents: map[string][]RecordEventHandler{},
}

// RegisterCompute binds one compute handler to a model field.
func RegisterCompute(modelName, fieldName string, handler ComputeHandler) {
	if strings.TrimSpace(modelName) == "" || strings.TrimSpace(fieldName) == "" || handler == nil {
		return
	}
	runtimeHookRegistry.mu.Lock()
	defer runtimeHookRegistry.mu.Unlock()
	if runtimeHookRegistry.computes[modelName] == nil {
		// Allocate the per-model field map lazily so unused models stay cheap.
		runtimeHookRegistry.computes[modelName] = map[string]ComputeHandler{}
	}
	// One compute handler owns one field on one model.
	runtimeHookRegistry.computes[modelName][fieldName] = handler
}

// ValidateComputeHandlers verifies that every metadata-declared computed field
// (a `compute:` tag with no `related:` chain) has a handler registered through
// RegisterCompute. Startup must fail fast on a miss (SRS-F-018): a computed
// field without a handler would silently read as empty on every request, which
// is exactly the class of quiet data drift the compute contract forbids.
func ValidateComputeHandlers() error {
	runtimeHookRegistry.mu.RLock()
	defer runtimeHookRegistry.mu.RUnlock()
	var missing []string
	// Walk each registered model so the check covers kernel and module models
	// alike; only Go-registered metadata exists at bootstrap time, so runtime
	// user-defined models cannot brick startup through this validation.
	for _, meta := range RegisteredModels() {
		for _, field := range meta.Fields {
			// Related chains resolve through relational reads, not compute
			// handlers, so a related field never needs a registration.
			if strings.TrimSpace(field.Compute) == "" || len(field.Related) > 0 {
				continue
			}
			if runtimeHookRegistry.computes[meta.Name][field.Name] == nil {
				missing = append(missing, meta.Name+"."+field.Name)
			}
		}
	}
	if len(missing) == 0 {
		return nil
	}
	// Deterministic ordering keeps the boot error stable across runs so ops can
	// diff logs between deployments.
	sort.Strings(missing)
	return fmt.Errorf("orm: computed fields have no registered compute handler: %s", strings.Join(missing, ", "))
}

// RegisterConstraint binds one constraint handler to a model.
func RegisterConstraint(modelName string, fields []string, handler ConstraintHandler) {
	if strings.TrimSpace(modelName) == "" || handler == nil {
		return
	}
	runtimeHookRegistry.mu.Lock()
	defer runtimeHookRegistry.mu.Unlock()
	// Preserve registration order so validation runs predictably.
	runtimeHookRegistry.constraints[modelName] = append(runtimeHookRegistry.constraints[modelName], registeredConstraint{
		fields:  append([]string(nil), fields...),
		handler: handler,
	})
}

// RegisterOnchange binds one onchange handler to a model.
func RegisterOnchange(modelName string, fields []string, handler OnchangeHandler) {
	if strings.TrimSpace(modelName) == "" || handler == nil {
		return
	}
	runtimeHookRegistry.mu.Lock()
	defer runtimeHookRegistry.mu.Unlock()
	// Preserve registration order here as well because onchange side effects may
	// be order-sensitive for callers.
	runtimeHookRegistry.onchanges[modelName] = append(runtimeHookRegistry.onchanges[modelName], registeredOnchange{
		fields:  append([]string(nil), fields...),
		handler: handler,
	})
}

// OnchangeTriggerField reports whether changing fieldName on modelName would run
// at least one registered onchange handler (see RegisterOnchange). A hook
// registered with an empty fields list runs on every change, so it matches any
// field. Used to populate FieldMetadata.HasOnchange for frontend consumers.
func OnchangeTriggerField(modelName, fieldName string) bool {
	runtimeHookRegistry.mu.RLock()
	defer runtimeHookRegistry.mu.RUnlock()
	for _, hook := range runtimeHookRegistry.onchanges[modelName] {
		if len(hook.fields) == 0 {
			return true
		}
		for _, field := range hook.fields {
			if field == fieldName {
				return true
			}
		}
	}
	return false
}

// RegisterAccessHandler installs a model-level access checker.
func RegisterAccessHandler(modelName string, handler AccessHandler) {
	if strings.TrimSpace(modelName) == "" || handler == nil {
		return
	}
	runtimeHookRegistry.mu.Lock()
	defer runtimeHookRegistry.mu.Unlock()
	runtimeHookRegistry.access[modelName] = handler
}

// RegisterDefaultAccessHandler installs a fallback model-level access checker
// used when the target model has no model-specific handler.
func RegisterDefaultAccessHandler(handler AccessHandler) {
	if handler == nil {
		return
	}
	runtimeHookRegistry.mu.Lock()
	defer runtimeHookRegistry.mu.Unlock()
	runtimeHookRegistry.defaultAccess = handler
}

// RegisterRecordRule installs a record-rule domain provider for a model.
func RegisterRecordRule(modelName string, handler RecordRuleHandler) {
	if strings.TrimSpace(modelName) == "" || handler == nil {
		return
	}
	runtimeHookRegistry.mu.Lock()
	defer runtimeHookRegistry.mu.Unlock()
	runtimeHookRegistry.rules[modelName] = handler
}

// HasRecordRuleHandler reports whether a model-specific record-rule provider is
// registered for the model (the generic default provider does not count). Callers
// that cache results across users use it to detect per-user row filtering.
func HasRecordRuleHandler(modelName string) bool {
	runtimeHookRegistry.mu.RLock()
	defer runtimeHookRegistry.mu.RUnlock()
	_, ok := runtimeHookRegistry.rules[strings.TrimSpace(modelName)]
	return ok
}

// RegisterDefaultRecordRule installs a fallback record-rule domain provider
// used when the target model does not have model-specific rule logic.
func RegisterDefaultRecordRule(handler RecordRuleHandler) {
	if handler == nil {
		return
	}
	runtimeHookRegistry.mu.Lock()
	defer runtimeHookRegistry.mu.Unlock()
	runtimeHookRegistry.defaultRules = handler
}

// RegisterRecordEventHandler binds one record-lifecycle event handler to a
// model. There is no Unregister (mirrors RegisterConstraint/RegisterOnchange):
// callers that need per-row enable/disable semantics (e.g. base.workflow.trigger
// model triggers) register one generic handler per target model the first
// time a row targets it, then do their own enabled/domain filtering inside
// the handler body — see sys_model_constraint.go's businessRuleConstraintHandler
// for the established pattern this mirrors.
func RegisterRecordEventHandler(modelName string, handler RecordEventHandler) {
	if strings.TrimSpace(modelName) == "" || handler == nil {
		return
	}
	runtimeHookRegistry.mu.Lock()
	defer runtimeHookRegistry.mu.Unlock()
	runtimeHookRegistry.recordEvents[modelName] = append(runtimeHookRegistry.recordEvents[modelName], handler)
}

// HasRecordEventHandlers reports whether modelName has at least one
// registered record-event handler. Create/Write/Unlink call this first so
// the common case — no model trigger targets this model — costs one RLock'd
// map lookup instead of an extra pre/post-image read on every write.
func HasRecordEventHandlers(modelName string) bool {
	runtimeHookRegistry.mu.RLock()
	defer runtimeHookRegistry.mu.RUnlock()
	return len(runtimeHookRegistry.recordEvents[modelName]) > 0
}

// fireRecordEvent invokes every handler registered for modelName. Always
// called through fireRecordEventsAfterCommit, i.e. after the outermost
// transaction has committed (see AfterCommit) — never from inside an open
// transaction — so a handler can safely treat the record as durably persisted.
// Exception: a transaction opened outside the ORM without WithAfterCommitScope
// gives the ORM no commit signal, and events still fire when the write returns.
func fireRecordEvent(ctx context.Context, event string, modelName string, id string, record RecordMap) {
	runtimeHookRegistry.mu.RLock()
	handlers := append([]RecordEventHandler(nil), runtimeHookRegistry.recordEvents[modelName]...)
	runtimeHookRegistry.mu.RUnlock()
	for _, handler := range handlers {
		handler(ctx, event, modelName, id, record)
	}
}
