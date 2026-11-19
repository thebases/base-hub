package orm

// File overview:
// - Responsibility: implements the metadata-driven ORM/runtime kernel for runtime environment, where storage, runtime dispatch, and security rules stay centralized.
// - Key symbols: `NewEnvironment`, `NewEnvironmentWithDBName`, `envContextKey`, `environmentState`.
// - Pattern: Leans on metadata-driven registration so storage mapping, runtime methods, and behavior hooks stay centralized instead of being scattered across handlers.
// - Pattern: Uses Bun-facing model or repository patterns so schema mapping stays explicit and transaction-aware inside the backend data layer.

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sync"

	"git.thebase.vn/base_hub/internal/services/log"
	"github.com/uptrace/bun"
)

type envContextKey string

const runtimeEnvironmentContextKey envContextKey = "orm.runtime.environment"

const (
	ContextKeyDatabaseName         = "database_name"
	ContextKeyAllowedCompanyIDs    = "allowed_company_ids"
	ContextKeyAuthorizedCompanyIDs = "authorized_company_ids"
	ContextKeyCurrentCompanyID     = "company_id"
)

// recordCacheKey identifies one record; fieldCacheKey one model field. Struct
// keys avoid building "model/id" strings on every cache hit.
type recordCacheKey struct{ model, id string }

type fieldCacheKey struct{ model, field string }

type environmentState struct {
	cacheMu      sync.RWMutex
	fieldCache   map[recordCacheKey]map[string]any
	prefetchDone map[fieldCacheKey]map[recordCacheKey]struct{}
}

// Environment carries request-scoped runtime state such as transaction handle,
// caller identity, sudo mode, context flags, and prefetch-backed field cache.
type Environment struct {
	db      bun.IDB
	dbName  string
	userID  int64
	sudo    bool
	context map[string]any
	state   *environmentState
}

// NewEnvironment builds one isolated runtime environment for ORM method calls.
func NewEnvironment(db bun.IDB, userID int64, values map[string]any, sudo bool) *Environment {
	return NewEnvironmentWithDBName(db, "", userID, values, sudo)
}

// NewEnvironmentWithDBName builds one isolated runtime environment for ORM
// method calls and keeps the logical database name available to RPC layers.
func NewEnvironmentWithDBName(db bun.IDB, dbName string, userID int64, values map[string]any, sudo bool) *Environment {
	// Clone the caller context map so later WithContext mutations stay isolated.
	cloned := make(map[string]any, len(values))
	maps.Copy(cloned, values)
	if dbName != "" {
		cloned[ContextKeyDatabaseName] = dbName
	}
	return &Environment{
		db:      db,
		dbName:  dbName,
		userID:  userID,
		sudo:    sudo,
		context: cloned,
		state: &environmentState{
			fieldCache:   map[recordCacheKey]map[string]any{},
			prefetchDone: map[fieldCacheKey]map[recordCacheKey]struct{}{},
		},
	}
}

// cloneWith clones the with handled by this package.
func (e *Environment) cloneWith(db bun.IDB, dbName string, userID int64, sudo bool, values map[string]any) *Environment {
	if e == nil {
		return NewEnvironmentWithDBName(db, dbName, userID, values, sudo)
	}
	// Share the mutable cache state across clones so one request can benefit
	// from prefetches regardless of which helper produced the next environment.
	cloned := make(map[string]any, len(values))
	maps.Copy(cloned, values)
	return &Environment{
		db:      db,
		dbName:  dbName,
		userID:  userID,
		sudo:    sudo,
		context: cloned,
		state:   e.state,
	}
}

// EnvironmentFromContext extracts the runtime environment injected into ctx.
func EnvironmentFromContext(ctx context.Context) *Environment {
	if ctx == nil {
		return nil
	}
	env, _ := ctx.Value(runtimeEnvironmentContextKey).(*Environment)
	return env
}

// DBFromContext resolves a usable bun.IDB for ctx, preferring the bound runtime
// environment's connection and falling back to the shared process-wide handle.
// Consolidates the identical helper previously copy-pasted across base-automation,
// base-calendar, and base-credential's cron dispatch loops.
func DBFromContext(ctx context.Context) bun.IDB {
	if env := EnvironmentFromContext(ctx); env != nil {
		if db, ok := env.DB().(bun.IDB); ok && db != nil {
			return db
		}
	}
	if db := GetBunDB(); db != nil {
		return db
	}
	return nil
}

// ContextWithEnvironment binds env into ctx for downstream runtime helpers.
func ContextWithEnvironment(ctx context.Context, env *Environment) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, runtimeEnvironmentContextKey, env)
}

// DB returns the current DB or transaction handle.
func (e *Environment) DB() bun.IDB {
	if e == nil {
		return nil
	}
	return e.db
}

// DBName returns the logical runtime database name when one is bound.
func (e *Environment) DBName() string {
	if e == nil {
		return ""
	}
	return e.dbName
}

// UserID returns the runtime caller id.
func (e *Environment) UserID() int64 {
	if e == nil {
		return 0
	}
	return e.userID
}

// IsSudo reports whether access checks should be bypassed.
func (e *Environment) IsSudo() bool {
	return e != nil && e.sudo
}

// ContextMap returns a defensive copy of the environment context.
func (e *Environment) ContextMap() map[string]any {
	if e == nil {
		return nil
	}
	cloned := make(map[string]any, len(e.context))
	maps.Copy(cloned, e.context)
	return cloned
}

// ContextValue looks up one request-scoped context key.
func (e *Environment) ContextValue(key string) (any, bool) {
	if e == nil {
		return nil, false
	}
	value, ok := e.context[key]
	return value, ok
}

// WithContext returns a copy with merged context overrides.
func (e *Environment) WithContext(values map[string]any) *Environment {
	if e == nil {
		return nil
	}
	// Start from a cloned map so override keys only affect the returned
	// environment and not the original caller scope.
	next := e.ContextMap()
	maps.Copy(next, values)
	dbName := e.dbName
	if value, ok := next[ContextKeyDatabaseName]; ok {
		dbName = fmt.Sprint(value)
	}
	return e.cloneWith(e.db, dbName, e.userID, e.sudo, next)
}

// WithUser returns a copy with caller identity and sudo mode adjusted.
func (e *Environment) WithUser(userID int64, sudo bool) *Environment {
	if e == nil {
		return nil
	}
	return e.cloneWith(e.db, e.dbName, userID, sudo, e.context)
}

// WithDB returns a copy bound to a different DB or transaction handle.
func (e *Environment) WithDB(db bun.IDB) *Environment {
	if e == nil {
		return nil
	}
	return e.cloneWith(db, e.dbName, e.userID, e.sudo, e.context)
}

// WithDBConnection returns a copy bound to a different DB handle and logical
// database name.
func (e *Environment) WithDBConnection(db bun.IDB, dbName string) *Environment {
	if e == nil {
		return nil
	}
	next := e.ContextMap()
	if dbName != "" {
		next[ContextKeyDatabaseName] = dbName
	}
	return e.cloneWith(db, dbName, e.userID, e.sudo, next)
}

// AuthorizedCompanyIDs returns the full company set granted to the caller.
func (e *Environment) AuthorizedCompanyIDs() []int64 {
	if e == nil {
		return nil
	}
	return uniqueInt64Slice(int64SliceFromAny(e.context[ContextKeyAuthorizedCompanyIDs]))
}

// AllowedCompanyIDs returns the current active company set after validating
// that the requested context does not exceed the authorized companies.
func (e *Environment) AllowedCompanyIDs() ([]int64, error) {
	if e == nil {
		return nil, nil
	}
	authorized := e.AuthorizedCompanyIDs()
	requested := uniqueInt64Slice(int64SliceFromAny(e.context[ContextKeyAllowedCompanyIDs]))
	if len(requested) == 0 {
		requested = append([]int64(nil), authorized...)
	}
	if e.sudo || len(requested) == 0 || len(authorized) == 0 {
		return requested, nil
	}
	for _, companyID := range requested {
		if !slices.Contains(authorized, companyID) {
			log.Errorf("access to unauthorized company %d", companyID)
			return nil, fmt.Errorf("access to unauthorized company %d", companyID)
		}
	}
	return requested, nil
}

// CompanyID returns the active primary company for the current request.
func (e *Environment) CompanyID() (int64, error) {
	if e == nil {
		return 0, nil
	}
	if companyID := toInt64(e.context[ContextKeyCurrentCompanyID]); companyID > 0 {
		allowed, err := e.AllowedCompanyIDs()
		if err != nil {
			return 0, err
		}
		// Explicit current-company overrides win, but they still must stay
		// inside the validated active-company scope for non-sudo callers.
		if len(allowed) == 0 || e.sudo || slices.Contains(allowed, companyID) {
			return companyID, nil
		}
		log.Errorf("access to unauthorized company %d", companyID)
		return 0, fmt.Errorf("access to unauthorized company %d", companyID)
	}
	allowed, err := e.AllowedCompanyIDs()
	if err != nil {
		return 0, err
	}
	if len(allowed) > 0 {
		return allowed[0], nil
	}
	return 0, nil
}

// WithCompany returns a copy whose active company becomes the primary context
// while preserving the remaining allowed companies.
func (e *Environment) WithCompany(companyID int64) *Environment {
	if e == nil || companyID <= 0 {
		return e
	}
	next := e.ContextMap()
	allowed := uniqueInt64Slice(int64SliceFromAny(next[ContextKeyAllowedCompanyIDs]))
	allowed = append([]int64{companyID}, removeInt64Value(allowed, companyID)...)
	next[ContextKeyAllowedCompanyIDs] = allowed
	next[ContextKeyCurrentCompanyID] = companyID
	return e.cloneWith(e.db, e.dbName, e.userID, e.sudo, next)
}

// CacheField stores one prefetched field value in the environment cache.
func (e *Environment) CacheField(modelName, recordID, fieldName string, value any) {
	if e == nil {
		return
	}
	// Use model/id as the first-level cache key so multiple records can store
	// the same field names without colliding.
	cacheKey := recordCacheKey{modelName, recordID}
	e.state.cacheMu.Lock()
	defer e.state.cacheMu.Unlock()
	if e.state.fieldCache[cacheKey] == nil {
		e.state.fieldCache[cacheKey] = map[string]any{}
	}
	e.state.fieldCache[cacheKey][fieldName] = value
}

// CacheFields stores a grouped field payload for one record so later reads in
// the same request can reuse the same Bun-backed fetch result.
func (e *Environment) CacheFields(modelName, recordID string, values map[string]any) {
	if e == nil || len(values) == 0 {
		return
	}
	for fieldName, value := range values {
		e.CacheField(modelName, recordID, fieldName, value)
	}
}

// CachedField reads one cached field value when present.
func (e *Environment) CachedField(modelName, recordID, fieldName string) (any, bool) {
	if e == nil {
		return nil, false
	}
	cacheKey := recordCacheKey{modelName, recordID}
	e.state.cacheMu.RLock()
	defer e.state.cacheMu.RUnlock()
	values := e.state.fieldCache[cacheKey]
	if values == nil {
		return nil, false
	}
	value, ok := values[fieldName]
	return value, ok
}

// CachedFields reads one cached field for many records under a single lock
// acquisition. Only ids with a cached value appear in the result.
func (e *Environment) CachedFields(modelName, fieldName string, ids []string) map[string]any {
	out := make(map[string]any, len(ids))
	if e == nil || len(ids) == 0 {
		return out
	}
	e.state.cacheMu.RLock()
	defer e.state.cacheMu.RUnlock()
	for _, id := range ids {
		if value, ok := e.state.fieldCache[recordCacheKey{modelName, id}][fieldName]; ok {
			out[id] = value
		}
	}
	return out
}

// Invalidate removes cached fields for the given records.
func (e *Environment) Invalidate(modelName string, ids []string) {
	if e == nil {
		return
	}
	// Invalidate both raw field cache entries and grouped prefetch markers so
	// later reads will recompute fresh values after a write.
	e.state.cacheMu.Lock()
	defer e.state.cacheMu.Unlock()
	for _, id := range ids {
		delete(e.state.fieldCache, recordCacheKey{modelName, id})
	}
	for key := range e.state.prefetchDone {
		if len(ids) == 0 {
			// An empty id list means the caller wants to flush every prefetch mark
			// for the model cache namespace.
			delete(e.state.prefetchDone, key)
			continue
		}
		for _, id := range ids {
			delete(e.state.prefetchDone[key], recordCacheKey{modelName, id})
		}
		if len(e.state.prefetchDone[key]) == 0 {
			delete(e.state.prefetchDone, key)
		}
	}
}

// MarkPrefetched records that one field was resolved for the given records in
// the current request scope so adjacent reads can skip duplicate grouped loads.
func (e *Environment) MarkPrefetched(modelName, fieldName string, ids []string) {
	if e == nil || len(ids) == 0 {
		return
	}
	// Track grouped prefetch completion per model field so enrichment helpers
	// can avoid repeated fan-out queries inside one request.
	cacheKey := fieldCacheKey{modelName, fieldName}
	e.state.cacheMu.Lock()
	defer e.state.cacheMu.Unlock()
	if e.state.prefetchDone[cacheKey] == nil {
		e.state.prefetchDone[cacheKey] = map[recordCacheKey]struct{}{}
	}
	for _, id := range ids {
		e.state.prefetchDone[cacheKey][recordCacheKey{modelName, id}] = struct{}{}
	}
}

// MissingPrefetchIDs returns the record ids that still need grouped field
// resolution inside the current request scope.
func (e *Environment) MissingPrefetchIDs(modelName, fieldName string, ids []string) []string {
	if e == nil || len(ids) == 0 {
		return append([]string(nil), ids...)
	}
	// Return a defensive copy when nothing is cached so callers can mutate the
	// result without touching their original id slice.
	cacheKey := fieldCacheKey{modelName, fieldName}
	e.state.cacheMu.RLock()
	defer e.state.cacheMu.RUnlock()
	known := e.state.prefetchDone[cacheKey]
	if len(known) == 0 {
		return append([]string(nil), ids...)
	}
	missing := make([]string, 0, len(ids))
	for _, id := range ids {
		if _, ok := known[recordCacheKey{modelName, id}]; ok {
			continue
		}
		missing = append(missing, id)
	}
	return missing
}

// ActiveTest reports whether the default active=true filter should be applied.
func (e *Environment) ActiveTest() bool {
	if e == nil {
		return true
	}
	value, ok := e.context["active_test"]
	if !ok {
		return true
	}
	flag, ok := value.(bool)
	if !ok {
		return true
	}
	return flag
}

func int64SliceFromAny(raw any) []int64 {
	switch typed := raw.(type) {
	case []int64:
		return append([]int64(nil), typed...)
	case []int:
		out := make([]int64, 0, len(typed))
		for _, value := range typed {
			out = append(out, int64(value))
		}
		return out
	case []any:
		out := make([]int64, 0, len(typed))
		for _, item := range typed {
			if value := toInt64(item); value > 0 {
				out = append(out, value)
			}
		}
		return out
	default:
		if value := toInt64(raw); value > 0 {
			return []int64{value}
		}
		return nil
	}
}

func uniqueInt64Slice(values []int64) []int64 {
	if len(values) == 0 {
		return nil
	}
	out := make([]int64, 0, len(values))
	seen := make(map[int64]struct{}, len(values))
	for _, value := range values {
		if value <= 0 {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func removeInt64Value(values []int64, target int64) []int64 {
	if len(values) == 0 {
		return nil
	}
	out := make([]int64, 0, len(values))
	for _, value := range values {
		if value == target {
			continue
		}
		out = append(out, value)
	}
	return out
}
