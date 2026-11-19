// repository_handler_enrich.go: read-side enrichment of runtime records (many2one/x2many/related field resolution), split out of repository_handler_read.go.
package orm

import (
	"context"
	"fmt"
	"strings"

	"git.thebase.vn/base_hub/internal/services/log"
)

func (h *Handler[T]) enrichRuntimeRecords(ctx context.Context, records []RecordMap, fields []string) ([]RecordMap, error) {
	if len(records) == 0 {
		return records, nil
	}
	needAll := len(fields) == 0
	recordIDs := make([]string, 0, len(records))
	for _, record := range records {
		if id, ok := runtimeStringID(record["id"]); ok {
			recordIDs = append(recordIDs, id)
		}
	}
	env := EnvironmentFromContext(ctx)
	for _, field := range h.metadata.Fields {
		if !needAll && !containsString(fields, field.Name) {
			continue
		}
		// A related:/compute field has no stored column to display-resolve, so whatever its kind it
		// must take the resolve path below; the chain read already returns [id, name] / x2many tuples.
		kind := field.Kind
		if len(field.Related) > 0 {
			kind = ""
		}
		switch kind {
		case FieldKindManyToOne, FieldKindOneToOne:
			prefetched, err := h.prefetchManyToOneDisplays(ctx, field, records)
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
						env.CacheField(h.metadata.Name, recordID, field.Name, false)
					}
					continue
				}
				relationID, ok := runtimeStringID(value)
				if !ok {
					record[field.Name] = false
					if env != nil {
						env.CacheField(h.metadata.Name, recordID, field.Name, false)
					}
					continue
				}
				if display, exists := prefetched[relationID]; exists {
					record[field.Name] = display
				} else {
					record[field.Name] = h.resolveManyToOneDisplay(ctx, field, value)
				}
				if env != nil {
					env.CacheField(h.metadata.Name, recordID, field.Name, record[field.Name])
				}
			}
		case FieldKindOneToMany, FieldKindManyToMany:
			for _, record := range records {
				recordID, ok := runtimeStringID(record["id"])
				if !ok {
					continue
				}
				if env != nil {
					if cached, ok := env.CachedField(h.metadata.Name, recordID, field.Name); ok {
						record[field.Name] = cached
					}
				}
			}
			prefetched, err := h.prefetchX2ManyIDs(ctx, field, recordIDs)
			if err != nil {
				return nil, err
			}
			relationIDs := make([]string, 0, len(recordIDs))
			for _, parentID := range recordIDs {
				relationIDs = append(relationIDs, prefetched[parentID]...)
			}
			displays, err := prefetchRelationDisplaysByIDs(ctx, field.Relation, relationIDs)
			if err != nil {
				return nil, err
			}
			for _, record := range records {
				id, _ := runtimeStringID(record["id"])
				if cached, ok := record[field.Name].([]string); ok && len(cached) > 0 && prefetched[id] == nil {
					continue
				}
				relatedIDs := prefetched[id]
				if len(relatedIDs) == 0 {
					fallbackIDs, err := h.readX2ManyIDs(ctx, field, id)
					if err != nil {
						return nil, err
					}
					relatedIDs = fallbackIDs
				}
				if len(relatedIDs) == 0 {
					record[field.Name] = false
					continue
				}
				tuples := make([][2]any, 0, len(relatedIDs))
				for _, relatedID := range relatedIDs {
					if display, ok := displays[relatedID]; ok {
						tuples = append(tuples, display)
						continue
					}
					tuples = append(tuples, [2]any{toInt64(relatedID), relatedID})
				}
				record[field.Name] = tuples
				if env != nil {
					env.CacheField(h.metadata.Name, id, field.Name, append([]string(nil), relatedIDs...))
				}
			}
		default:
			if field.Compute == "" && len(field.Related) == 0 {
				continue
			}
			missingIDs := append([]string(nil), recordIDs...)
			if env != nil {
				missingIDs = missingIDs[:0]
				for _, record := range records {
					id, _ := runtimeStringID(record["id"])
					if cached, ok := env.CachedField(h.metadata.Name, id, field.Name); ok {
						record[field.Name] = cached
						continue
					}
					missingIDs = append(missingIDs, id)
				}
			}
			if len(missingIDs) == 0 {
				continue
			}
			resolved, err := h.resolveFieldValues(ctx, field, missingIDs)
			if err != nil {
				return nil, err
			}
			for _, record := range records {
				id, _ := runtimeStringID(record["id"])
				if value, ok := resolved[id]; ok {
					record[field.Name] = value
					if env != nil {
						env.CacheField(h.metadata.Name, id, field.Name, value)
					}
				}
			}
		}
	}
	return records, nil
}

// resolveFieldValues resolves the field values handled by this package.
func (h *Handler[T]) resolveFieldValues(ctx context.Context, field FieldMetadata, ids []string) (map[string]any, error) {
	return resolveFieldValuesCore(ctx, h.metadata, field, ids)
}

// resolveFieldValuesCore is the shared implementation behind
// Handler[T].resolveFieldValues and dynamicRuntimeExecutor's equivalent.
func resolveFieldValuesCore(ctx context.Context, meta *ModelMetadata, field FieldMetadata, ids []string) (map[string]any, error) {
	if len(field.Related) > 0 {
		return resolveRelatedValuesCore(ctx, meta, field, ids)
	}
	runtimeHookRegistry.mu.RLock()
	handler := runtimeHookRegistry.computes[meta.Name][field.Name]
	runtimeHookRegistry.mu.RUnlock()
	if handler == nil {
		return map[string]any{}, nil
	}
	return handler(ctx, NewRecordSet(meta, EnvironmentFromContext(ctx), ids), field)
}

// resolveRelatedValuesCore is the shared implementation behind
// Handler[T] and dynamicRuntimeExecutor.
func resolveRelatedValuesCore(ctx context.Context, meta *ModelMetadata, field FieldMetadata, ids []string) (map[string]any, error) {
	out := make(map[string]any, len(ids))
	if len(field.Related) == 0 || len(ids) == 0 {
		return out, nil
	}

	// Walk the related chain in grouped Bun-backed reads so repeated relational
	// access within one request reuses the shared environment cache.
	currentModel := meta
	currentIDsByOrigin := make(map[string]string, len(ids))
	for _, id := range ids {
		currentIDsByOrigin[id] = id
	}
	for index, segment := range field.Related {
		stepIDs := uniqueNonEmptyValues(currentIDsByOrigin)
		if len(stepIDs) == 0 {
			break
		}
		valuesByID, err := runtimeReadFieldMap(ctx, currentModel, stepIDs, segment)
		if err != nil {
			return nil, err
		}
		if index == len(field.Related)-1 {
			for originID, currentID := range currentIDsByOrigin {
				out[originID] = valuesByID[currentID]
			}
			break
		}
		nextField, err := currentModel.Field(segment)
		if err != nil || (nextField.Kind != FieldKindManyToOne && nextField.Kind != FieldKindOneToOne) {
			break
		}
		nextModel, ok := LookupSharedModel(nextField.Relation)
		if !ok {
			break
		}
		nextIDsByOrigin := make(map[string]string, len(currentIDsByOrigin))
		for originID, currentID := range currentIDsByOrigin {
			nextID, ok := relatedRecordID(valuesByID[currentID])
			if ok {
				nextIDsByOrigin[originID] = nextID
				continue
			}
			nextIDsByOrigin[originID] = ""
		}
		currentModel = nextModel
		currentIDsByOrigin = nextIDsByOrigin
	}
	return out, nil
}

// runtimeReadFieldMap reads one logical field for a grouped id set and keeps
// the result in the request-scoped environment cache for later reuse.
func runtimeReadFieldMap(ctx context.Context, model *ModelMetadata, ids []string, fieldName string) (map[string]any, error) {
	out := make(map[string]any, len(ids))
	if model == nil || len(ids) == 0 || strings.TrimSpace(fieldName) == "" {
		return out, nil
	}
	env := EnvironmentFromContext(ctx)
	missingIDs := make([]string, 0, len(ids))
	cachedValues := env.CachedFields(model.Name, fieldName, ids)
	for _, id := range ids {
		if cached, ok := cachedValues[id]; ok {
			out[id] = cached
			continue
		}
		missingIDs = append(missingIDs, id)
	}
	if len(missingIDs) == 0 {
		return out, nil
	}
	executor, ok := lookupRuntimeExecutor(model.Name)
	if !ok {
		log.Errorf("orm: no runtime executor registered for model %q", model.Name)
		return out, fmt.Errorf("orm: no runtime executor registered for model %q", model.Name)
	}
	rows, err := executor.RuntimeRead(ctx, missingIDs, []string{"id", fieldName})
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		id, ok := runtimeStringID(row["id"])
		if !ok {
			continue
		}
		out[id] = row[fieldName]
		if env != nil {
			env.CacheFields(model.Name, id, map[string]any{fieldName: row[fieldName]})
		}
	}
	return out, nil
}

func uniqueNonEmptyValues(values map[string]string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
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

func relatedRecordID(value any) (string, bool) {
	if id, ok := runtimeStringID(value); ok {
		return id, true
	}
	switch typed := value.(type) {
	case [2]any:
		return runtimeStringID(typed[0])
	case []any:
		if len(typed) == 0 {
			return "", false
		}
		return runtimeStringID(typed[0])
	default:
		return "", false
	}
}
