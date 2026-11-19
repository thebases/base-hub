package engine

// File overview:
// - Responsibility: owns `workflow` module trigger registry orchestration, where Kafka and MQTT subscriptions are registered once and kept aligned with persisted workflow trigger rows.
// - Key symbols: `DefaultTriggerRegistry`, `workflowTriggerRegistry`, `Register`, `Sync`, `SyncAllDatabases`.
// - Tenancy: trigger rows live in every tenant database, so a registration is keyed by
//   (logical db name, trigger id) and executes in a sudo environment bound to that database —
//   never on whatever handle context.Background() falls back to.
// - MQTT fan-out: one broker subscription per topic; each message runs every registered
//   (db, trigger) for that topic in turn. Workflows decide for themselves whether a message is
//   theirs (e.g. edc checks the device serial against its own tbl_edc_device).
// - MQTT boot order: module bootstrap runs before the kernel MQTT client connects, so the
//   subscribe is retried in the background instead of failing registration (which used to make
//   the boot-time Sync fatal whenever an active mqtt trigger existed and the broker was not up).
// - Pattern: Threads `context.Context` through the main execution paths so cancellation, auth scope, and transaction state can follow the work end to end.
// - Pattern: Protects shared mutable state with explicit synchronization or worker coordination rather than relying on unsupervised goroutine side effects.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"git.thebase.vn/base_hub/internal/config"
	"git.thebase.vn/base_hub/internal/modelruntime"
	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/queries"
	"git.thebase.vn/base_hub/internal/services/log"
	mqttservice "git.thebase.vn/base_hub/internal/services/mqtt"
	"git.thebase.vn/base_hub/modules/base-workflow/models"
	"github.com/segmentio/kafka-go"
)

// mqttSubscribeRetry is how often a pending MQTT topic subscription is retried.
// A var so tests can shorten it.
var mqttSubscribeRetry = 5 * time.Second

// mqttTriggerQoS is the fixed QoS for every workflow MQTT trigger subscription.
const mqttTriggerQoS byte = 1

// mqttInboundQueueSize bounds the per-topic hand-off queue between the paho handler and the
// workflow worker.
const mqttInboundQueueSize = 1024

// mqttEnqueueWait is how long the paho handler waits for room in a full topic queue before shedding
// the frame. The handler runs on the client's single ordered router goroutine, so an unbounded wait
// lets one slow topic stall every other topic and delay all QoS 1 acks; a short bounded wait keeps
// the other topics flowing, and each shed frame is counted (`dropped`) and logged.
const mqttEnqueueWait = 250 * time.Millisecond

// mqttRunTimeout caps one workflow run triggered by an MQTT frame.
const mqttRunTimeout = 2 * time.Minute

// workflowModuleName gates SyncAllDatabases to databases that have the workflow module.
const workflowModuleName = "base-workflow"

type registeredTrigger struct {
	key         string
	dbName      string
	trigger     models.WorkflowTriggerRecord
	cancelKafka context.CancelFunc
	waitKafka   *sync.WaitGroup
	tcp         *tcpListener // set for tcp triggers
}

// mqttTopicSubscription is the single broker subscription shared by every trigger on one topic.
type mqttTopicSubscription struct {
	keys        map[string]struct{}
	queue       chan []byte
	cancel      context.CancelFunc
	unsubscribe func() error
	stats       mqttTopicStats
}

// mqttTopicStats are lock-free counters for one topic's hand-off queue and worker.
type mqttTopicStats struct {
	enqueued        atomic.Int64
	processed       atomic.Int64
	dropped         atomic.Int64
	failed          atomic.Int64
	runNanos        atomic.Int64 // sum over processed frames
	lastRunNanos    atomic.Int64
	maxRunNanos     atomic.Int64
	lastProcessedAt atomic.Int64
}

func (st *mqttTopicStats) recordRun(took time.Duration, failed bool) {
	n := took.Nanoseconds()
	st.processed.Add(1)
	st.runNanos.Add(n)
	st.lastRunNanos.Store(n)
	st.lastProcessedAt.Store(time.Now().Unix())
	if failed {
		st.failed.Add(1)
	}
	for {
		cur := st.maxRunNanos.Load()
		if n <= cur || st.maxRunNanos.CompareAndSwap(cur, n) {
			return
		}
	}
}

// mqttStats feeds the MQTT dashboard (mqttservice.Snapshot) with per-topic queue depth and run times.
func (r *workflowTriggerRegistry) mqttStats() map[string]mqttservice.TopicRuntimeStats {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]mqttservice.TopicRuntimeStats, len(r.mqttTopics))
	for topic, sub := range r.mqttTopics {
		processed := sub.stats.processed.Load()
		avg := 0.0
		if processed > 0 {
			avg = float64(sub.stats.runNanos.Load()) / float64(processed) / 1e6
		}
		out[topic] = mqttservice.TopicRuntimeStats{
			QueueDepth:      len(sub.queue),
			QueueCap:        cap(sub.queue),
			Enqueued:        sub.stats.enqueued.Load(),
			Processed:       processed,
			Dropped:         sub.stats.dropped.Load(),
			Failed:          sub.stats.failed.Load(),
			LastRunMs:       float64(sub.stats.lastRunNanos.Load()) / 1e6,
			AvgRunMs:        avg,
			MaxRunMs:        float64(sub.stats.maxRunNanos.Load()) / 1e6,
			LastProcessedAt: sub.stats.lastProcessedAt.Load(),
			Triggers:        len(sub.keys),
		}
	}
	return out
}

func init() { mqttservice.RegisterTopicStatsProvider(defaultTriggerRegistry.mqttStats) }

type workflowTriggerRegistry struct {
	mu         sync.Mutex
	registered map[string]registeredTrigger
	kafkaTopic map[string]string
	mqttTopics map[string]*mqttTopicSubscription
	tcpPorts   map[int]string // listen port -> trigger key
	// watch holds the databases (with the workflow module) whose trigger-change events this
	// instance follows, and the last service-bus row seen in each (plan T55).
	watch triggerWatchState
}

var (
	defaultTriggerRegistry = &workflowTriggerRegistry{
		registered: make(map[string]registeredTrigger),
		kafkaTopic: make(map[string]string),
		mqttTopics: make(map[string]*mqttTopicSubscription),
		tcpPorts:   make(map[int]string),
	}
	triggerRegistrySubscribe = mqttservice.Subscribe
	// triggerDBContext is swappable in tests.
	triggerDBContext = sudoContextForDB
)

// DefaultTriggerRegistry returns the process-global workflow trigger registry.
func DefaultTriggerRegistry() *workflowTriggerRegistry {
	return defaultTriggerRegistry
}

// triggerDBName is the canonical logical database of ctx (blank resolves to the default DB).
func triggerDBName(ctx context.Context) string {
	return orm.ResolveLogicalDBName(orm.EnvironmentFromContext(ctx).DBName())
}

func triggerKey(dbName string, triggerID int64) string {
	return fmt.Sprintf("%s|%d", dbName, triggerID)
}

func (r *workflowTriggerRegistry) Register(ctx context.Context, trigger models.WorkflowTriggerRecord) error {
	if r == nil {
		return fmt.Errorf("workflow: trigger registry is nil")
	}
	if trigger.ID <= 0 {
		return orm.InvalidParamsf("workflow: trigger id is required")
	}
	dbName := triggerDBName(ctx)
	key := triggerKey(dbName, trigger.ID)
	if !trigger.IsActive {
		// A deactivated row must also drop any registration left from when it was active.
		return r.unregisterKey(key)
	}
	if trigger.WorkflowID == nil || *trigger.WorkflowID <= 0 {
		return orm.InvalidParamsf("workflow: trigger workflow_id is required")
	}
	// A delivery to a workflow that cannot run (never published, template, inactive) fails with
	// ErrFlowNotFound, which a Kafka consumer never commits: the partition would stall on that
	// message forever, and an mqtt/tcp/model delivery would be consumed and dropped. Such a
	// trigger is not registered at all, so Kafka keeps its messages for when the workflow can run;
	// publish and workflow activation re-sync the workflow's triggers (SyncWorkflowFromWrite).
	// A failed read keeps whatever is registered: Sync retries.
	blocked, err := triggerWorkflowBlockedReason(ctx, *trigger.WorkflowID)
	if err != nil {
		return err
	}
	if blocked != "" {
		if err := r.unregisterKey(key); err != nil {
			return err
		}
		triggerHealth.recordError(key, errors.New(blocked))
		return nil
	}
	triggerHealth.clearBlockedReason(key)

	r.mu.Lock()
	var cleanups []func()
	existing, ok := r.registered[key]
	if ok && sameTriggerRegistration(existing.trigger, trigger) {
		// Reload of an unchanged row (bus event, periodic reconcile): keep the live consumer,
		// subscription or listener; only the stored row is refreshed.
		existing.trigger = trigger
		if existing.tcp != nil {
			existing.tcp.trigger.Store(&trigger)
		}
		r.registered[key] = existing
		r.mu.Unlock()
		return nil
	}
	if ok && sameTCPConfig(existing.trigger, trigger) && existing.tcp != nil {
		// Re-sync of an unchanged tcp config: swap the row in place so live connections survive.
		existing.trigger = trigger
		existing.tcp.trigger.Store(&trigger)
		r.registered[key] = existing
		r.mu.Unlock()
		return nil
	}
	if ok && sameMQTTTopic(existing.trigger, trigger) {
		// Re-sync of an unchanged mqtt topic: swap the row in place so the live broker
		// subscription is not torn down and re-created (a message in that gap would be lost).
		existing.trigger = trigger
		r.registered[key] = existing
		r.mu.Unlock()
		return nil
	}
	if ok {
		cleanup := r.unregisterLocked(existing)
		if existing.tcp != nil {
			// The new listener may need the very port the old one holds, so the old one must be
			// closed first. Shutdown waits for handlers, so it runs outside the lock.
			r.mu.Unlock()
			runCleanups([]func(){cleanup})
			r.mu.Lock()
		} else {
			cleanups = append(cleanups, cleanup)
		}
	}
	entry := registeredTrigger{key: key, dbName: dbName, trigger: trigger}
	err = r.registerLocked(&entry)
	r.mu.Unlock()
	if err != nil {
		// Subscribe/listen failure: surface it on the trigger's health row.
		triggerHealth.recordError(key, err)
	}
	// Broker/consumer teardown runs outside the lock: an MQTT unsubscribe waits for the broker
	// ack, which paho cannot deliver while a message handler is blocked on this mutex.
	runCleanups(cleanups)
	return err
}

// sameTriggerRegistration reports whether next would be registered exactly like previous: same
// type, topic, workflow and config. Registering it again would only tear down and recreate the
// same consumer (a Kafka reader restart, an MQTT resubscribe), so Register keeps the live one.
func sameTriggerRegistration(previous, next models.WorkflowTriggerRecord) bool {
	if strings.TrimSpace(previous.TriggerType) != strings.TrimSpace(next.TriggerType) ||
		strings.TrimSpace(previous.Topic) != strings.TrimSpace(next.Topic) ||
		previous.IsActive != next.IsActive ||
		int64PointerValue(previous.WorkflowID) != int64PointerValue(next.WorkflowID) {
		return false
	}
	before, errBefore := json.Marshal(previous.Config)
	after, errAfter := json.Marshal(next.Config)
	return errBefore == nil && errAfter == nil && bytes.Equal(before, after)
}

func int64PointerValue(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}

// sameMQTTTopic reports whether both rows are mqtt triggers on the same topic.
func sameMQTTTopic(previous, next models.WorkflowTriggerRecord) bool {
	return strings.TrimSpace(previous.TriggerType) == "mqtt" && strings.TrimSpace(next.TriggerType) == "mqtt" &&
		strings.TrimSpace(previous.Topic) == strings.TrimSpace(next.Topic)
}

// registerLocked wires one entry by trigger type; r.mu must be held.
func (r *workflowTriggerRegistry) registerLocked(entry *registeredTrigger) error {
	trigger := entry.trigger
	// listening is false when the transport cannot deliver yet (kafka without brokers, mqtt
	// disabled or not subscribed): health must then read registered=false with last_error
	// instead of "Listening". The mqtt subscribe goroutine marks it registered once it lands.
	listening := true
	switch strings.TrimSpace(trigger.TriggerType) {
	case "webhook":
	case "kafka":
		topic := strings.TrimSpace(trigger.Topic)
		if topic == "" {
			return orm.InvalidParamsf("workflow: kafka trigger topic is required")
		}
		if existingKey, exists := r.kafkaTopic[topic]; exists && existingKey != entry.key {
			return orm.Conflictf("workflow: kafka topic %s is already assigned to trigger %s", topic, existingKey)
		}
		if kafkaTopicOwnedByKernel(topic, config.Get().Kafka.Topics) {
			return orm.InvalidParamsf("workflow: kafka topic %s is consumed by the kernel consumer (kafka.topics); a trigger cannot share it", topic)
		}
		groupID := kafkaTriggerGroupID(entry.dbName, trigger)
		dbName := entry.dbName
		key := entry.key
		brokers := append([]string(nil), config.Get().Kafka.Brokers...)
		if len(brokers) == 0 {
			// startKafkaTriggerConsumer is a no-op without brokers: say so instead of looking healthy.
			triggerHealth.recordError(key, errors.New("workflow: no kafka brokers configured; trigger is not consuming"))
			listening = false
		}
		entry.cancelKafka, entry.waitKafka = startKafkaTriggerConsumer(
			context.Background(),
			brokers,
			topic,
			groupID,
			func(handlerCtx context.Context, msg kafka.Message) error {
				triggerHealth.deliveryStart(key)
				err := executeKafkaTriggerInDB(handlerCtx, dbName, groupID, trigger, msg)
				if handlerCtx.Err() == nil { // an unregister mid-delivery is not a trigger failure
					triggerHealth.deliveryEnd(key, err)
				}
				return err
			},
			func(err error) { triggerHealth.recordError(key, err) },
		)
		r.kafkaTopic[topic] = entry.key
	case "mqtt":
		topic := strings.TrimSpace(trigger.Topic)
		if topic == "" {
			return orm.InvalidParamsf("workflow: mqtt trigger topic is required")
		}
		sub, exists := r.mqttTopics[topic]
		if !exists {
			sub = &mqttTopicSubscription{keys: map[string]struct{}{}, queue: make(chan []byte, mqttInboundQueueSize)}
			r.mqttTopics[topic] = sub
			// QoS is pinned to 1 (at-least-once) for every MQTT subscription; a legacy
			// per-trigger "qos" config value is ignored.
			r.startMQTTSubscriptionLocked(topic, mqttTriggerQoS, sub)
		}
		sub.keys[entry.key] = struct{}{}
		if !config.Get().MqttClient.Enable {
			triggerHealth.recordError(entry.key, errors.New("workflow: mqtt disabled in config; topic is not subscribed"))
		}
		// sub.unsubscribe is set (under r.mu) only after a successful subscribe, so a trigger
		// joining an already-subscribed topic is listening at once; otherwise the background
		// subscribe marks every key of the topic registered when it succeeds.
		listening = sub.unsubscribe != nil
	case "model":
		if err := registerModelTrigger(entry.dbName, entry.key, trigger); err != nil {
			return err
		}
	case "tcp":
		listener, port, err := r.startTCPTriggerLocked(entry)
		if err != nil {
			return err
		}
		entry.tcp = listener
		if r.tcpPorts == nil {
			r.tcpPorts = make(map[int]string)
		}
		r.tcpPorts[port] = entry.key
	default:
		return orm.InvalidParamsf("workflow: unsupported trigger type %s", strings.TrimSpace(trigger.TriggerType))
	}
	r.registered[entry.key] = *entry
	if listening {
		triggerHealth.markRegistered(entry.key)
	} else {
		triggerHealth.markUnregistered(entry.key)
	}
	return nil
}

// startMQTTSubscriptionLocked subscribes topic in the background until it succeeds or the topic's
// last trigger is removed; r.mu must be held. A subscription that lands after cancel is undone.
func (r *workflowTriggerRegistry) startMQTTSubscriptionLocked(topic string, qos byte, sub *mqttTopicSubscription) {
	if !config.Get().MqttClient.Enable {
		log.Warnf("workflow: mqtt disabled in config; topic %s registered but not subscribed", topic)
		sub.cancel = func() {}
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	sub.cancel = cancel
	queue := sub.queue
	// The paho handler only enqueues: it runs on the client's ordered router goroutine, so running
	// workflows there would delay every QoS 1 ack behind slow steps and deadlock any step that
	// publishes and waits for its own ack. One worker per topic keeps per-device order.
	go r.runMQTTWorker(ctx, topic, queue)
	go func() {
		for attempt := 1; ; attempt++ {
			unsubscribe, err := triggerRegistrySubscribe(topic, qos, func(payload []byte) {
				select {
				case queue <- payload:
					sub.stats.enqueued.Add(1)
					return
				default:
				}
				timer := time.NewTimer(mqttEnqueueWait)
				defer timer.Stop()
				select {
				case queue <- payload:
					sub.stats.enqueued.Add(1)
				case <-ctx.Done():
				case <-timer.C:
					// Shed instead of stalling the shared router; log the first and every 100th.
					if n := sub.stats.dropped.Add(1); n == 1 || n%100 == 0 {
						log.Errorf("workflow: mqtt topic %s inbound queue full (%d) for %s; frame dropped (%d dropped so far)", topic, cap(queue), mqttEnqueueWait, n)
					}
				}
			})
			if err == nil {
				r.mu.Lock()
				cancelled := ctx.Err() != nil
				keys := mqttSubscriptionKeys(sub)
				if !cancelled {
					sub.unsubscribe = unsubscribe
				}
				r.mu.Unlock()
				if cancelled {
					runCleanups([]func(){func() { logUnsubscribe(topic, unsubscribe) }})
					return
				}
				for _, key := range keys {
					triggerHealth.clearError(key)
					triggerHealth.markRegistered(key)
				}
				log.Infof("workflow: mqtt trigger topic %s subscribed", topic)
				return
			}
			r.mu.Lock()
			keys := mqttSubscriptionKeys(sub)
			r.mu.Unlock()
			for _, key := range keys {
				triggerHealth.recordError(key, fmt.Errorf("workflow: mqtt subscribe %s failed: %w", topic, err))
			}
			// The first attempts at boot always miss: the kernel client connects after module bootstrap.
			if attempt%6 == 0 {
				log.Warnf("workflow: mqtt trigger topic %s not subscribed yet (attempt %d): %v", topic, attempt, err)
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(mqttSubscribeRetry):
			}
		}
	}()
}

// mqttSubscriptionKeys snapshots the trigger keys sharing sub; r.mu must be held.
func mqttSubscriptionKeys(sub *mqttTopicSubscription) []string {
	keys := make([]string, 0, len(sub.keys))
	for key := range sub.keys {
		keys = append(keys, key)
	}
	return keys
}

// runMQTTWorker drains one topic's queue in arrival order until the topic's last trigger is removed.
func (r *workflowTriggerRegistry) runMQTTWorker(ctx context.Context, topic string, queue <-chan []byte) {
	for {
		select {
		case <-ctx.Done():
			return
		case payload := <-queue:
			r.dispatchMQTT(topic, payload)
		}
	}
}

// dispatchMQTT runs every trigger registered on topic, each in its own database. Entries are
// snapshotted under the lock and executed outside it, in key order, one after another, so per-
// device message order is kept inside each tenant.
func (r *workflowTriggerRegistry) dispatchMQTT(topic string, payload []byte) {
	r.mu.Lock()
	sub := r.mqttTopics[topic]
	entries := make([]registeredTrigger, 0)
	if sub != nil {
		for key := range sub.keys {
			if entry, ok := r.registered[key]; ok {
				entries = append(entries, entry)
			}
		}
	}
	r.mu.Unlock()
	sort.Slice(entries, func(i, j int) bool { return entries[i].key < entries[j].key })
	// One line per frame so "device published but nothing happened" can be told apart from
	// "frame never arrived" and "no trigger registered for the topic".
	if len(entries) == 0 {
		log.Warnf("workflow: mqtt frame on %s (%d bytes) dropped: no active trigger registered", topic, len(payload))
		return
	}
	log.Infof("workflow: mqtt frame on %s (%d bytes) -> %d trigger(s)", topic, len(payload), len(entries))
	// One processed sample per frame (all tenants' triggers included), so avg/max run time is the
	// time the topic's single worker was busy with it, which is what bounds topic throughput.
	frameStarted := time.Now()
	frameFailed := false
	if sub != nil {
		defer func() { sub.stats.recordRun(time.Since(frameStarted), frameFailed) }()
	}
	for _, entry := range entries {
		started := time.Now()
		triggerHealth.deliveryStart(entry.key)
		err := runMQTTEntry(entry, payload)
		triggerHealth.deliveryEnd(entry.key, err)
		if isRunRetryScheduled(err) {
			log.Infof("workflow: mqtt trigger %s: %v", entry.key, err)
			continue
		}
		if err != nil {
			frameFailed = true
			log.Errorf("workflow: mqtt trigger %s execution failed after %s: %v", entry.key, time.Since(started), err)
			continue
		}
		log.Debugf("workflow: mqtt trigger %s done in %s", entry.key, time.Since(started))
	}
}

// runMQTTEntry runs one trigger for one frame. A panic is turned into that trigger's failure
// (logged with its stack) so it can neither kill the topic's only worker nor skip the remaining
// triggers of the frame.
func runMQTTEntry(entry registeredTrigger, payload []byte) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			log.Errorf("workflow: mqtt trigger %s panicked: %v\n%s", entry.key, recovered, debug.Stack())
			err = fmt.Errorf("workflow: mqtt trigger %s panicked: %v", entry.key, recovered)
		}
	}()
	// Bound one run so a hung step cannot stall the topic's single worker and back up the queue.
	runCtx, cancel := context.WithTimeout(context.Background(), mqttRunTimeout)
	defer cancel()
	return executeMQTTTriggerInDB(runCtx, entry.dbName, entry.trigger, payload)
}

// executeMQTTTriggerInDB runs one MQTT frame in the trigger's database. Without a dedupe_key every
// frame is a new run (a QoS 1 redelivery included). With one, the run is named after the value at
// that path of the payload (plan T56) and a frame whose run already exists is skipped as handled.
func executeMQTTTriggerInDB(parent context.Context, dbName string, trigger models.WorkflowTriggerRecord, payload []byte) error {
	ctx, err := triggerDBContext(parent, dbName)
	if err != nil {
		return err
	}
	if name, ok := triggerDedupeRunName(trigger, dbName, payload); ok {
		exists, err := workflowRunNameExists(ctx, name)
		if err != nil {
			return fmt.Errorf("workflow: check mqtt delivery run %s: %w", name, err)
		}
		if exists {
			log.Infof("workflow: mqtt delivery already ran, skipping (run=%s)", name)
			return nil
		}
		ctx = models.ContextWithWorkflowRequestID(ctx, name)
	}
	return executeWorkflowTrigger(ctx, trigger, payload)
}

// triggerDedupeKeyConfig is the trigger config key naming the payload field that identifies a
// message (plan T56), e.g. "message_id" or "data.txn_id" (a leading "$." is accepted).
const triggerDedupeKeyConfig = "dedupe_key"

// maxDedupeValueLen bounds the payload value copied into a run name; longer values are hashed.
const maxDedupeValueLen = 128

// triggerDedupeRunName returns the run name `<type>:<db>:<topic>:<value>` for a payload whose
// dedupe_key path holds a scalar, and false when the trigger has no dedupe_key or the payload has
// no usable value there (the delivery then runs under its usual name). Values longer than
// maxDedupeValueLen become `sha256:<hex>` so a sender cannot grow the unique run name column.
func triggerDedupeRunName(trigger models.WorkflowTriggerRecord, dbName string, payload []byte) (string, bool) {
	path := strings.TrimPrefix(strings.TrimPrefix(configString(trigger.Config, triggerDedupeKeyConfig), "$"), ".")
	if path == "" {
		return "", false
	}
	var decoded any
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	if err := decoder.Decode(&decoded); err != nil {
		return "", false
	}
	current := decoded
	for _, segment := range strings.Split(path, ".") {
		object, ok := current.(map[string]any)
		if !ok || segment == "" {
			return "", false
		}
		current = object[segment]
	}
	var value string
	switch typed := current.(type) {
	case string:
		value = strings.TrimSpace(typed)
	case json.Number:
		value = typed.String()
	case bool:
		value = strconv.FormatBool(typed)
	default:
		return "", false
	}
	if value == "" {
		return "", false
	}
	if len(value) > maxDedupeValueLen {
		sum := sha256.Sum256([]byte(value))
		value = "sha256:" + hex.EncodeToString(sum[:])
	}
	return fmt.Sprintf("%s:%s:%s:%s", strings.TrimSpace(trigger.TriggerType), dbName, strings.TrimSpace(trigger.Topic), value), true
}

// Unregister removes the trigger with triggerID in ctx's database.
func (r *workflowTriggerRegistry) Unregister(ctx context.Context, triggerID int64) error {
	if r == nil || triggerID <= 0 {
		return nil
	}
	return r.unregisterKey(triggerKey(triggerDBName(ctx), triggerID))
}

func (r *workflowTriggerRegistry) unregisterKey(key string) error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	entry, ok := r.registered[key]
	var cleanup func()
	if ok {
		cleanup = r.unregisterLocked(entry)
	}
	r.mu.Unlock()
	runCleanups([]func(){cleanup})
	return nil
}

// loadActiveTriggerRows reads every active trigger row of ctx's database; swappable in tests.
var loadActiveTriggerRows = func(ctx context.Context) ([]orm.RecordMap, error) {
	return modelruntime.NewService().SearchRead(ctx, models.WorkflowTriggerModelName, orm.RecordMap{
		"is_active": true,
		"__order":   "id asc",
	}, nil)
}

// Sync reconciles the registry with ctx's database (plan T55): every active trigger row is
// registered (an unchanged one keeps its live consumer, see sameTriggerRegistration), and every
// registration of this database whose row is no longer active, or no longer exists, is removed.
// Before T55 Sync only added, so a trigger deleted or deactivated through another instance stayed
// subscribed here until a restart. A failed read removes nothing.
func (r *workflowTriggerRegistry) Sync(ctx context.Context) error {
	if r == nil {
		return fmt.Errorf("workflow: trigger registry is nil")
	}
	rows, err := loadActiveTriggerRows(ctx)
	if err != nil {
		return err
	}
	dbName := triggerDBName(ctx)
	keep := make(map[string]struct{}, len(rows))
	// One undecodable or unregistrable row is logged and skipped: aborting here would leave every
	// other trigger of the database unsubscribed after a restart. Its key stays in keep, so a
	// registration made from an earlier good version of the row is not torn down by a bad read.
	for _, row := range rows {
		if id := modelTriggerRowID(row["id"]); id > 0 {
			keep[triggerKey(dbName, id)] = struct{}{}
		}
		trigger, err := decodeRecord[models.WorkflowTriggerRecord](row)
		if err != nil {
			log.Errorf("workflow: trigger row id=%v skipped: %v", row["id"], err)
			continue
		}
		if err := r.Register(ctx, *trigger); err != nil {
			log.Errorf("workflow: trigger %d not registered: %v", trigger.ID, err)
		}
	}
	for _, key := range r.keysForDB(dbName) {
		if _, ok := keep[key]; ok {
			continue
		}
		log.Infof("workflow: trigger %s is no longer active; unregistered", key)
		if err := r.unregisterKey(key); err != nil {
			log.Errorf("workflow: unregister trigger %s: %v", key, err)
		}
	}
	return nil
}

// keysForDB lists the registry keys of one database.
func (r *workflowTriggerRegistry) keysForDB(dbName string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	keys := make([]string, 0)
	for key, entry := range r.registered {
		if entry.dbName == dbName {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

// modelTriggerRowID reads a row id from the shapes a runtime read returns.
func modelTriggerRowID(raw any) int64 {
	switch typed := raw.(type) {
	case int64:
		return typed
	case int:
		return int64(typed)
	case float64:
		return int64(typed)
	case json.Number:
		value, _ := typed.Int64()
		return value
	case string:
		value, _ := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		return value
	default:
		return 0
	}
}

// SyncAllDatabases runs Sync once per registered database that has the workflow module. One
// tenant's failure is logged and skipped so it can never block the others (or the boot).
func (r *workflowTriggerRegistry) SyncAllDatabases(parent context.Context) {
	names := orm.RegisteredDBNames()
	if len(names) == 0 {
		names = []string{orm.ResolveLogicalDBName("")}
	}
	for _, name := range names {
		ctx, err := triggerDBContext(parent, name)
		if err != nil {
			log.Warnf("workflow: trigger sync skipped db=%s: %v", name, err)
			continue
		}
		if !workflowInstalled(ctx, name) {
			continue
		}
		r.watchDatabase(name)
		if err := r.Sync(ctx); err != nil {
			if isTriggerSchemaLag(err) {
				log.Warnf("workflow: trigger registry sync skipped db=%s (schema not migrated yet): %v", name, err)
				continue
			}
			log.Errorf("workflow: trigger registry sync failed db=%s: %v", name, err)
		}
	}
}

// workflowInstalled reports whether db has base-workflow installed; a lookup error (fresh DB
// without the module table yet) counts as not installed.
func workflowInstalled(ctx context.Context, dbName string) bool {
	registered := orm.ResolveRegisteredDatabase(dbName)
	if registered == nil {
		return true
	}
	installed, err := queries.ModuleInstalled(ctx, registered.Pool, workflowModuleName)
	return err == nil && installed
}

// isTriggerSchemaLag matches the errors that mean the trigger table is not in its final migrated
// shape yet (fresh install racing auto-migrate, or a column added by code not migrated yet —
// SQLSTATE 42703). Those self-heal on the post-migration sync.
func isTriggerSchemaLag(err error) bool {
	errMsg := err.Error()
	return strings.Contains(errMsg, `model "base.workflow.trigger" is not registered`) ||
		strings.Contains(errMsg, `relation "base_workflow_trigger" does not exist`) ||
		strings.Contains(errMsg, `BunRepository is not initialized`) ||
		strings.Contains(errMsg, `BunDB is not initialized`) ||
		strings.Contains(errMsg, `SQLSTATE 42703`)
}

func (r *workflowTriggerRegistry) Shutdown() {
	if r == nil {
		return
	}
	r.mu.Lock()
	cleanups := make([]func(), 0, len(r.registered))
	for _, entry := range r.registered {
		cleanups = append(cleanups, r.unregisterLocked(entry))
	}
	r.registered = make(map[string]registeredTrigger)
	r.kafkaTopic = make(map[string]string)
	r.mqttTopics = make(map[string]*mqttTopicSubscription)
	r.tcpPorts = make(map[int]string)
	r.mu.Unlock()
	runCleanups(cleanups)
}

// unregisterLocked drops entry from the maps (r.mu held) and returns the blocking teardown
// (Kafka wait, MQTT unsubscribe) for the caller to run after releasing the lock.
func (r *workflowTriggerRegistry) unregisterLocked(entry registeredTrigger) func() {
	delete(r.registered, entry.key)
	triggerHealth.markUnregistered(entry.key)
	switch strings.TrimSpace(entry.trigger.TriggerType) {
	case "kafka":
		delete(r.kafkaTopic, strings.TrimSpace(entry.trigger.Topic))
		if entry.cancelKafka != nil {
			entry.cancelKafka()
		}
		if entry.waitKafka != nil {
			return entry.waitKafka.Wait
		}
	case "mqtt":
		topic := strings.TrimSpace(entry.trigger.Topic)
		sub := r.mqttTopics[topic]
		if sub == nil {
			return nil
		}
		delete(sub.keys, entry.key)
		if len(sub.keys) > 0 {
			return nil
		}
		// Last trigger on the topic: stop a pending subscribe and drop the live one.
		delete(r.mqttTopics, topic)
		if sub.cancel != nil {
			sub.cancel()
		}
		if unsubscribe := sub.unsubscribe; unsubscribe != nil {
			return func() { logUnsubscribe(topic, unsubscribe) }
		}
	case "model":
		unregisterModelTrigger(configString(entry.trigger.Config, "model"), entry.key)
	case "tcp":
		if entry.tcp != nil {
			if r.tcpPorts[entry.tcp.port] == entry.key {
				delete(r.tcpPorts, entry.tcp.port)
			}
			return entry.tcp.shutdown
		}
	}
	return nil
}

func logUnsubscribe(topic string, unsubscribe func() error) {
	if err := unsubscribe(); err != nil {
		log.Errorf("workflow: mqtt topic %s unsubscribe failed: %v", topic, err)
	}
}

func runCleanups(cleanups []func()) {
	for _, cleanup := range cleanups {
		if cleanup != nil {
			cleanup()
		}
	}
}

// sudoContextForDB binds a sudo environment for dbName (same shape as the kernel cron's per-DB
// job context). NoCreate: a trigger can never cause a database to be created.
func sudoContextForDB(parent context.Context, dbName string) (context.Context, error) {
	registered, err := orm.EnsureNamedBunDBNoCreate(dbName)
	if err != nil {
		return nil, err
	}
	if registered == nil || registered.DB == nil {
		return nil, fmt.Errorf("workflow: database %q is not available", dbName)
	}
	return orm.ContextWithEnvironment(parent, orm.NewEnvironmentWithDBName(registered.DB, dbName, 0, nil, true)), nil
}

// executeTriggerInDB runs one trigger delivery inside its own database.
func executeTriggerInDB(parent context.Context, dbName string, trigger models.WorkflowTriggerRecord, input []byte) error {
	ctx, err := triggerDBContext(parent, dbName)
	if err != nil {
		return err
	}
	return executeWorkflowTrigger(ctx, trigger, input)
}

// kafkaDeliveryRunName is the deterministic run name (request id) for one Kafka
// delivery. base.workflow.run.name is unique, so a redelivered offset can never
// create a second run.
func kafkaDeliveryRunName(dbName, groupID string, msg kafka.Message) string {
	return fmt.Sprintf("kafka:%s:%s:%s:%d:%d", dbName, groupID, msg.Topic, msg.Partition, msg.Offset)
}

// kafkaTriggerGroupID is the trigger's consumer group: config.consumer_group, else
// `wf-<db>-<trigger id>` (plan T54), so a trigger never shares a group, and therefore offsets
// and partition assignments, with the kernel IPN consumer or another trigger.
func kafkaTriggerGroupID(dbName string, trigger models.WorkflowTriggerRecord) string {
	if group := strings.TrimSpace(configString(trigger.Config, "consumer_group")); group != "" {
		return group
	}
	return fmt.Sprintf("wf-%s-%d", dbName, trigger.ID)
}

// kafkaTopicOwnedByKernel reports whether topic is one of the kernel consumer's topics
// (config kafka.topics); a workflow trigger on it would compete with that consumer.
func kafkaTopicOwnedByKernel(topic string, kernelTopics []string) bool {
	for _, kernelTopic := range kernelTopics {
		if strings.TrimSpace(kernelTopic) == topic {
			return true
		}
	}
	return false
}

// workflowRunNameExists reports whether a run with this request id was already
// created. It is a package var so tests can stub the database lookup.
var workflowRunNameExists = func(ctx context.Context, name string) (bool, error) {
	count, err := modelruntime.NewService().SearchCount(ctx, models.WorkflowRunModelName, orm.RecordMap{"name": name})
	return count > 0, err
}

// executeKafkaTriggerInDB runs one Kafka delivery at most once per run name: the run is named
// after topic/partition/offset (or after the trigger's dedupe_key value, plan T56), and a delivery
// whose run already exists is treated as success so a redelivery is not executed again (the
// earlier run row keeps its own success/failure state). nil tells the consumer to commit.
func executeKafkaTriggerInDB(parent context.Context, dbName, groupID string, trigger models.WorkflowTriggerRecord, msg kafka.Message) error {
	ctx, err := triggerDBContext(parent, dbName)
	if err != nil {
		return err
	}
	name, deduped := triggerDedupeRunName(trigger, dbName, msg.Value)
	if !deduped {
		name = kafkaDeliveryRunName(dbName, groupID, msg)
	}
	exists, err := workflowRunNameExists(ctx, name)
	if err != nil {
		return fmt.Errorf("workflow: check kafka delivery run %s: %w", name, err)
	}
	if exists {
		log.Warnf("workflow: kafka delivery already ran, skipping (run=%s partition=%d offset=%d)", name, msg.Partition, msg.Offset)
		return nil
	}
	err = executeWorkflowTrigger(models.ContextWithWorkflowRequestID(ctx, name), trigger, msg.Value)
	return kafkaDeliveryOutcome(ctx, name, err)
}

// kafkaDeliveryOutcome decides whether a handled Kafka message may be committed (nil) or must be
// retried (error). Commit only after success, a scheduled retry (the run is stored and
// resume_due_runs owns it) or a failure that left a run row behind (the failure is audited there
// and replaying the message would only hit the existing name). A failure before any run row
// exists (database down, workflow not loadable) returns the error: the consumer backs off and
// handles the same message again instead of losing it (plan T54).
func kafkaDeliveryOutcome(ctx context.Context, name string, execErr error) error {
	if execErr == nil {
		return nil
	}
	if isRunRetryScheduled(execErr) {
		log.Infof("workflow: kafka delivery %s: %v", name, execErr)
		return nil
	}
	exists, err := workflowRunNameExists(ctx, name)
	if err != nil {
		return fmt.Errorf("%w (and checking run %s failed: %v)", execErr, name, err)
	}
	if exists {
		log.Errorf("workflow: kafka delivery %s failed; recorded on its run: %v", name, execErr)
		return nil
	}
	return execErr
}

func executeWorkflowTrigger(ctx context.Context, trigger models.WorkflowTriggerRecord, input []byte) error {
	if trigger.WorkflowID == nil || *trigger.WorkflowID <= 0 {
		return orm.InvalidParamsf("workflow: trigger workflow_id is required")
	}
	ctx = models.ContextWithWorkflowTriggerType(ctx, strings.TrimSpace(trigger.TriggerType))
	ctx = models.ContextWithWorkflowAuditMode(ctx, models.WorkflowAuditModeFromTriggerConfig(trigger.TriggerType, trigger.Config))
	// Webhook triggers bind a request ID at the HTTP route entry (workflow_route.go)
	// before execution; Kafka/MQTT triggers have no such entry point, so bind one
	// here. Without it, base.workflow.run.name (the canonical correlation ID reused
	// across retries/resume) silently falls back to a fresh, unrelated UUID on
	// every step write instead of staying pinned to this run's lifecycle.
	ctx, _ = models.EnsureWorkflowRequestID(ctx)
	flow, err := loadActiveFlowByID(ctx, *trigger.WorkflowID)
	if err != nil {
		return err
	}
	svc := NewService().withDefaults()
	// Deliveries run the published snapshot (plan T97); the trigger row itself is live transport config.
	runtime, err := svc.publishedRuntime(ctx, flow)
	if err != nil {
		return err
	}
	return svc.executeFlow(ctx, runtime, input)
}

func startKafkaTriggerConsumer(
	parent context.Context,
	brokers []string,
	topic string,
	groupID string,
	handler func(context.Context, kafka.Message) error,
	onReadErr func(error),
) (context.CancelFunc, *sync.WaitGroup) {
	ctx, cancel := context.WithCancel(parent)
	waitGroup := &sync.WaitGroup{}
	if len(brokers) == 0 || topic == "" || handler == nil {
		return cancel, waitGroup
	}

	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers:         brokers,
		GroupID:         groupID,
		Topic:           topic,
		MinBytes:        10e3,
		MaxBytes:        10e6,
		MaxWait:         time.Second,
		CommitInterval:  time.Second,
		ReadLagInterval: -1,
		// A group without committed offsets (every trigger after the T54 switch to its own
		// group) starts at the end of the topic: starting at the beginning would replay the
		// whole retention under new run names, i.e. execute old messages a second time.
		StartOffset: kafka.LastOffset,
	})

	waitGroup.Add(1)
	go func() {
		defer waitGroup.Done()
		defer func() {
			if err := reader.Close(); err != nil {
				log.Errorf("workflow: kafka trigger reader close topic=%s: %v", topic, err)
			}
		}()

		kafkaTriggerLoop(ctx, reader, topic, handler, onReadErr)
	}()
	return cancel, waitGroup
}

// kafkaTriggerReader is the slice of *kafka.Reader the trigger loop needs.
type kafkaTriggerReader interface {
	FetchMessage(ctx context.Context) (kafka.Message, error)
	CommitMessages(ctx context.Context, msgs ...kafka.Message) error
}

// Backoff between attempts at one message whose handling failed before a run row existed.
var (
	kafkaHandleBackoffMin = 500 * time.Millisecond
	kafkaHandleBackoffMax = 30 * time.Second
)

// kafkaTriggerLoop fetches a message, handles it, and commits its offset only once the handler
// accepts it (see kafkaDeliveryOutcome). A message the handler rejects is not committed and not
// skipped: offsets are cumulative per partition, so moving on to the next message and committing
// it would commit this one too. The same message is handled again after an exponential backoff
// (capped at kafkaHandleBackoffMax) until it is accepted or the consumer stops; that is the
// "refetch" of plan T54 without recreating the reader. A shutdown leaves the offset uncommitted
// so the message is redelivered. The content-hash deduper is gone: the unique run name already
// stops a redelivered offset, and the hash dropped distinct messages with identical bodies.
func kafkaTriggerLoop(ctx context.Context, reader kafkaTriggerReader, topic string, handler func(context.Context, kafka.Message) error, onReadErr func(error)) {
	backoff := 500 * time.Millisecond
	for {
		msg, err := reader.FetchMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Errorf("workflow: kafka trigger read topic=%s: %v", topic, err)
			if onReadErr != nil {
				onReadErr(err)
			}
			if !sleepCtx(ctx, backoff) {
				return
			}
			if backoff < 10*time.Second {
				backoff *= 2
			}
			continue
		}
		backoff = 500 * time.Millisecond

		if !handleKafkaTriggerMessage(ctx, topic, handler, msg) {
			return
		}
		commitKafkaTriggerMessage(ctx, reader, topic, msg)
	}
}

// handleKafkaTriggerMessage runs handler on msg until it accepts it (true) or ctx ends (false).
func handleKafkaTriggerMessage(ctx context.Context, topic string, handler func(context.Context, kafka.Message) error, msg kafka.Message) bool {
	delay := kafkaHandleBackoffMin
	for attempt := 1; ; attempt++ {
		err := safeKafkaTriggerHandle(ctx, handler, msg)
		if err == nil {
			return true
		}
		if ctx.Err() != nil {
			return false
		}
		log.Errorf("workflow: kafka trigger %s handler error (partition=%d offset=%d attempt=%d, not committed, retry in %s): %v", topic, msg.Partition, msg.Offset, attempt, delay, err)
		if !sleepCtx(ctx, delay) {
			return false
		}
		delay *= 2
		if delay > kafkaHandleBackoffMax {
			delay = kafkaHandleBackoffMax
		}
	}
}

// sleepCtx waits d or until ctx ends; false when ctx ended.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func commitKafkaTriggerMessage(ctx context.Context, reader kafkaTriggerReader, topic string, msg kafka.Message) {
	if err := reader.CommitMessages(ctx, msg); err != nil && ctx.Err() == nil {
		log.Errorf("workflow: kafka trigger %s commit offset=%d: %v", topic, msg.Offset, err)
	}
}

func safeKafkaTriggerHandle(ctx context.Context, handler func(context.Context, kafka.Message) error, msg kafka.Message) (err error) {
	defer func() {
		if r := recover(); r != nil {
			log.Errorf("workflow: kafka trigger handler panic: %v\n%s", r, debug.Stack())
			err = fmt.Errorf("handler panic: %v", r)
		}
	}()
	return handler(ctx, msg)
}

// readTriggerFlowRows reads one workflow row for a trigger delivery; swappable in tests.
var readTriggerFlowRows = func(ctx context.Context, workflowID int64) ([]orm.RecordMap, error) {
	return modelruntime.NewService().Read(ctx, models.FlowModelName, []string{fmt.Sprint(workflowID)}, nil)
}

// Trigger health last_error texts of a trigger whose workflow cannot run (Register skipped it).
const (
	triggerBlockedNotFound     = "workflow not found"
	triggerBlockedInactive     = "workflow is inactive"
	triggerBlockedTemplate     = "workflow is a template: instantiate it first"
	triggerBlockedNotPublished = "workflow not published"
)

// triggerWorkflowBlockedReason is workflowBlockedReason; swappable in tests.
var triggerWorkflowBlockedReason = workflowBlockedReason

// workflowBlockedReason reports why deliveries to workflowID would not run ("" = they would),
// mirroring loadActiveFlowByID + publishedRuntime.
func workflowBlockedReason(ctx context.Context, workflowID int64) (string, error) {
	rows, err := readTriggerFlowRows(ctx, workflowID)
	if err != nil {
		return "", fmt.Errorf("workflow: load flow id=%d: %w", workflowID, err)
	}
	if len(rows) == 0 {
		return triggerBlockedNotFound, nil
	}
	delete(rows[0], "action_ids")
	flow, err := decodeRecord[models.FlowRecord](rows[0])
	if err != nil {
		return "", err
	}
	switch {
	case flow.IsTemplate:
		return triggerBlockedTemplate, nil
	case !flow.IsActive:
		return triggerBlockedInactive, nil
	case len(flow.PublishedSnapshot) == 0 && strings.TrimSpace(flow.State) == models.WorkflowStateDraft:
		return triggerBlockedNotPublished, nil
	}
	return "", nil
}

func loadActiveFlowByID(ctx context.Context, workflowID int64) (*models.FlowRecord, error) {
	rows, err := readTriggerFlowRows(ctx, workflowID)
	if err != nil {
		log.Errorf("workflow: load flow id=%d: %v", workflowID, err)
		return nil, fmt.Errorf("workflow: load flow id=%d: %w", workflowID, err)
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("%w: workflow_id=%d", ErrFlowNotFound, workflowID)
	}
	delete(rows[0], "action_ids")
	flow, err := decodeRecord[models.FlowRecord](rows[0])
	if err != nil {
		return nil, err
	}
	// Every trigger delivery (kafka, mqtt, tcp, model) loads its flow here, so this is the one place
	// a gallery template (plan T101) is refused: it never runs, even if someone activates it.
	if !flow.IsActive || flow.IsTemplate {
		return nil, fmt.Errorf("%w: workflow_id=%d", ErrFlowNotFound, workflowID)
	}
	return flow, nil
}

func configString(values map[string]any, key string) string {
	if values == nil {
		return ""
	}
	if value, ok := values[key].(string); ok {
		return strings.TrimSpace(value)
	}
	return ""
}

func configBool(values map[string]any, key string) bool {
	if values == nil {
		return false
	}
	if value, ok := values[key].(bool); ok {
		return value
	}
	return false
}

func configInt(values map[string]any, key string, fallback int64) int64 {
	if values == nil {
		return fallback
	}
	switch typed := values[key].(type) {
	case int64:
		return typed
	case int:
		return int64(typed)
	case float64:
		return int64(typed)
	case string:
		if value, err := strconv.ParseInt(strings.TrimSpace(typed), 10, 64); err == nil {
			return value
		}
	}
	return fallback
}
