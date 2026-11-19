package orm

import "reflect"

// runtimeStore is what the shared runtime read/write helpers need to know
// about a model's storage: its metadata, physical table and Go struct type.
// Handler[T] (static models) and dynamicRuntimeExecutor (published custom
// models) each hand one out through store(), so those helpers are written
// once as methods instead of free functions taking the same three arguments.
type runtimeStore struct {
	meta  *ModelMetadata
	table string
	typ   reflect.Type
}

// store describes this handler's model. Callers have already checked h.repo.
func (h *Handler[T]) store() runtimeStore {
	return runtimeStore{meta: h.metadata, table: h.repo.tableName, typ: reflect.TypeOf((*T)(nil)).Elem()}
}

func (e *dynamicRuntimeExecutor) store() runtimeStore {
	return runtimeStore{meta: e.metadata, table: e.tableName, typ: e.typ}
}
