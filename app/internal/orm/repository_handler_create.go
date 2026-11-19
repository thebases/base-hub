package orm

// File overview:
// - Responsibility: runtime create path: defaults, value splitting and per-kind normalisation, relational values, constraints and stored-field recompute.
// - Key symbols: `runtimeApplyDefaultsCore`, `splitRuntimeValuesCore`, `normalizeRuntimeFieldValue`, `unixSecondsInput`.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"git.thebase.vn/base_hub/internal/services/log"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/uptrace/bun"
)

func (h *Handler[T]) RuntimeCreate(ctx context.Context, values RecordMap) (RecordMap, error) {
	var created RecordMap
	// Captured inside the tx closure so the post-commit before_create event can
	// carry the pre-image (defaults-applied, not-yet-persisted) values without
	// re-reading them.
	var createPreImage RecordMap

	err := withRuntimeTx(ctx, h, func(txCtx context.Context) error {
		var err error
		created, createPreImage, err = h.runtimeCreateInTx(txCtx, values)
		return err
	})
	// Fire record-lifecycle events only after the transaction has committed —
	// never from inside the closure above — so a workflow can never be
	// notified about a create that a later error in the same transaction
	// rolled back. Guarded by HasRecordEventHandlers so models with no
	// registered model trigger pay nothing beyond one RLock'd map lookup.
	if err == nil && HasRecordEventHandlers(h.metadata.Name) {
		id, _ := runtimeStringID(created["id"])
		fireRecordEventsAfterCommit(ctx, h.metadata.Name, []recordEvent{
			{name: RecordEventBeforeCreate, id: id, record: createPreImage},
			{name: RecordEventAfterCreate, id: id, record: created},
		})
	}
	return created, err
}

// runtimeCreateInTx runs the create steps inside the open transaction:
// normalize and authorize, apply defaults, insert the row, then finalize.
// It returns the created record and the defaults-applied pre-image.
func (h *Handler[T]) runtimeCreateInTx(txCtx context.Context, values RecordMap) (created, preImage RecordMap, err error) {
	values, err = runtimeNormalizeFalseValues(h.metadata, values)
	if err != nil {
		return nil, nil, err
	}
	if err := ensureModelAccess(txCtx, EnvironmentFromContext(txCtx), h.metadata, "create", nil, values); err != nil {
		return nil, nil, err
	}
	nextValues, err := h.runtimeApplyDefaults(txCtx, values)
	if err != nil {
		return nil, nil, err
	}
	directValues, relationalValues, err := h.splitRuntimeValues(nextValues)
	if err != nil {
		return nil, nil, err
	}
	// Translated columns are jsonb (SRS-F-021): a plain-string payload must never reach the
	// generic Bun INSERT as-is. They are split out and re-added as the JSON-text language
	// envelope inside the same INSERT, so NOT NULL translated columns hold their value from
	// the first statement; columns without a payload stay excluded (NULL).
	directValues, translatedValues := splitTranslatedValues(h.metadata, directValues)
	envelopes, excludedTranslated, err := translatedInsertValues(txCtx, h.metadata, translatedValues)
	if err != nil {
		return nil, nil, err
	}
	if len(envelopes) > 0 && directValues == nil {
		directValues = RecordMap{}
	}
	for name, envelope := range envelopes {
		directValues[name] = envelope
	}
	translatedValues = nil
	document, err := h.toDocument(directValues)
	if err != nil {
		return nil, nil, err
	}

	inserted, err := h.repo.insertWithDB(txCtx, runtimeDB(txCtx, h), document, append(excludedTranslated, computedVirtualColumns(h.metadata)...)...)
	if err != nil {
		return nil, nil, err
	}
	result, err := toRecordMap(document)
	if err != nil {
		return nil, nil, err
	}
	log.Debugf("creating %s with values: %+v", h.metadata.Name, document)
	if inserted != nil && !isZeroModelID(inserted.InsertedID) {
		result["id"] = inserted.InsertedID
	}
	if isZeroModelID(result["id"]) {
		log.Errorf("orm: create on %s did not return an inserted id", h.metadata.Name)
		return nil, nil, fmt.Errorf("orm: create on %s did not return an inserted id", h.metadata.Name)
	}
	id, _ := runtimeStringID(result["id"])
	created, err = h.runtimeFinalizeCreate(txCtx, id, nextValues, relationalValues, translatedValues, result)
	return created, nextValues, err
}

// runtimeFinalizeCreate applies everything that needs the new row's id:
// translated and relational values, constraints, stored computes, cache
// invalidation, and the read-back. A failed read-back keeps the insert result
// so bootstrap imports are not broken by model-specific SELECT quirks after
// the row has already been inserted and validated.
func (h *Handler[T]) runtimeFinalizeCreate(txCtx context.Context, id string, nextValues, relationalValues, translatedValues, result RecordMap) (RecordMap, error) {
	if err := applyTranslatedValues(txCtx, runtimeDB(txCtx, h), h.metadata, []string{id}, translatedValues, true); err != nil {
		return nil, err
	}
	if err := h.applyRelationalValues(txCtx, []string{id}, relationalValues); err != nil {
		return nil, err
	}
	if err := h.runConstraints(txCtx, []string{id}, nextValues); err != nil {
		return nil, err
	}
	if err := h.recomputeStored(txCtx, []string{id}, nextValues); err != nil {
		return nil, err
	}
	if env := EnvironmentFromContext(txCtx); env != nil {
		env.Invalidate(h.metadata.Name, []string{id})
	}
	records, err := h.RuntimeRead(txCtx, []string{id}, nil)
	return createReadBackResult(h.metadata.Name, id, records, err, result)
}

// createReadBackResult picks the create result after the read-back. A Go-side
// read-back failure keeps the insert result (bootstrap imports must survive
// model-specific enrichment quirks), but a Postgres error has aborted the
// transaction: reporting success then would hand out the id of a row the
// COMMIT silently rolls back (or fail the caller's next statement with 25P02),
// so that error is returned and the create fails visibly.
func createReadBackResult(modelName, id string, records []RecordMap, readErr error, inserted RecordMap) (RecordMap, error) {
	if readErr != nil {
		var pgErr *pgconn.PgError
		if errors.As(readErr, &pgErr) {
			log.Errorf("orm: create readback failed for %s[%s]: %v", modelName, id, readErr)
			return nil, fmt.Errorf("orm: create %s[%s]: read-back failed: %w", modelName, id, readErr)
		}
		log.Warnf("orm: create readback failed for %s[%s]: %v", modelName, id, readErr)
		return inserted, nil
	}
	if len(records) == 1 {
		return records[0], nil
	}
	return inserted, nil
}

func (h *Handler[T]) runtimeApplyDefaults(ctx context.Context, values RecordMap) (RecordMap, error) {
	return runtimeApplyDefaultsCore(ctx, h.metadata, values)
}

// runtimeApplyDefaultsCore is the shared implementation behind
// Handler[T].runtimeApplyDefaults and dynamicRuntimeExecutor's equivalent.
func runtimeApplyDefaultsCore(ctx context.Context, meta *ModelMetadata, values RecordMap) (RecordMap, error) {
	defaultsRaw, err := runtimeDefaultGet(ctx, meta, nil, []any{meta.FieldNames()}, nil)
	if err != nil {
		return nil, err
	}
	defaults, _ := defaultsRaw.(map[string]any)
	merged := RecordMap{}
	for key, value := range defaults {
		merged[key] = value
	}
	for key, value := range values {
		merged[key] = value
	}
	if err := applySequenceFields(ctx, meta, merged); err != nil {
		return nil, err
	}
	return merged, nil
}

func (h *Handler[T]) splitRuntimeValues(values RecordMap) (RecordMap, RecordMap, error) {
	return splitRuntimeValuesCore(h.metadata, values)
}

// splitRuntimeValuesCore is the shared implementation behind
// Handler[T].splitRuntimeValues and dynamicRuntimeExecutor's equivalent.
func splitRuntimeValuesCore(meta *ModelMetadata, values RecordMap) (RecordMap, RecordMap, error) {
	direct := RecordMap{}
	relational := RecordMap{}
	for key, value := range values {
		field, ok := runtimeLookupField(meta, key)
		if !ok {
			continue
		}
		// Non-stored computed fields must never reach INSERT/UPDATE statements
		// (SRS-F-018): the dispatch layer rejects client writes, and this skip
		// keeps internal handler callers from persisting handler-owned values
		// into a column the read path deliberately ignores. Non-stored related
		// fields have no column either (they resolve through their chain on
		// read), so a client echoing one back must not reach the UPDATE.
		if !field.Stored && (strings.TrimSpace(field.Compute) != "" || len(field.Related) > 0) {
			continue
		}
		switch field.Kind {
		case FieldKindOneToMany, FieldKindManyToMany:
			relational[field.Name] = value
		default:
			// Normalize scalar field payloads before they reach Bun so relation
			// tuples like [id, display_name] do not leak into bigint columns.
			direct[field.Name] = normalizeRuntimeFieldValue(field, value)
		}
	}
	return direct, relational, nil
}

func normalizeRuntimeFieldValue(field FieldMetadata, value any) any {
	switch field.Kind {
	case FieldKindBoolean:
		// Coerce API payloads into real booleans before Bun sees them so string
		// and numeric checkbox values do not drift into text/json storage paths.
		return normalizeRuntimeBooleanValue(value)
	case FieldKindID, FieldKindInteger:
		return normalizeRuntimeIntegerValue(value)
	case FieldKindManyToOne, FieldKindOneToOne:
		// Relation fields should never pass a raw boolean sentinel through to
		// SQL because PostgreSQL bigint foreign keys cannot store a boolean false.
		if boolValue, ok := value.(bool); ok && !boolValue {
			return nil
		}
		if relationID, ok := relatedRecordID(value); ok {
			return normalizeRuntimeIntegerValue(relationID)
		}
		return normalizeRuntimeIntegerValue(value)
	case FieldKindFloat:
		return normalizeRuntimeFloatValue(value)
	case FieldKindDate, FieldKindDateTime:
		if text, ok := value.(string); ok {
			text = strings.TrimSpace(text)
			if text == "" {
				return nil
			}
			// A date input sends a bare YYYY-MM-DD, which Bun's RFC3339-only time.Time decode
			// rejects; pin it to UTC midnight. Full timestamps pass through unchanged.
			if day, err := time.Parse("2006-01-02", text); err == nil {
				return day
			}
		}
		// Responses emit UTC Unix seconds, so accept the same integer back on write.
		if ts, ok := unixSecondsInput(value); ok {
			return time.Unix(ts, 0).UTC()
		}
	}
	return value
}

// unixSecondsInput reports whether value is a whole-number JSON/Go numeric usable as Unix seconds.
func unixSecondsInput(value any) (int64, bool) {
	switch typed := value.(type) {
	case int:
		return int64(typed), true
	case int64:
		return typed, true
	case float64:
		if typed == float64(int64(typed)) {
			return int64(typed), true
		}
	case json.Number:
		if parsed, err := typed.Int64(); err == nil {
			return parsed, true
		}
	}
	return 0, false
}

func normalizeRuntimeIntegerValue(value any) any {
	switch typed := value.(type) {
	case nil:
		return nil
	case int:
		return int64(typed)
	case int8:
		return int64(typed)
	case int16:
		return int64(typed)
	case int32:
		return int64(typed)
	case int64:
		return typed
	case uint:
		return int64(typed)
	case uint8:
		return int64(typed)
	case uint16:
		return int64(typed)
	case uint32:
		return int64(typed)
	case uint64:
		return int64(typed)
	case float32:
		// Only coerce whole-number floats so decimal payloads cannot be silently
		// truncated into foreign-key or integer columns.
		if typed == float32(int64(typed)) {
			return int64(typed)
		}
	case float64:
		// Only coerce whole-number floats so decimal payloads cannot be silently
		// truncated into foreign-key or integer columns.
		if typed == float64(int64(typed)) {
			return int64(typed)
		}
	case string:
		text := strings.TrimSpace(typed)
		if text == "" {
			return nil
		}
		if parsed, err := strconv.ParseInt(text, 10, 64); err == nil {
			return parsed
		}
	case json.Number:
		if parsed, err := typed.Int64(); err == nil {
			return parsed
		}
	}
	return value
}

func normalizeRuntimeBooleanValue(value any) any {
	switch typed := value.(type) {
	case nil:
		return nil
	case bool:
		return typed
	case string:
		text := strings.TrimSpace(typed)
		if text == "" {
			return nil
		}
		// Accept conventional string boolean payloads first so API clients can
		// send "true"/"false" without model-specific adapters.
		if parsed, err := strconv.ParseBool(text); err == nil {
			return parsed
		}
		// Some clients still serialize switches as "1"/"0", so honor those
		// narrow integer forms without guessing on larger numbers.
		if parsed, err := strconv.ParseInt(text, 10, 64); err == nil {
			switch parsed {
			case 0:
				return false
			case 1:
				return true
			}
		}
	case json.Number:
		// JSON numeric booleans should only coerce for explicit 0/1 payloads.
		if parsed, err := typed.Int64(); err == nil {
			switch parsed {
			case 0:
				return false
			case 1:
				return true
			}
		}
	case int:
		if typed == 0 {
			return false
		}
		if typed == 1 {
			return true
		}
	case int8:
		if typed == 0 {
			return false
		}
		if typed == 1 {
			return true
		}
	case int16:
		if typed == 0 {
			return false
		}
		if typed == 1 {
			return true
		}
	case int32:
		if typed == 0 {
			return false
		}
		if typed == 1 {
			return true
		}
	case int64:
		if typed == 0 {
			return false
		}
		if typed == 1 {
			return true
		}
	case uint:
		if typed == 0 {
			return false
		}
		if typed == 1 {
			return true
		}
	case uint8:
		if typed == 0 {
			return false
		}
		if typed == 1 {
			return true
		}
	case uint16:
		if typed == 0 {
			return false
		}
		if typed == 1 {
			return true
		}
	case uint32:
		if typed == 0 {
			return false
		}
		if typed == 1 {
			return true
		}
	case uint64:
		if typed == 0 {
			return false
		}
		if typed == 1 {
			return true
		}
	case float32:
		if typed == 0 {
			return false
		}
		if typed == 1 {
			return true
		}
	case float64:
		if typed == 0 {
			return false
		}
		if typed == 1 {
			return true
		}
	}
	return value
}

func normalizeRuntimeFloatValue(value any) any {
	if text, ok := value.(string); ok {
		trimmed := strings.TrimSpace(text)
		if trimmed == "" {
			return nil
		}
		if parsed, err := strconv.ParseFloat(trimmed, 64); err == nil {
			return parsed
		}
	}
	return value
}

func (h *Handler[T]) applyRelationalValues(ctx context.Context, ids []string, values RecordMap) error {
	if len(values) == 0 || h.repo == nil || h.repo.db == nil {
		return nil
	}
	for fieldName, raw := range values {
		field, ok := runtimeLookupField(h.metadata, fieldName)
		if !ok {
			continue
		}
		commands := normalizeX2ManyCommandsForField(field, raw)
		for _, id := range ids {
			switch field.Kind {
			case FieldKindManyToMany:
				if err := h.applyManyToManyCommands(ctx, field, id, commands); err != nil {
					return err
				}
			case FieldKindOneToMany:
				if err := h.applyOneToManyCommands(ctx, field, id, commands); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// runConstraints runs the constraints handled by this package.
func (h *Handler[T]) runConstraints(ctx context.Context, ids []string, values RecordMap) error {
	return runConstraintsCore(ctx, h.metadata, ids, values)
}

// runConstraintsCore is the shared implementation behind
// Handler[T].runConstraints and dynamicRuntimeExecutor's equivalent.
func runConstraintsCore(ctx context.Context, meta *ModelMetadata, ids []string, values RecordMap) error {
	runtimeHookRegistry.mu.RLock()
	constraints := append([]registeredConstraint(nil), runtimeHookRegistry.constraints[meta.Name]...)
	runtimeHookRegistry.mu.RUnlock()
	records := NewRecordSet(meta, EnvironmentFromContext(ctx), ids)
	changedFields := make([]string, 0, len(values))
	for key := range values {
		changedFields = append(changedFields, key)
	}
	for _, constraint := range constraints {
		if len(constraint.fields) > 0 && !runtimeStringSetsIntersect(constraint.fields, changedFields) {
			continue
		}
		if err := constraint.handler(ctx, records, values); err != nil {
			return err
		}
	}
	return nil
}

func (h *Handler[T]) recomputeStored(ctx context.Context, ids []string, values RecordMap) error {
	return h.store().recomputeStored(ctx, runtimeDB(ctx, h), ids, values)
}

// runtimeStore.recomputeStored is the shared implementation behind
// Handler[T].recomputeStored and dynamicRuntimeExecutor's equivalent.
func (s runtimeStore) recomputeStored(ctx context.Context, db bun.IDB, ids []string, values RecordMap) error {
	meta := s.meta
	if len(ids) == 0 {
		return nil
	}
	changedFields := make([]string, 0, len(values))
	for key := range values {
		changedFields = append(changedFields, key)
	}
	if err := s.recomputeStoredFields(ctx, db, ids, nil, changedFields); err != nil {
		return err
	}
	if err := recomputeInverseStoredCore(ctx, meta, ids, changedFields); err != nil {
		return err
	}
	return nil
}

// recomputeStoredFields recalculates a selected stored-field subset, or the
// full affected stored-field set when targetFields is empty.
func (h *Handler[T]) recomputeStoredFields(ctx context.Context, ids []string, targetFields []string, changedFields []string) error {
	return h.store().recomputeStoredFields(ctx, runtimeDB(ctx, h), ids, targetFields, changedFields)
}

// runtimeStore.recomputeStoredFields is the shared implementation behind
// Handler[T].recomputeStoredFields and dynamicRuntimeExecutor's equivalent.
// recomputeInverseStoredCore type-asserts for this exact method name/signature
// on whatever value is registered as the model's runtimeExecutor, so both
// Handler[T] and dynamicRuntimeExecutor must keep exposing it directly.
func (s runtimeStore) recomputeStoredFields(ctx context.Context, db bun.IDB, ids []string, targetFields []string, changedFields []string) error {
	meta, tableName, typ := s.meta, s.table, s.typ
	if len(ids) == 0 {
		return nil
	}
	targetSet := make(map[string]struct{}, len(targetFields))
	for _, fieldName := range targetFields {
		targetSet[fieldName] = struct{}{}
	}
	for _, field := range meta.Fields {
		if !field.Stored || (field.Compute == "" && len(field.Related) == 0) {
			continue
		}
		if len(targetSet) > 0 {
			if _, ok := targetSet[field.Name]; !ok {
				continue
			}
		} else if len(field.Depends) > 0 && !runtimeStringSetsIntersect(field.Depends, changedFields) {
			continue
		}
		resolved, err := resolveFieldValuesCore(ctx, meta, field, ids)
		if err != nil {
			return err
		}
		for id, value := range resolved {
			if _, err := bunUpdateWithDB(ctx, db, tableName, typ, RecordMap{"id": []any{id}}, RecordMap{field.Name: value}); err != nil {
				return err
			}
			if env := EnvironmentFromContext(ctx); env != nil {
				env.CacheField(meta.Name, id, field.Name, value)
			}
		}
	}
	return nil
}

// recomputeInverseStoredCore is the shared implementation behind
// Handler[T] and dynamicRuntimeExecutor.
func recomputeInverseStoredCore(ctx context.Context, meta *ModelMetadata, sourceIDs []string, changedFields []string) error {
	if len(sourceIDs) == 0 || len(changedFields) == 0 {
		return nil
	}
	// Walk each registeredmodels() entry in order so validation and side effects stay scoped to the current element.
	for _, targetMeta := range RegisteredModels() {
		if targetMeta.Name == meta.Name {
			continue
		}
		targetFieldsByLink := map[string][]string{}
		for _, targetField := range targetMeta.Fields {
			if !targetField.Stored || len(targetField.Related) < 2 {
				continue
			}
			linkField, err := targetMeta.Field(targetField.Related[0])
			if err != nil {
				continue
			}
			// Only inverse many2one chains can be recomputed from one source-row set.
			if (linkField.Kind != FieldKindManyToOne && linkField.Kind != FieldKindOneToOne) || linkField.Relation != meta.Name {
				continue
			}
			if !containsString(changedFields, targetField.Related[1]) {
				continue
			}
			targetFieldsByLink[linkField.Name] = append(targetFieldsByLink[linkField.Name], targetField.Name)
		}
		if len(targetFieldsByLink) == 0 {
			continue
		}
		targetExecutor, ok := lookupRuntimeExecutor(targetMeta.Name)
		if !ok {
			continue
		}
		recomputer, ok := targetExecutor.(interface {
			recomputeStoredFields(context.Context, []string, []string, []string) error
		})
		if !ok {
			continue
		}
		for linkFieldName, targetFields := range targetFieldsByLink {
			rows, err := targetExecutor.RuntimeSearchRead(ctx, RecordMap{
				linkFieldName: idsToFilterValues(sourceIDs),
				"__order":     "id asc",
			}, []string{"id"})
			if err != nil {
				return err
			}
			targetIDs := make([]string, 0, len(rows))
			for _, row := range rows {
				id, ok := runtimeStringID(row["id"])
				if ok {
					targetIDs = append(targetIDs, id)
				}
			}
			if len(targetIDs) == 0 {
				continue
			}
			if err := recomputer.recomputeStoredFields(ctx, targetIDs, targetFields, nil); err != nil {
				return err
			}
			if env := EnvironmentFromContext(ctx); env != nil {
				env.Invalidate(targetMeta.Name, targetIDs)
			}
		}
	}
	return nil
}
