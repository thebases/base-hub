package orm

// File overview:
// - Responsibility: implements the runtimeExecutor interface for models
//   published at runtime from the sys.model/sys.model.field catalog (see
//   PublishModel in model_publish.go), closing the gap where PublishModel
//   registered a model's metadata and physical table but never gave it a
//   runtime executor -- so every admin UI / RPC call against a freshly
//   published model failed with "orm: no runtime executor registered for
//   model ...".
// - Key symbols: `dynamicRuntimeExecutor`, `newDynamicRuntimeExecutor`.
// - Pattern: Handler[T] cannot be instantiated for these models because Go
//   generics require a compile-time type argument, and a published model's
//   Go type only exists as a runtime reflect.Type (built via
//   reflect.StructOf in buildDynamicStructType). This type reimplements
//   Handler[T]'s runtime CRUD surface using reflection instead of a type
//   parameter, while reusing every *Core helper Handler[T] itself was
//   refactored to share (access rules, defaults, constraints, stored-field
//   recompute, record events) so a published model gets identical behavior
//   to a statically registered one -- not a second, divergent
//   implementation of create/write/unlink.

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"strings"

	"git.thebase.vn/base_hub/internal/services/log"
	"github.com/uptrace/bun"
)

type dynamicRuntimeExecutor struct {
	metadata  *ModelMetadata
	typ       reflect.Type
	tableName string
	db        *bun.DB
}

// newDynamicRuntimeExecutor builds the runtime executor for a just-published
// custom model. meta.goReflectType is already populated by
// DefaultModelRegistry().Register (called from PublishModel) with the exact
// reflect.StructOf type buildDynamicStructType synthesized, so no separate
// type plumbing is required here.
func newDynamicRuntimeExecutor(meta *ModelMetadata, tableName string) *dynamicRuntimeExecutor {
	return &dynamicRuntimeExecutor{
		metadata:  meta,
		typ:       meta.goReflectType,
		tableName: tableName,
		db:        GetBunDB(),
	}
}

func (e *dynamicRuntimeExecutor) Metadata() *ModelMetadata {
	return cloneModelMetadata(e.metadata)
}

// runtimeEnvironment mirrors Handler[T].runtimeEnvironment so
// runtimeContextWithEnvironment (runtime_service.go) can attach a usable
// environment for callers that invoke a runtime method without one already
// bound to the context.
func (e *dynamicRuntimeExecutor) runtimeEnvironment() *Environment {
	if e == nil {
		return nil
	}
	return NewEnvironment(e.db, 0, nil, false)
}

// dynamicGuardNoRelationalValues defends an invariant PublishModel already
// enforces at publish time (buildDynamicStructType / dynamicUnsupportedRelationDataTypes
// reject one2many/many2many field definitions), so a non-empty relational
// value set here should be unreachable in practice. It exists so a future
// relaxation of that publish-time guard fails loudly instead of silently
// dropping relational writes on the floor.
func dynamicGuardNoRelationalValues(meta *ModelMetadata, relational RecordMap) error {
	if len(relational) == 0 {
		return nil
	}
	log.Errorf("orm: model %q does not support one2many/many2many field writes on published custom models", meta.Name)
	return fmt.Errorf("orm: model %q does not support one2many/many2many field writes on published custom models", meta.Name)
}

// dynamicDocumentFromValues is the reflect-based equivalent of
// Handler[T].toDocument: it allocates a new instance of the model's
// synthesized struct type and JSON round-trips values into it so the
// resulting pointer can be handed to bun's Model() the same way a *T would be.
func dynamicDocumentFromValues(typ reflect.Type, values RecordMap) (any, error) {
	raw, err := json.Marshal(values)
	if err != nil {
		return nil, err
	}
	document := reflect.New(typ).Interface()
	if err := json.Unmarshal(raw, document); err != nil {
		return nil, err
	}
	return document, nil
}

// ExecuteAction implements the async-worker dispatch surface
// (SubmitAsyncModelAction). Its result is never consumed by any caller --
// InitSharedModelWorker's own doc comment notes the shared worker pool
// drains and discards both the results and errors channel -- so returning
// RecordMap-shaped results here (rather than a raw struct pointer, which a
// dynamic type can't usefully hand back to a caller anyway) is safe and
// mirrors what Handler[T].ExecuteAction does for its own action set.
func (e *dynamicRuntimeExecutor) ExecuteAction(ctx context.Context, action string, filter RecordMap, payload any) (any, error) {
	db := runtimeDBFallback(ctx, e.db)
	switch strings.TrimSpace(action) {
	case ActionCreate:
		values, err := anyToRecordMap(payload)
		if err != nil {
			return nil, err
		}
		document, err := dynamicDocumentFromValues(e.typ, values)
		if err != nil {
			return nil, err
		}
		return bunInsertWithDB(ctx, db, e.tableName, e.typ, document)
	case ActionUpdate:
		values, err := anyToRecordMap(payload)
		if err != nil {
			return nil, err
		}
		return bunUpdateWithDB(ctx, db, e.tableName, e.typ, filter, values)
	case ActionGet:
		return bunFindOneRecordMap(ctx, db, e.tableName, e.typ, filter)
	case ActionList:
		return bunFindRecordMaps(ctx, db, e.tableName, e.typ, filter)
	case ActionDelete:
		return bunDeleteWithDB(ctx, db, e.tableName, e.typ, filter)
	default:
		log.Errorf("unknown action: %s", action)
		return nil, fmt.Errorf("unknown action: %s", action)
	}
}

func (e *dynamicRuntimeExecutor) RuntimeSearch(ctx context.Context, filter RecordMap) ([]any, error) {
	records, err := e.runtimeSearchRecords(ctx, filter, nil)
	if err != nil {
		return nil, err
	}
	out := make([]any, 0, len(records))
	for _, record := range records {
		out = append(out, extractModelID(record))
	}
	return out, nil
}

func (e *dynamicRuntimeExecutor) RuntimeSearchCount(ctx context.Context, filter RecordMap) (int64, error) {
	directFilter, domain, _, _, _, nameNeedle, _, err := runtimeSearchOptionsCore(e.metadata, filter)
	if err != nil {
		return 0, err
	}
	if runtimeSearchPGXPool(ctx) != nil {
		return e.store().searchCountPGX(ctx, directFilter, domain, nameNeedle)
	}
	db := runtimeDBFallback(ctx, e.db)
	if db == nil {
		log.Errorf("orm: BunRepository is not initialized")
		return 0, fmt.Errorf("orm: BunRepository is not initialized")
	}
	query, err := e.store().selectQuery(ctx, db, directFilter, domain, nameNeedle, reflect.New(e.typ).Interface())
	if err != nil {
		return 0, err
	}
	count, err := query.Count(ctx)
	return int64(count), err
}

func (e *dynamicRuntimeExecutor) RuntimeRead(ctx context.Context, ids []string, fields []string) ([]RecordMap, error) {
	filter := RecordMap{"__ids": append([]string(nil), ids...)}
	if len(ids) > 0 {
		if len(ids) == 1 {
			filter["id"] = toInt64(ids[0])
		} else {
			filter["id"] = idsToFilterValues(ids)
		}
	}
	return e.runtimeSearchRecords(ctx, filter, fields)
}

func (e *dynamicRuntimeExecutor) RuntimeSearchRead(ctx context.Context, filter RecordMap, fields []string) ([]RecordMap, error) {
	return e.runtimeSearchRecords(ctx, filter, fields)
}

// runtimeSearchRecords mirrors Handler[T].runtimeSearchRecords.
func (e *dynamicRuntimeExecutor) runtimeSearchRecords(ctx context.Context, filter RecordMap, fields []string) ([]RecordMap, error) {
	directFilter, domain, order, offset, limit, nameNeedle, ids, err := runtimeSearchOptionsCore(e.metadata, filter)
	if err != nil {
		return nil, err
	}
	var rows []RecordMap
	if runtimeSearchPGXPool(ctx) != nil {
		rows, err = e.store().searchDocumentsPGX(ctx, buildRuntimeSelectColumns(e.metadata), directFilter, domain, order, offset, limit, nameNeedle)
		if err != nil {
			return nil, err
		}
	} else {
		rows, err = e.runtimeSearchDocuments(ctx, directFilter, domain, order, offset, limit, nameNeedle)
		if err != nil {
			return nil, err
		}
	}
	if len(ids) > 0 {
		rows = reorderRecordsByIDs(rows, ids)
	}
	// Published custom models never have translate:true fields --
	// buildDynamicFieldTag never emits that meta option -- so this is
	// guaranteed to be a no-op; kept only so behavior stays parallel to
	// Handler[T].runtimeSearchRecords if that ever changes.
	resolveTranslatedRecordValues(e.metadata, rows, runtimeRequestLang(ctx))
	rows, err = e.enrichRuntimeRecords(ctx, rows, fields)
	if err != nil {
		return nil, err
	}
	return filterFields(rows, fields), nil
}

// runtimeSearchDocuments is the reflect-based equivalent of
// Handler[T].runtimeSearchDocuments: it scans into a *[]<dynamic struct>
// instead of *[]T (there is no compile-time T for a published model) and
// converts each row straight to a RecordMap since that is the only shape a
// caller can use for a runtime-only type anyway.
func (e *dynamicRuntimeExecutor) runtimeSearchDocuments(ctx context.Context, directFilter RecordMap, domain DomainNode, order string, offset, limit int, nameNeedle string) ([]RecordMap, error) {
	sliceValue := reflect.New(reflect.SliceOf(e.typ))
	query, err := e.store().selectQuery(ctx, runtimeDBFallback(ctx, e.db), directFilter, domain, nameNeedle, sliceValue.Interface())
	if err != nil {
		return nil, err
	}
	if err := e.store().applyOrder(ctx, query, order); err != nil {
		return nil, err
	}
	if offset > 0 {
		query = query.Offset(offset)
	}
	if limit > 0 {
		query = query.Limit(limit)
	}
	if err := query.Scan(ctx); err != nil {
		log.Warnf("orm: runtime search documents failed for %s sql=%s err=%v", e.metadata.Name, query.String(), err)
		return nil, err
	}
	slice := sliceValue.Elem()
	out := make([]RecordMap, 0, slice.Len())
	for i := 0; i < slice.Len(); i++ {
		record, err := toRecordMap(slice.Index(i).Interface())
		if err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	return out, nil
}

// enrichRuntimeRecords is a deliberately narrower version of
// Handler[T].enrichRuntimeRecords: published custom models can only ever
// carry scalar fields plus many2one/one2one relations (buildDynamicStructType
// / dynamicUnsupportedRelationDataTypes reject compute, related, one2many,
// and many2many field definitions at publish time), so the one2many/
// many2many and compute/related branches Handler[T] needs can never be
// reached here and are intentionally omitted rather than dead-copied.
func (e *dynamicRuntimeExecutor) enrichRuntimeRecords(ctx context.Context, records []RecordMap, fields []string) ([]RecordMap, error) {
	if len(records) == 0 {
		return records, nil
	}
	needAll := len(fields) == 0
	env := EnvironmentFromContext(ctx)
	for _, field := range e.metadata.Fields {
		if !needAll && !containsString(fields, field.Name) {
			continue
		}
		if field.Kind != FieldKindManyToOne && field.Kind != FieldKindOneToOne {
			continue
		}
		prefetched, err := prefetchManyToOneDisplaysCore(ctx, field, records)
		if err != nil {
			return nil, err
		}
		for _, record := range records {
			recordID, ok := runtimeStringID(record["id"])
			if !ok {
				continue
			}
			value, exists := record[field.Name]
			if !exists || value == nil {
				record[field.Name] = false
				if env != nil {
					env.CacheField(e.metadata.Name, recordID, field.Name, false)
				}
				continue
			}
			relationID, ok := runtimeStringID(value)
			if !ok {
				record[field.Name] = false
				if env != nil {
					env.CacheField(e.metadata.Name, recordID, field.Name, false)
				}
				continue
			}
			if display, exists := prefetched[relationID]; exists {
				record[field.Name] = display
			} else {
				record[field.Name] = resolveManyToOneDisplayCore(ctx, field, value)
			}
			if env != nil {
				env.CacheField(e.metadata.Name, recordID, field.Name, record[field.Name])
			}
		}
	}
	return records, nil
}

// RuntimeCreate mirrors Handler[T].RuntimeCreate: same access-rule check,
// default application, constraint run, stored-field recompute, and
// before/after record-event dispatch after commit -- only the document
// construction and INSERT go through reflect instead of a compile-time T.
func (e *dynamicRuntimeExecutor) RuntimeCreate(ctx context.Context, values RecordMap) (RecordMap, error) {
	var created RecordMap
	var createPreImage RecordMap

	err := withRuntimeTxDB(ctx, e.db, func(txCtx context.Context) error {
		normalizedValues, err := runtimeNormalizeFalseValues(e.metadata, values)
		if err != nil {
			return err
		}
		values = normalizedValues
		if err := ensureModelAccess(txCtx, EnvironmentFromContext(txCtx), e.metadata, "create", nil, values); err != nil {
			return err
		}
		nextValues, err := runtimeApplyDefaultsCore(txCtx, e.metadata, values)
		if err != nil {
			return err
		}
		createPreImage = nextValues
		directValues, relationalValues, err := splitRuntimeValuesCore(e.metadata, nextValues)
		if err != nil {
			return err
		}
		if err := dynamicGuardNoRelationalValues(e.metadata, relationalValues); err != nil {
			return err
		}
		// Same as Handler[T]: translated values go into the INSERT as their
		// language envelope (a NOT NULL translated column would reject a
		// follow-up UPDATE); only unprovided translated columns are excluded.
		directValues, translatedValues := splitTranslatedValues(e.metadata, directValues)
		envelopes, excludedTranslated, err := translatedInsertValues(txCtx, e.metadata, translatedValues)
		if err != nil {
			return err
		}
		if len(envelopes) > 0 && directValues == nil {
			directValues = RecordMap{}
		}
		maps.Copy(directValues, envelopes)
		document, err := dynamicDocumentFromValues(e.typ, directValues)
		if err != nil {
			return err
		}

		db := runtimeDBFallback(txCtx, e.db)
		inserted, err := bunInsertWithDB(txCtx, db, e.tableName, e.typ, document, excludedTranslated...)
		if err != nil {
			return translateConstraintViolation(txCtx, err)
		}

		result, err := toRecordMap(document)
		if err != nil {
			return err
		}
		log.Debugf("creating %s with values: %+v", e.metadata.Name, document)
		if inserted != nil && !isZeroModelID(inserted.InsertedID) {
			result["id"] = inserted.InsertedID
		}
		id, _ := runtimeStringID(result["id"])
		if isZeroModelID(result["id"]) {
			log.Errorf("orm: create on %s did not return an inserted id", e.metadata.Name)
			return fmt.Errorf("orm: create on %s did not return an inserted id", e.metadata.Name)
		}
		if err := runConstraintsCore(txCtx, e.metadata, []string{id}, nextValues); err != nil {
			return err
		}
		if err := e.store().recomputeStored(txCtx, db, []string{id}, nextValues); err != nil {
			return err
		}
		if env := EnvironmentFromContext(txCtx); env != nil {
			env.Invalidate(e.metadata.Name, []string{id})
		}
		records, err := e.RuntimeRead(txCtx, []string{id}, nil)
		created, err = createReadBackResult(e.metadata.Name, id, records, err, result)
		return err
	})
	// Fire record-lifecycle events only after the transaction has committed,
	// same audit invariant as Handler[T].RuntimeCreate.
	if err == nil && HasRecordEventHandlers(e.metadata.Name) {
		id, _ := runtimeStringID(created["id"])
		fireRecordEventsAfterCommit(ctx, e.metadata.Name, []recordEvent{
			{name: RecordEventBeforeCreate, id: id, record: createPreImage},
			{name: RecordEventAfterCreate, id: id, record: created},
		})
	}
	return created, err
}

// RuntimeWrite mirrors Handler[T].RuntimeWrite.
func (e *dynamicRuntimeExecutor) RuntimeWrite(ctx context.Context, ids []string, values RecordMap) (bool, error) {
	if len(ids) == 0 {
		log.Errorf("orm: write requires at least one id")
		return false, fmt.Errorf("orm: write requires at least one id")
	}
	updated := false
	hasRecordEvents := HasRecordEventHandlers(e.metadata.Name)
	var writePreImages map[string]RecordMap
	err := withRuntimeTxDB(ctx, e.db, func(txCtx context.Context) error {
		normalizedValues, err := runtimeNormalizeFalseValues(e.metadata, values)
		if err != nil {
			return err
		}
		values = normalizedValues
		if err := ensureModelAccess(txCtx, EnvironmentFromContext(txCtx), e.metadata, "write", ids, values); err != nil {
			return err
		}
		if hasRecordEvents {
			rows, err := e.RuntimeRead(txCtx, ids, nil)
			if err != nil {
				return err
			}
			writePreImages = recordMapsByID(rows)
		}
		directValues, relationalValues, err := splitRuntimeValuesCore(e.metadata, values)
		if err != nil {
			return err
		}
		if err := dynamicGuardNoRelationalValues(e.metadata, relationalValues); err != nil {
			return err
		}
		directValues, translatedValues := splitTranslatedValues(e.metadata, directValues)
		db := runtimeDBFallback(txCtx, e.db)
		if len(directValues) > 0 {
			result, err := bunUpdateWithDB(txCtx, db, e.tableName, e.typ, RecordMap{"id": idsToFilterValues(ids)}, directValues)
			if err != nil {
				return translateConstraintViolation(txCtx, err)
			}
			updated = result.ModifiedCount > 0
		}
		if err := applyTranslatedValues(txCtx, db, e.metadata, ids, translatedValues, false); err != nil {
			return err
		}
		updated = updated || len(translatedValues) > 0
		if err := runConstraintsCore(txCtx, e.metadata, ids, values); err != nil {
			return err
		}
		if err := e.store().recomputeStored(txCtx, db, ids, values); err != nil {
			return err
		}
		if env := EnvironmentFromContext(txCtx); env != nil {
			env.Invalidate(e.metadata.Name, ids)
		}
		return nil
	})
	changed := updated || len(values) > 0
	if err == nil && changed && hasRecordEvents {
		postRows, readErr := e.RuntimeRead(ctx, ids, nil)
		if readErr != nil {
			log.Errorf("orm: post-write record event read for %s failed: %v", e.metadata.Name, readErr)
		} else {
			postImages := recordMapsByID(postRows)
			events := make([]recordEvent, 0, 2*len(ids))
			for _, id := range ids {
				events = append(events,
					recordEvent{name: RecordEventBeforeWrite, id: id, record: writePreImages[id]},
					recordEvent{name: RecordEventAfterWrite, id: id, record: postImages[id]})
			}
			fireRecordEventsAfterCommit(ctx, e.metadata.Name, events)
		}
	}
	return changed, err
}

// RuntimeUnlink mirrors Handler[T].RuntimeUnlink.
func (e *dynamicRuntimeExecutor) RuntimeUnlink(ctx context.Context, ids []string) (bool, error) {
	if len(ids) == 0 {
		log.Errorf("orm: unlink requires at least one id")
		return false, fmt.Errorf("orm: unlink requires at least one id")
	}
	deleted := false
	hasRecordEvents := HasRecordEventHandlers(e.metadata.Name)
	var unlinkPreImages map[string]RecordMap
	err := withRuntimeTxDB(ctx, e.db, func(txCtx context.Context) error {
		if err := ensureModelAccess(txCtx, EnvironmentFromContext(txCtx), e.metadata, "unlink", ids, nil); err != nil {
			return err
		}
		if hasRecordEvents {
			rows, err := e.RuntimeRead(txCtx, ids, nil)
			if err != nil {
				return err
			}
			unlinkPreImages = recordMapsByID(rows)
		}
		db := runtimeDBFallback(txCtx, e.db)
		result, err := bunDeleteWithDB(txCtx, db, e.tableName, e.typ, RecordMap{"id": idsToFilterValues(ids)})
		if err != nil {
			return err
		}
		deleted = result.DeletedCount > 0
		if env := EnvironmentFromContext(txCtx); env != nil {
			env.Invalidate(e.metadata.Name, ids)
		}
		return nil
	})
	// Unlink has no post-image, so before_unlink and after_unlink both carry
	// the same pre-delete snapshot -- fired only after commit, same audit
	// invariant as Handler[T].RuntimeUnlink.
	if err == nil && deleted && hasRecordEvents {
		events := make([]recordEvent, 0, 2*len(ids))
		for _, id := range ids {
			snapshot := unlinkPreImages[id]
			events = append(events,
				recordEvent{name: RecordEventBeforeUnlink, id: id, record: snapshot},
				recordEvent{name: RecordEventAfterUnlink, id: id, record: snapshot})
		}
		fireRecordEventsAfterCommit(ctx, e.metadata.Name, events)
	}
	return deleted, err
}

// recomputeStoredFields satisfies the unexported interface
// recomputeInverseStoredCore type-asserts for on whatever value is
// registered as a model's runtimeExecutor (see runtimeStore.recomputeStoredFields's
// doc comment) -- a static model recomputing a related field stored on a
// published custom model needs this to be present here too.
func (e *dynamicRuntimeExecutor) recomputeStoredFields(ctx context.Context, ids []string, targetFields []string, changedFields []string) error {
	db := runtimeDBFallback(ctx, e.db)
	return e.store().recomputeStoredFields(ctx, db, ids, targetFields, changedFields)
}

// bunFindRecordMaps is the reflect-based equivalent of BunRepository[T].Find,
// returning RecordMap instead of []T since a published model has no
// compile-time struct type a caller could use.
func bunFindRecordMaps(ctx context.Context, db bun.IDB, tableName string, typ reflect.Type, filter RecordMap) ([]RecordMap, error) {
	resultsPtr := reflect.New(reflect.SliceOf(typ))
	query := db.NewSelect().Model(resultsPtr.Interface())
	query = applyModelTable(query, tableName, typ)
	query = applyFilters(query, filter, typ)
	if err := query.Scan(ctx); err != nil {
		return nil, err
	}
	slice := resultsPtr.Elem()
	out := make([]RecordMap, 0, slice.Len())
	for i := 0; i < slice.Len(); i++ {
		record, err := toRecordMap(slice.Index(i).Interface())
		if err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	return out, nil
}

// bunFindOneRecordMap is the reflect-based equivalent of
// BunRepository[T].FindOne.
func bunFindOneRecordMap(ctx context.Context, db bun.IDB, tableName string, typ reflect.Type, filter RecordMap) (RecordMap, error) {
	resultPtr := reflect.New(typ)
	query := db.NewSelect().Model(resultPtr.Interface()).Limit(1)
	query = applyModelTable(query, tableName, typ)
	query = applyFilters(query, filter, typ)
	if err := query.Scan(ctx); err != nil {
		return nil, err
	}
	return toRecordMap(resultPtr.Interface())
}
