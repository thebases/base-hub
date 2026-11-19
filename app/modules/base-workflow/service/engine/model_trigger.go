package engine

// File overview:
// - Responsibility: owns `workflow` module trigger_type="model" dispatch — the record-lifecycle
//   (before/after create/write/unlink) trigger that fires a workflow off another model's CRUD
//   events instead of an inbound webhook/kafka/mqtt message.
// - Key symbols: `registerModelTrigger`, `unregisterModelTrigger`, `dispatchModelTriggerEvent`.
// - Pattern: One generic `orm.RecordEventHandler` is registered per target model (mirrors
//   sys_model_constraint.go's `ensureBusinessRuleHandlerRegistered` business-rule pattern) —
//   per-trigger enable/disable and domain filtering happen inside the handler body, not at the
//   ORM registration layer, since `orm.RegisterRecordEventHandler` has no Unregister.
// - Pattern: Protects shared mutable state with explicit synchronization rather than relying on
//   unsupervised goroutine side effects.
// - Tenancy (plan T47): entries are keyed by the registry key (`<db>|<id>`) and only fire for writes
//   in their own database; runs execute in that database through a bounded per-database queue.
// - Recursion: the chain of model triggers that led to a write travels in ctx; a trigger already
//   in the chain, or a chain of modelTriggerMaxDepth, is not fired again.
// - before_* events carry the pre-image but, like every record event, are delivered after commit
//   (see orm.RecordEventHandler): they can never block or change the write.

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
	"git.thebase.vn/base_hub/modules/base-workflow/models"
)

type modelTriggerEntry struct {
	trigger models.WorkflowTriggerRecord
	// dbName is the database the trigger row lives in; only writes there fire it.
	dbName string
	// operation is the single before_*/after_* event this trigger row fires on.
	operation string
	// triggerDomain gates the whole workflow: checked on every event fired for
	// the target model, independent of which operation this row is configured
	// for. Kept separate from domain (below) so authors can express "only ever
	// run for tenant X" without repeating it per-operation.
	triggerDomain    orm.DomainNode
	hasTriggerDomain bool
	// domain gates the selected operation event specifically (pre/post image
	// per before_*/after_* semantics already encoded in `operation`).
	domain    orm.DomainNode
	hasDomain bool
}

// modelTriggerCache indexes active model-trigger rows by the target model
// they watch so dispatchModelTriggerEvent — invoked synchronously from inside
// the ORM write path via orm.fireRecordEvent — only pays for one RLock'd map
// lookup on writes to models nothing is watching.
var modelTriggerCache = struct {
	mu               sync.RWMutex
	byTargetModel    map[string]map[string]modelTriggerEntry // target model -> registry key -> entry
	registeredModels map[string]bool
}{
	byTargetModel:    map[string]map[string]modelTriggerEntry{},
	registeredModels: map[string]bool{},
}

// registerModelTrigger stores/refreshes one trigger row in the cache keyed by
// its configured target model, and the first time any trigger targets that
// model, registers the one generic orm.RecordEventHandler dispatcher for it.
func registerModelTrigger(dbName, key string, trigger models.WorkflowTriggerRecord) error {
	targetModel := strings.TrimSpace(configString(trigger.Config, "model"))
	if targetModel == "" {
		return orm.InvalidParamsf("workflow: model trigger %d target model is required", trigger.ID)
	}
	operation := strings.TrimSpace(configString(trigger.Config, "operation"))
	if operation == "" {
		return orm.InvalidParamsf("workflow: model trigger %d operation is required", trigger.ID)
	}

	entry := modelTriggerEntry{trigger: trigger, dbName: dbName, operation: operation}
	if rawTriggerDomain, ok := trigger.Config["trigger_domain"]; ok && rawTriggerDomain != nil {
		triggerDomain, err := orm.ParseDomain(rawTriggerDomain)
		if err != nil {
			return fmt.Errorf("workflow: model trigger %d trigger_domain: %w", trigger.ID, err)
		}
		entry.triggerDomain = triggerDomain
		entry.hasTriggerDomain = true
	}
	if rawDomain, ok := trigger.Config["domain"]; ok && rawDomain != nil {
		domain, err := orm.ParseDomain(rawDomain)
		if err != nil {
			return fmt.Errorf("workflow: model trigger %d domain: %w", trigger.ID, err)
		}
		entry.domain = domain
		entry.hasDomain = true
	}

	modelTriggerCache.mu.Lock()
	if modelTriggerCache.byTargetModel[targetModel] == nil {
		modelTriggerCache.byTargetModel[targetModel] = map[string]modelTriggerEntry{}
	}
	modelTriggerCache.byTargetModel[targetModel][key] = entry
	needsRegister := !modelTriggerCache.registeredModels[targetModel]
	if needsRegister {
		modelTriggerCache.registeredModels[targetModel] = true
	}
	modelTriggerCache.mu.Unlock()

	// Registered outside the lock: orm.RegisterRecordEventHandler takes its
	// own lock and never calls back into this package synchronously, but
	// there is no reason to hold modelTriggerCache.mu while it runs.
	if needsRegister {
		orm.RegisterRecordEventHandler(targetModel, dispatchModelTriggerEvent)
	}
	return nil
}

// unregisterModelTrigger removes the registry key from targetModel's bucket. Safe to
// call even if the trigger was never a model trigger or already removed.
func unregisterModelTrigger(targetModel string, key string) {
	if targetModel == "" || key == "" {
		return
	}
	modelTriggerCache.mu.Lock()
	defer modelTriggerCache.mu.Unlock()
	delete(modelTriggerCache.byTargetModel[targetModel], key)
}

// dispatchModelTriggerEvent is the one orm.RecordEventHandler registered per
// target model. It is invoked synchronously, in-line, from the ORM's
// post-commit fireRecordEvent — so matching here (map lookup + domain
// evaluation against the already-fetched record snapshot) must stay cheap,
// and every matched workflow execution is handed to its own goroutine so a
// slow or failing downstream action can never add latency to, or fail, the
// write that produced this event.
func dispatchModelTriggerEvent(ctx context.Context, event string, modelName string, id string, record orm.RecordMap) {
	dbName := triggerDBName(ctx)
	modelTriggerCache.mu.RLock()
	entries := modelTriggerCache.byTargetModel[modelName]
	matches := make([]modelTriggerEntry, 0, len(entries))
	for _, entry := range entries {
		if !entry.trigger.IsActive || entry.operation != event || entry.dbName != dbName {
			continue
		}
		matches = append(matches, entry)
	}
	modelTriggerCache.mu.RUnlock()
	if len(matches) == 0 {
		return
	}
	chain := modelTriggerChainFromContext(ctx)

	for _, entry := range matches {
		if reason := modelTriggerChainBlocks(chain, entry.trigger.ID); reason != "" {
			log.Warnf("workflow: model trigger %d not fired for %s %s/%s in %s: %s", entry.trigger.ID, event, modelName, id, dbName, reason)
			continue
		}
		if entry.hasTriggerDomain {
			matched, err := orm.EvaluateDomain(record, entry.triggerDomain)
			if err != nil {
				log.Errorf("workflow: model trigger %d trigger_domain evaluation failed: %v", entry.trigger.ID, err)
				continue
			}
			if !matched {
				continue
			}
		}
		if entry.hasDomain {
			matched, err := orm.EvaluateDomain(record, entry.domain)
			if err != nil {
				log.Errorf("workflow: model trigger %d domain evaluation failed: %v", entry.trigger.ID, err)
				continue
			}
			if !matched {
				continue
			}
		}
		// Deliberately decoupled from the write's own context/goroutine: the
		// triggering request must not wait on, or be canceled by, workflow
		// execution. The run gets a fresh context bound to the event's database
		// and carrying the extended trigger chain.
		enqueueModelTriggerRun(modelTriggerJob{
			dbName:  dbName,
			trigger: entry.trigger,
			payload: modelTriggerPayload(event, modelName, id, record),
			chain:   append(append([]int64(nil), chain...), entry.trigger.ID),
		})
	}
}

// modelTriggerMaxDepth bounds trigger -> write -> trigger chains (a workflow writing the model it
// watches, or two workflows writing each other's models).
const modelTriggerMaxDepth = 3

type modelTriggerChainContextKey struct{}

func contextWithModelTriggerChain(ctx context.Context, chain []int64) context.Context {
	return context.WithValue(ctx, modelTriggerChainContextKey{}, chain)
}

// modelTriggerChainFromContext returns the ids of the model triggers whose runs led to this write.
func modelTriggerChainFromContext(ctx context.Context) []int64 {
	if ctx == nil {
		return nil
	}
	chain, _ := ctx.Value(modelTriggerChainContextKey{}).([]int64)
	return chain
}

// modelTriggerChainBlocks returns why triggerID must not fire for a write made under chain, or "".
func modelTriggerChainBlocks(chain []int64, triggerID int64) string {
	for _, id := range chain {
		if id == triggerID {
			return "trigger already in the chain (recursion)"
		}
	}
	if len(chain) >= modelTriggerMaxDepth {
		return fmt.Sprintf("trigger chain depth %d reached", modelTriggerMaxDepth)
	}
	return ""
}

// Bounded execution: a burst of writes must not spawn one goroutine per event.
const (
	modelTriggerWorkersPerDB = 4
	modelTriggerQueuePerDB   = 256
)

type modelTriggerJob struct {
	dbName  string
	trigger models.WorkflowTriggerRecord
	payload []byte
	chain   []int64
}

var modelTriggerQueues = struct {
	mu   sync.Mutex
	byDB map[string]chan modelTriggerJob
}{byDB: map[string]chan modelTriggerJob{}}

// executeModelTriggerJob runs one queued job in its database with the extended trigger chain.
func executeModelTriggerJob(job modelTriggerJob) {
	ctx := contextWithModelTriggerChain(context.Background(), job.chain)
	key := triggerKey(job.dbName, job.trigger.ID)
	triggerHealth.deliveryStart(key)
	err := executeTriggerInDB(ctx, job.dbName, job.trigger, job.payload)
	triggerHealth.deliveryEnd(key, err)
	if isRunRetryScheduled(err) {
		log.Infof("workflow: model trigger %d in %s: %v", job.trigger.ID, job.dbName, err)
	} else if err != nil {
		log.Errorf("workflow: model trigger %d execution failed in %s: %v", job.trigger.ID, job.dbName, err)
	}
}

// modelTriggerRunner is what the long-lived workers call; atomic so tests can swap it safely.
var modelTriggerRunner atomic.Pointer[func(modelTriggerJob)]

func init() {
	run := executeModelTriggerJob
	modelTriggerRunner.Store(&run)
}

// runModelTriggerJobSafely runs one job; a panic is logged as that trigger's failure so the
// long-lived worker survives (a dead worker would shrink the database's pool for the process life).
func runModelTriggerJobSafely(job modelTriggerJob) {
	defer func() {
		if recovered := recover(); recovered != nil {
			log.Errorf("workflow: model trigger %d panicked in %s: %v\n%s", job.trigger.ID, job.dbName, recovered, debug.Stack())
		}
	}()
	(*modelTriggerRunner.Load())(job)
}

// enqueueModelTriggerRun hands job to its database's workers without blocking the writer. When the
// queue is full the event is dropped and logged (model triggers are best-effort notifications).
func enqueueModelTriggerRun(job modelTriggerJob) bool {
	modelTriggerQueues.mu.Lock()
	queue, ok := modelTriggerQueues.byDB[job.dbName]
	if !ok {
		queue = make(chan modelTriggerJob, modelTriggerQueuePerDB)
		modelTriggerQueues.byDB[job.dbName] = queue
		for range modelTriggerWorkersPerDB {
			go func() {
				for queued := range queue {
					runModelTriggerJobSafely(queued)
				}
			}()
		}
	}
	modelTriggerQueues.mu.Unlock()
	select {
	case queue <- job:
		return true
	default:
		log.Errorf("workflow: model trigger %d dropped in %s: run queue full (%d)", job.trigger.ID, job.dbName, modelTriggerQueuePerDB)
		key := triggerKey(job.dbName, job.trigger.ID)
		triggerHealth.deliveryStart(key)
		triggerHealth.deliveryEnd(key, fmt.Errorf("workflow: model trigger run queue full (%d); event dropped", modelTriggerQueuePerDB))
		return false
	}
}

// modelTriggerPayload wraps the record snapshot with event metadata so
// downstream nodes (parse_payload, condition, transform_data) can branch on
// which model/operation fired without guessing from record shape alone.
func modelTriggerPayload(event, modelName, id string, record orm.RecordMap) []byte {
	payload := map[string]any{
		"event":  event,
		"model":  modelName,
		"id":     id,
		"record": record,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		log.Errorf("workflow: model trigger payload marshal failed: %v", err)
		return []byte("{}")
	}
	return encoded
}
