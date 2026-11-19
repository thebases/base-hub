package orm

// File overview:
// - Responsibility: implements the metadata-driven ORM/runtime kernel for repository handler, where storage, runtime dispatch, and security rules stay centralized.
// - Key symbols: `ActionCreate`, `ActionUpdate`, `ActionGet`, `ActionList`.

import (
	"context"
	"fmt"
	"strings"

	"git.thebase.vn/base_hub/internal/services/log"
)

// Actions as constants (optional but safer)
const (
	ActionCreate = "create"
	ActionUpdate = "update"
	ActionGet    = "get"
	ActionList   = "list"
	ActionDelete = "delete"
)

type Handler[T any] struct {
	repo        *BunRepository[T]
	modelSpec   ModelSpec
	metadata    *ModelMetadata
	prepareTask func(map[string]any) (map[string]any, error)
	// Cache the SQL-backed select shape once per handler because runtime search
	// uses it on every pgx read path and metadata is immutable after register.
	runtimeSelectColumnsCache []runtimeSelectColumn
}

type HandlerOption[T any] func(*Handler[T])

// NewHandler builds a generic handler for a given collection.
func NewHandler[T any]() *Handler[T] {
	h := &Handler[T]{}
	return h
}

func NewHandlerWithModel[T any](spec ModelSpec, opts ...HandlerOption[T]) *Handler[T] {
	h := &Handler[T]{modelSpec: spec}
	for _, opt := range opts {
		if opt != nil {
			opt(h)
		}
	}
	return h
}

func WithTaskPreparer[T any](fn func(map[string]any) (map[string]any, error)) HandlerOption[T] {
	return func(h *Handler[T]) {
		h.prepareTask = fn
	}
}

// Init initializes the init handled by this package.
func (h *Handler[T]) Init(collection string, workerSize, queueSize int) *Handler[T] {
	storageName := h.resolveStorageName(collection)
	h.repo = NewBunRepository[T](storageName)
	h.registerModel(storageName)
	return h
}

// InitRegistered initializes the registered handled by this package.
func (h *Handler[T]) InitRegistered(workerSize, queueSize int) *Handler[T] {
	return h.Init("", workerSize, queueSize)
}

// InitDefault initializes the default handled by this package.
func (h *Handler[T]) InitDefault() *Handler[T] {
	return h.Init("", 0, 0)
}

func (h *Handler[T]) Metadata() *ModelMetadata {
	return cloneModelMetadata(h.metadata)
}

// registerModel registers the model handled by this package.
func (h *Handler[T]) registerModel(collection string) {
	if h.modelSpec.Name == "" {
		return
	}

	// Registration is best-effort here so a handler can still exist for callers
	// even if metadata registration fails during bootstrap.
	recordRedundantStorageName(h.modelSpec)
	meta, err := RegisterModelType[T](h.modelSpec.WithStorageName(collection))
	if err != nil {
		// Boot keeps going (owner decision D8) but the failure is kept for
		// ValidateRegisteredModels: the model is missing and RPC answers 404.
		log.Errorf("model registry registration failed for %s: %v", h.modelSpec.Name, err)
		recordModelRegistrationError(h.modelSpec.Name, err)
		return
	}

	h.metadata = meta
	h.runtimeSelectColumnsCache = buildRuntimeSelectColumns(meta)
	RegisterRuntimeExecutor(meta.Name, h)
	log.Infof("registered model %s (%s) on %s", meta.Name, meta.GoType, meta.StorageName)
}

// resolveStorageName resolves the storage name handled by this package.
func (h *Handler[T]) resolveStorageName(collection string) string {
	if collection != "" {
		return collection
	}
	return h.modelSpec.ResolveStorageName()
}

// Shutdown shuts down the shutdown handled by this package.
func (h *Handler[T]) Shutdown() {
}

// Submit submits the submit handled by this package.
func (h *Handler[T]) Submit(job map[string]any) {
	if h == nil {
		log.Warnf("submit skipped: handler is nil")
		return
	}
	if strings.TrimSpace(h.modelSpec.Name) == "" {
		log.Warnf("submit skipped: model name is empty")
		return
	}

	if h.prepareTask != nil {
		// Allow model-specific preprocessing to normalize worker payloads before
		// they enter the shared async execution path.
		prepared, err := h.prepareTask(job)
		if err != nil {
			log.Errorf("error preparing task for model %s: %v", h.modelSpec.Name, err)
			return
		}
		job = prepared
	}

	task := AsyncModelTask{
		ModelName: h.modelSpec.Name,
		Action:    asString(job["action"]),
	}
	if filter, ok := job["filter"].(RecordMap); ok {
		// Preserve the strongly typed RecordMap when the caller already built one.
		task.Filter = filter
	} else if filter, ok := job["filter"].(map[string]any); ok {
		// Accept plain maps too because many queue payloads originate from JSON-like data.
		task.Filter = RecordMap(filter)
	}
	task.Payload = job["payload"]

	if err := SubmitAsyncModelAction(task); err != nil {
		log.Errorf("error submitting task for model %s: %v", h.modelSpec.Name, err)
	}
}

func (h *Handler[T]) Results() <-chan any {
	return nil
}

func (h *Handler[T]) Errors() <-chan error {
	return nil
}

// ======= High-level convenience methods (mirror your BankHandler API) =======

// Create one document
func (h *Handler[T]) Create(ctx context.Context, payload T) (*InsertResult, error) {
	return h.repo.Insert(ctx, payload)
}

// CreateOrUpdate inserts when not found; otherwise updates and returns an InsertResult with the existing id.
func (h *Handler[T]) CreateOrUpdate(ctx context.Context, filter RecordMap, payload T, merge bool) (*InsertResult, error) {

	// Try to find an existing record
	found, err := h.GetOne(ctx, filter)
	if err != nil || found == nil {
		// Not found means the operation degenerates cleanly into a normal insert.
		return h.Create(ctx, payload)
	}

	m, err := toRecordMap(payload)
	if err != nil {
		return nil, err
	}
	// Strip immutable/generated fields so the upsert path does not accidentally
	// overwrite identity or timestamp columns on update.
	delete(m, "id")
	delete(m, "created_at")
	delete(m, "updated_at")

	f, err := toRecordMap(found)
	if err != nil {
		return nil, err
	}

	var mergedData RecordMap
	if merge {
		// Merge mode keeps fields from the stored record unless the new payload overrides them.
		mergedData = mergeMaps(f, m)
	} else {
		// Replace mode writes only the caller-provided payload fields.
		mergedData = m
	}

	ret, err := h.Update(ctx, RecordMap{"id": f["id"]}, mergedData)
	if err != nil {
		log.Errorf("error: Fail to update")
		return nil, fmt.Errorf("error: Fail to update")
	}
	return &InsertResult{
		InsertedID:   f["id"],
		Acknowledged: ret.Acknowledged,
	}, nil
}

func mergeMaps(original, updates RecordMap) RecordMap {
	merged := RecordMap{}

	// Start from the stored record so omitted keys are preserved in merge mode.
	for k, v := range original {
		merged[k] = v
	}

	// Then overwrite with caller-provided values, except for immutable identity.
	for k, v := range updates {
		if k == "id" {
			continue
		}
		merged[k] = v
	}
	return merged
}

// Update documents by filter
func (h *Handler[T]) Update(ctx context.Context, filter RecordMap, payload RecordMap) (*UpdateResult, error) {
	return h.repo.Update(ctx, filter, payload)
}

// Get one document by filter
func (h *Handler[T]) GetOne(ctx context.Context, filter RecordMap) (*T, error) {
	return h.repo.FindOne(ctx, filter)
}

// List documents by filter
func (h *Handler[T]) List(ctx context.Context, filter RecordMap) ([]T, error) {
	return h.repo.Find(ctx, filter)
}

// Delete by filter
func (h *Handler[T]) Delete(ctx context.Context, filter RecordMap) (*DeleteResult, error) {
	return h.repo.Delete(ctx, filter)
}

// ExecuteAction executes the action handled by this package.
func (h *Handler[T]) ExecuteAction(ctx context.Context, action string, filter RecordMap, payload any) (any, error) {
	switch strings.TrimSpace(action) {
	case ActionCreate:
		document, err := h.toDocument(payload)
		if err != nil {
			return nil, err
		}
		return h.repo.Insert(ctx, document)
	case ActionUpdate:
		values, err := anyToRecordMap(payload)
		if err != nil {
			return nil, err
		}
		return h.repo.Update(ctx, filter, values)
	case ActionGet:
		return h.repo.FindOne(ctx, filter)
	case ActionList:
		return h.repo.Find(ctx, filter)
	case ActionDelete:
		return h.repo.Delete(ctx, filter)
	default:
		log.Errorf("unknown action: %s", action)
		return nil, fmt.Errorf("unknown action: %s", action)
	}
}
