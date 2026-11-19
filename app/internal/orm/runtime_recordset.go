package orm

// File overview:
// - Responsibility: implements the metadata-driven ORM/runtime kernel for runtime recordset, where storage, runtime dispatch, and security rules stay centralized.
// - Key symbols: `NewRecordSet`, `Model`, `RecordSet`.
// - Pattern: Leans on metadata-driven registration so storage mapping, runtime methods, and behavior hooks stay centralized instead of being scattered across handlers.

import (
	"fmt"
	"git.thebase.vn/base_hub/internal/services/log"
	"slices"
)

// RecordSet is the runtime-first recordset abstraction passed to hooks and
// higher-level helpers instead of ad hoc id slices.
type RecordSet struct {
	model *ModelMetadata
	env   *Environment
	ids   []string
}

// NewRecordSet builds an immutable recordset view for one model and id list.
func NewRecordSet(model *ModelMetadata, env *Environment, ids []string) RecordSet {
	// Copy ids so external slices cannot mutate the logical membership of the
	// recordset after construction.
	cloned := append([]string(nil), ids...)
	return RecordSet{model: model, env: env, ids: cloned}
}

// Model returns the bound model metadata.
func (r RecordSet) Model() *ModelMetadata {
	return r.model
}

// ModelName returns the logical model name.
func (r RecordSet) ModelName() string {
	if r.model == nil {
		return ""
	}
	return r.model.Name
}

// Environment returns the bound runtime environment.
func (r RecordSet) Environment() *Environment {
	return r.env
}

// IDs returns a defensive copy of the record ids.
func (r RecordSet) IDs() []string {
	return append([]string(nil), r.ids...)
}

// Len reports the number of records in the set.
func (r RecordSet) Len() int {
	return len(r.ids)
}

// EnsureOne preserves singleton expectations in hook code.
func (r RecordSet) EnsureOne() error {
	if len(r.ids) != 1 {
		log.Errorf("orm: expected singleton %s, got %v", r.ModelName(), r.ids)
		return fmt.Errorf("orm: expected singleton %s, got %v", r.ModelName(), r.ids)
	}
	return nil
}

// WithContext returns a copy with merged runtime context.
func (r RecordSet) WithContext(values map[string]any) RecordSet {
	// Return a copy so chaining keeps recordsets immutable at the API surface.
	r.env = r.env.WithContext(values)
	return r
}

// WithUser returns a copy with different caller identity.
func (r RecordSet) WithUser(userID int64, sudo bool) RecordSet {
	// Caller identity changes produce a new environment snapshot for the same
	// logical record ids.
	r.env = r.env.WithUser(userID, sudo)
	return r
}

// Sudo returns a copy in sudo mode.
func (r RecordSet) Sudo() RecordSet {
	if r.env == nil {
		return r
	}
	r.env = r.env.WithUser(r.env.UserID(), true)
	return r
}

// Union merges ids from two recordsets of the same model.
func (r RecordSet) Union(other RecordSet) RecordSet {
	if r.ModelName() == "" {
		return other
	}
	if r.ModelName() != other.ModelName() {
		// Cross-model unions are ignored because recordsets are model-scoped.
		return r
	}
	// Sort before Compact so duplicate ids collapse deterministically.
	merged := append(r.IDs(), other.IDs()...)
	slices.Sort(merged)
	return NewRecordSet(r.model, r.env, slices.Compact(merged))
}
