package orm

// File overview:
// - Responsibility: implements the metadata-driven ORM/runtime kernel for runtime service, where storage, runtime dispatch, and security rules stay centralized.
// - Key symbols: `RegisterRuntimeExecutor`, `lookupRuntimeExecutor`, `runtimeExecutor`, `AsyncModelTask`.
// - Pattern: Leans on metadata-driven registration so storage mapping, runtime methods, and behavior hooks stay centralized instead of being scattered across handlers.
// - Pattern: Threads `context.Context` through the main execution paths so cancellation, auth scope, and transaction state can follow the work end to end.

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"git.thebase.vn/base_hub/internal/services/log"
	"git.thebase.vn/base_hub/internal/worker"
)

type runtimeExecutor interface {
	Metadata() *ModelMetadata
	ExecuteAction(ctx context.Context, action string, filter RecordMap, payload any) (any, error)
	RuntimeSearch(ctx context.Context, filter RecordMap) ([]any, error)
	RuntimeSearchCount(ctx context.Context, filter RecordMap) (int64, error)
	RuntimeRead(ctx context.Context, ids []string, fields []string) ([]RecordMap, error)
	RuntimeSearchRead(ctx context.Context, filter RecordMap, fields []string) ([]RecordMap, error)
	RuntimeCreate(ctx context.Context, values RecordMap) (RecordMap, error)
	RuntimeWrite(ctx context.Context, ids []string, values RecordMap) (bool, error)
	RuntimeUnlink(ctx context.Context, ids []string) (bool, error)
}

type AsyncModelTask struct {
	ModelName string
	Action    string
	Filter    RecordMap
	Payload   any
}

type SharedWorkerConfig struct {
	WorkerCount  int
	JobQueueSize int
}

var runtimeExecutorRegistry = struct {
	mu        sync.RWMutex
	executors map[string]runtimeExecutor
}{
	executors: map[string]runtimeExecutor{},
}

// jobSubmitter narrows worker.Pool[T, R] down to the lifecycle/submit methods
// this package actually uses, so the shared model worker can be faked in
// tests without spinning up real goroutines.
type jobSubmitter[T any] interface {
	Submit(job T) error
	Shutdown()
}

var sharedModelWorker = struct {
	mu   sync.RWMutex
	pool jobSubmitter[AsyncModelTask]
}{}

// RegisterRuntimeExecutor registers the runtime executor handled by this package.
// A lock guards the shared registry or worker state here so concurrent callers do not race on updates.
func RegisterRuntimeExecutor(modelName string, executor runtimeExecutor) {
	modelName = strings.TrimSpace(modelName)
	if modelName == "" || executor == nil {
		return
	}

	// Keep one executor per logical model so runtime method dispatch remains
	// deterministic even when handlers are reinitialized.
	runtimeExecutorRegistry.mu.Lock()
	defer runtimeExecutorRegistry.mu.Unlock()
	runtimeExecutorRegistry.executors[modelName] = executor
}

// UnregisterRuntimeExecutor removes the runtime executor for modelName. It is
// the symmetric counterpart to RegisterRuntimeExecutor for models that can be
// taken offline at runtime (published custom models via UnpublishModel) --
// statically registered Go models never call this since they exist for the
// lifetime of the process.
func UnregisterRuntimeExecutor(modelName string) {
	modelName = strings.TrimSpace(modelName)
	if modelName == "" {
		return
	}
	runtimeExecutorRegistry.mu.Lock()
	defer runtimeExecutorRegistry.mu.Unlock()
	delete(runtimeExecutorRegistry.executors, modelName)
}

// lookupRuntimeExecutor looks up the runtime executor handled by this package.
// A lock guards the shared registry or worker state here so concurrent callers do not race on updates.
func lookupRuntimeExecutor(modelName string) (runtimeExecutor, bool) {
	runtimeExecutorRegistry.mu.RLock()
	defer runtimeExecutorRegistry.mu.RUnlock()

	executor, ok := runtimeExecutorRegistry.executors[strings.TrimSpace(modelName)]
	return executor, ok
}

// InitSharedModelWorker initializes the shared model worker handled by this package.
func InitSharedModelWorker(cfg SharedWorkerConfig) {
	workerCount := cfg.WorkerCount
	if workerCount <= 0 {
		// Use a small default pool so async model actions work even without
		// explicit worker tuning.
		workerCount = 4
	}

	queueSize := cfg.JobQueueSize
	if queueSize <= 0 {
		// Size the queue relative to workers to absorb short bursts without
		// building an unbounded backlog.
		queueSize = workerCount * 16
	}

	sharedModelWorker.mu.Lock()
	defer sharedModelWorker.mu.Unlock()

	if sharedModelWorker.pool != nil {
		// Replace the previous pool wholesale so config changes take effect
		// immediately and stale goroutines shut down cleanly.
		sharedModelWorker.pool.Shutdown()
	}

	sharedModelWorker.pool = worker.NewPool(runtimeAsyncWorkerFn, worker.WithWorkers(workerCount), worker.WithQueue(queueSize))
	log.Infof("initialized shared ORM model worker: workers=%d queue=%d", workerCount, queueSize)
}

// ShutdownSharedModelWorker shuts down the shared model worker handled by this package.
func ShutdownSharedModelWorker() {
	sharedModelWorker.mu.Lock()
	defer sharedModelWorker.mu.Unlock()

	if sharedModelWorker.pool != nil {
		// Tear down the resource cleanly so later calls do not inherit stale state.
		sharedModelWorker.pool.Shutdown()
		sharedModelWorker.pool = nil
	}
}

// SubmitAsyncModelAction submits the async model action handled by this package.
func SubmitAsyncModelAction(task AsyncModelTask) error {
	modelName := strings.TrimSpace(task.ModelName)
	if modelName == "" {
		log.Errorf("orm: async model task requires model name")
		return fmt.Errorf("orm: async model task requires model name")
	}

	// Lazily initialize the shared worker so background actions can run without
	// a separate bootstrap phase in tests or small services.
	pool := ensureSharedModelWorker()
	return pool.Submit(task)
}

// ExecuteRuntimeMethod executes the runtime method handled by this package.
func ExecuteRuntimeMethod(ctx context.Context, modelName, methodName string, ids []string, args []any, kwargs map[string]any) (any, error) {
	// Resolve the logical model first because runtime methods are scoped to the
	// metadata registered on that model.
	meta, ok := LookupModel(strings.TrimSpace(modelName))
	if !ok {
		log.Errorf("orm: model %q is not registered", modelName)
		return nil, fmt.Errorf("orm: model %q is not registered", modelName)
	}

	method, ok := meta.LookupRuntimeMethod(strings.TrimSpace(methodName))
	if !ok {
		log.Errorf("orm: runtime method %q is not registered on model %q", methodName, modelName)
		return nil, fmt.Errorf("orm: runtime method %q is not registered on model %q", methodName, modelName)
	}
	// A client transport's row cap applies to this call only, never to the calls it makes.
	ctx = scopeClientReadCap(ctx, meta)
	// Attach a runtime environment when the executor exposes one so method
	// implementations can share context, cache, and DB handle state.
	ctx = runtimeContextWithEnvironment(ctx, meta, kwargs)
	// Methods that declare Params get their required/type contract enforced here, the single
	// dispatch chokepoint, so RPC, MCP and the Actions-menu popup all see the same rejection.
	if err := ValidateRuntimeMethodKwargs(method, args, kwargs); err != nil {
		return nil, err
	}
	// Model overrides of create/write/unlink get the record-rule and company guards of the
	// kernel defaults here, so an override cannot skip them by inserting through the repository.
	if err := guardMutationOverride(ctx, meta, method, ids, args, kwargs); err != nil {
		return nil, err
	}
	return method.Call(ctx, meta, ids, args, kwargs)
}

// RuntimeRepositoryStub overrides the shared runtime repository helpers for one
// model so business-method unit tests can isolate reads/writes/searches without
// bootstrapping the full handler/executor stack. Nil operation fields fall
// through to the registered executor, so a stub only replaces what a test needs.
type RuntimeRepositoryStub struct {
	Create     func(ctx context.Context, values RecordMap) (RecordMap, error)
	Write      func(ctx context.Context, ids []string, values RecordMap) (bool, error)
	Read       func(ctx context.Context, ids []string, fields []string) ([]RecordMap, error)
	SearchRead func(ctx context.Context, filter RecordMap, fields []string) ([]RecordMap, error)
	Unlink     func(ctx context.Context, ids []string) (bool, error)
}

var runtimeRepositoryStubs = struct {
	mu    sync.RWMutex
	stubs map[string]RuntimeRepositoryStub
}{
	stubs: map[string]RuntimeRepositoryStub{},
}

// StubRuntimeRepository installs a test-only repository stub for one model and
// returns a restore func. The seam is intentionally narrow: it is consulted only
// by the shared runtime repository helpers, never by executor registration, so
// production dispatch semantics stay owned by requireRuntimeExecutor.
func StubRuntimeRepository(modelName string, stub RuntimeRepositoryStub) func() {
	modelName = strings.TrimSpace(modelName)

	runtimeRepositoryStubs.mu.Lock()
	// Remember the previous stub so nested stubbing restores cleanly in LIFO order.
	previous, hadPrevious := runtimeRepositoryStubs.stubs[modelName]
	runtimeRepositoryStubs.stubs[modelName] = stub
	runtimeRepositoryStubs.mu.Unlock()

	return func() {
		runtimeRepositoryStubs.mu.Lock()
		defer runtimeRepositoryStubs.mu.Unlock()
		if hadPrevious {
			runtimeRepositoryStubs.stubs[modelName] = previous
			return
		}
		delete(runtimeRepositoryStubs.stubs, modelName)
	}
}

// runtimeRepositoryStubFor resolves the installed test stub for one model.
// A lock guards the shared registry or worker state here so concurrent callers do not race on updates.
func runtimeRepositoryStubFor(model *ModelMetadata) (RuntimeRepositoryStub, bool) {
	if model == nil {
		return RuntimeRepositoryStub{}, false
	}

	runtimeRepositoryStubs.mu.RLock()
	defer runtimeRepositoryStubs.mu.RUnlock()

	stub, ok := runtimeRepositoryStubs.stubs[strings.TrimSpace(model.Name)]
	return stub, ok
}

// RuntimeRepositoryCreate dispatches a runtime create through the executor
// registered for the model metadata, so model business methods share one ORM
// path instead of package-level delegate vars rebound in init().
func RuntimeRepositoryCreate(ctx context.Context, model *ModelMetadata, values RecordMap) (RecordMap, error) {
	// The test seam wins first so unit tests can isolate business logic without a live executor.
	if stub, ok := runtimeRepositoryStubFor(model); ok && stub.Create != nil {
		return stub.Create(ctx, values)
	}
	record, err := runtimeRepositoryCreate(ctx, model, values)
	if err != nil {
		return record, translateConstraintViolation(ctx, err)
	}
	return record, nil
}

// RuntimeRepositoryWrite dispatches a runtime write through the executor
// registered for the model metadata (shared helper flow for model methods).
func RuntimeRepositoryWrite(ctx context.Context, model *ModelMetadata, ids []string, values RecordMap) (bool, error) {
	// The test seam wins first so unit tests can isolate business logic without a live executor.
	if stub, ok := runtimeRepositoryStubFor(model); ok && stub.Write != nil {
		return stub.Write(ctx, ids, values)
	}
	ok2, err := runtimeRepositoryWrite(ctx, model, ids, values)
	if err != nil {
		return ok2, translateConstraintViolation(ctx, err)
	}
	return ok2, nil
}

// RuntimeRepositoryRead dispatches a runtime read through the executor
// registered for the model metadata (shared helper flow for model methods).
func RuntimeRepositoryRead(ctx context.Context, model *ModelMetadata, ids []string, fields []string) ([]RecordMap, error) {
	// The test seam wins first so unit tests can isolate business logic without a live executor.
	if stub, ok := runtimeRepositoryStubFor(model); ok && stub.Read != nil {
		return stub.Read(ctx, ids, fields)
	}
	return runtimeRepositoryRead(ctx, model, ids, fields)
}

// RuntimeRepositorySearchRead dispatches a runtime search_read through the
// executor registered for the model metadata (shared helper flow for model methods).
func RuntimeRepositorySearchRead(ctx context.Context, model *ModelMetadata, filter RecordMap, fields []string) ([]RecordMap, error) {
	// The test seam wins first so unit tests can isolate business logic without a live executor.
	if stub, ok := runtimeRepositoryStubFor(model); ok && stub.SearchRead != nil {
		return stub.SearchRead(ctx, filter, fields)
	}
	return runtimeRepositorySearchRead(ctx, model, filter, fields)
}

// RuntimeRepositoryUnlink dispatches a runtime unlink through the executor
// registered for the model metadata (shared helper flow for model methods).
func RuntimeRepositoryUnlink(ctx context.Context, model *ModelMetadata, ids []string) (bool, error) {
	// The test seam wins first so unit tests can isolate business logic without a live executor.
	if stub, ok := runtimeRepositoryStubFor(model); ok && stub.Unlink != nil {
		return stub.Unlink(ctx, ids)
	}
	return runtimeRepositoryUnlink(ctx, model, ids)
}

func runtimeRepositorySearch(ctx context.Context, model *ModelMetadata, filter RecordMap) ([]any, error) {
	executor, err := requireRuntimeExecutor(model)
	if err != nil {
		return nil, err
	}
	return executor.RuntimeSearch(ctx, filter)
}

func runtimeRepositorySearchCount(ctx context.Context, model *ModelMetadata, filter RecordMap) (int64, error) {
	executor, err := requireRuntimeExecutor(model)
	if err != nil {
		return 0, err
	}
	return executor.RuntimeSearchCount(ctx, filter)
}

func runtimeRepositoryRead(ctx context.Context, model *ModelMetadata, ids []string, fields []string) ([]RecordMap, error) {
	executor, err := requireRuntimeExecutor(model)
	if err != nil {
		return nil, err
	}
	return executor.RuntimeRead(ctx, ids, fields)
}

func runtimeRepositorySearchRead(ctx context.Context, model *ModelMetadata, filter RecordMap, fields []string) ([]RecordMap, error) {
	executor, err := requireRuntimeExecutor(model)
	if err != nil {
		return nil, err
	}
	return executor.RuntimeSearchRead(ctx, filter, fields)
}

func runtimeRepositoryCreate(ctx context.Context, model *ModelMetadata, values RecordMap) (RecordMap, error) {
	executor, err := requireRuntimeExecutor(model)
	if err != nil {
		return nil, err
	}
	return executor.RuntimeCreate(ctx, values)
}

func runtimeRepositoryWrite(ctx context.Context, model *ModelMetadata, ids []string, values RecordMap) (bool, error) {
	executor, err := requireRuntimeExecutor(model)
	if err != nil {
		return false, err
	}
	return executor.RuntimeWrite(ctx, ids, values)
}

func runtimeRepositoryUnlink(ctx context.Context, model *ModelMetadata, ids []string) (bool, error) {
	executor, err := requireRuntimeExecutor(model)
	if err != nil {
		return false, err
	}
	return executor.RuntimeUnlink(ctx, ids)
}

func runtimeRecordMapArg(args []any, kwargs map[string]any, keys ...string) (RecordMap, error) {
	if len(args) > 0 {
		// Positional payload wins so method signatures stay predictable.
		if record, err := anyToRecordMap(args[0]); err == nil && record != nil {
			return record, nil
		}
	}
	for _, key := range keys {
		if kwargs == nil {
			continue
		}
		if value, ok := kwargs[key]; ok {
			// Fall back to named kwargs for RPC-style callers that send maps by
			// keyword instead of position.
			return anyToRecordMap(value)
		}
	}
	return nil, nil
}

func runtimeIDsArg(ids []string, args []any, kwargs map[string]any) ([]string, error) {
	if len(ids) > 0 {
		// Explicit id lists from the dispatcher outrank all payload-derived ids.
		return append([]string(nil), ids...), nil
	}
	if len(args) > 0 {
		if parsed := stringSliceArg(args[0]); len(parsed) > 0 {
			return parsed, nil
		}
	}
	if kwargs != nil {
		if parsed := stringSliceArg(kwargs["ids"]); len(parsed) > 0 {
			return parsed, nil
		}
	}
	return nil, nil
}

func anyToRecordMap(value any) (RecordMap, error) {
	if value == nil {
		return nil, nil
	}

	switch typed := value.(type) {
	case RecordMap:
		// Already normalized.
		return typed, nil
	case map[string]any:
		// Accept plain Go maps without an extra marshal round-trip.
		return RecordMap(typed), nil
	default:
		// JSON round-tripping lets structs and typed payloads reuse the same map
		// conversion path without bespoke reflection code here.
		raw, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		var out RecordMap
		if err := json.Unmarshal(raw, &out); err != nil {
			return nil, err
		}
		return out, nil
	}
}

// filterFields filters the fields handled by this package.
func filterFields(records []RecordMap, fields []string) []RecordMap {
	if len(fields) == 0 {
		return records
	}

	// Normalize the requested field list into a set so repeated field names do
	// not duplicate work or output keys.
	fieldSet := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if field != "" {
			fieldSet[field] = struct{}{}
		}
	}
	if len(fieldSet) == 0 {
		return records
	}

	out := make([]RecordMap, 0, len(records))
	for _, record := range records {
		// Build a fresh map per record so trimming fields never mutates the
		// repository-owned record payload.
		filtered := make(RecordMap, len(fieldSet))
		for field := range fieldSet {
			if value, ok := record[field]; ok {
				filtered[field] = value
			}
		}
		out = append(out, filtered)
	}
	return out
}

func idsToFilterValues(ids []string) []any {
	out := make([]any, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		// Prefer numeric ids when possible so downstream filters hit integer
		// columns without implicit casts.
		if parsed, err := strconv.ParseInt(id, 10, 64); err == nil {
			out = append(out, parsed)
			continue
		}
		out = append(out, id)
	}
	return out
}

func stringSliceArg(value any) []string {
	switch typed := value.(type) {
	case []string:
		// Return a copy so callers can reorder or trim without mutating input.
		return append([]string(nil), typed...)
	case []any:
		// Accept heterogeneous RPC payloads and stringify the scalar values that
		// can meaningfully identify records or fields.
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			switch v := item.(type) {
			case string:
				if strings.TrimSpace(v) != "" {
					out = append(out, v)
				}
			case fmt.Stringer:
				out = append(out, v.String())
			case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
				out = append(out, fmt.Sprint(v))
			}
		}
		return out
	default:
		return nil
	}
}

// ensureSharedModelWorker ensures the shared model worker handled by this package.
func ensureSharedModelWorker() jobSubmitter[AsyncModelTask] {
	sharedModelWorker.mu.RLock()
	pool := sharedModelWorker.pool
	sharedModelWorker.mu.RUnlock()
	if pool != nil {
		return pool
	}

	// Double-check under the write lock so concurrent lazy initialization only
	// creates one backing pool.
	sharedModelWorker.mu.Lock()
	defer sharedModelWorker.mu.Unlock()
	if sharedModelWorker.pool == nil {
		sharedModelWorker.pool = worker.NewPool(runtimeAsyncWorkerFn, worker.WithWorkers(4), worker.WithQueue(64))
		log.Infof("initialized shared ORM model worker with default config")
	}
	return sharedModelWorker.pool
}

func runtimeAsyncWorkerFn(ctx context.Context, task AsyncModelTask) (any, error) {
	// Resolve the executor per task so the worker can service different models
	// through the same shared pool.
	executor, ok := lookupRuntimeExecutor(task.ModelName)
	if !ok {
		log.Errorf("orm: no runtime executor registered for model %q", task.ModelName)
		return nil, fmt.Errorf("orm: no runtime executor registered for model %q", task.ModelName)
	}
	return executor.ExecuteAction(ctx, task.Action, task.Filter, task.Payload)
}

// requireRuntimeExecutor requires the runtime executor handled by this package.
func requireRuntimeExecutor(model *ModelMetadata) (runtimeExecutor, error) {
	if model == nil {
		log.Errorf("orm: model metadata is required")
		return nil, fmt.Errorf("orm: model metadata is required")
	}

	// Runtime CRUD helpers only operate on registered handlers.
	executor, ok := lookupRuntimeExecutor(model.Name)
	if !ok {
		log.Errorf("orm: no runtime executor registered for model %q", model.Name)
		return nil, fmt.Errorf("orm: no runtime executor registered for model %q", model.Name)
	}
	return executor, nil
}

func runtimeContextWithEnvironment(ctx context.Context, model *ModelMetadata, kwargs map[string]any) context.Context {
	env := EnvironmentFromContext(ctx)
	if env == nil {
		executor, err := requireRuntimeExecutor(model)
		if err != nil {
			return ctx
		}
		handler, ok := executor.(interface{ runtimeEnvironment() *Environment })
		if !ok {
			return ctx
		}
		env = handler.runtimeEnvironment()
		if env == nil {
			return ctx
		}
	}
	if len(kwargs) == 0 {
		// Fast path for common calls that do not override context, sudo, or uid.
		return ContextWithEnvironment(ctx, env)
	}
	override := map[string]any{}
	if raw, ok := kwargs["context"].(map[string]any); ok {
		// Copy context overrides so later mutation by the caller cannot race with
		// the environment stored on the request context.
		for key, value := range raw {
			override[key] = value
		}
	}
	// A top-level `lang` kwarg (request body key or REST `?lang=` query) is shorthand for
	// context.lang; an explicit context.lang wins. Translated fields then read and write that
	// language, and a request naming no language at all gets the full {lang: value} maps.
	if _, hasLang := override["lang"]; !hasLang {
		if lang, ok := kwargs["lang"].(string); ok && strings.TrimSpace(lang) != "" {
			override["lang"] = strings.TrimSpace(lang)
		}
	}
	if sudo, ok := kwargs["sudo"].(bool); ok && sudo {
		// `sudo` escalates only inside this derived environment; the original
		// context stays untouched so sibling calls do not accidentally inherit it.
		env = env.WithUser(env.UserID(), true)
	}
	if userID, ok := kwargs["uid"]; ok {
		// Some generic RPC callers override `uid` explicitly after authenticating
		// as a service account. Rebind the environment before the method sees it.
		env = env.WithUser(toInt64(userID), env.IsSudo())
	}
	if len(override) > 0 {
		// Context overrides are merged last because they may depend on the final
		// user/sudo pair chosen above.
		env = env.WithContext(override)
	}
	return ContextWithEnvironment(ctx, env)
}
