package orm

// File overview:
// - Responsibility: runtime write path (RuntimeWrite) and its record-by-id helper.
// - Key symbols: `recordMapsByID`.

import (
	"context"
	"fmt"

	"git.thebase.vn/base_hub/internal/services/log"
)

func (h *Handler[T]) RuntimeWrite(ctx context.Context, ids []string, values RecordMap) (bool, error) {
	if len(ids) == 0 {
		log.Errorf("orm: write requires at least one id")
		return false, fmt.Errorf("orm: write requires at least one id")
	}
	updated := false
	hasRecordEvents := HasRecordEventHandlers(h.metadata.Name)
	// Pre-image captured before the UPDATE statement runs, only when a model
	// trigger actually targets this model — the common case (no trigger)
	// pays nothing beyond the RLock'd HasRecordEventHandlers lookup above.
	var writePreImages map[string]RecordMap
	err := withRuntimeTx(ctx, h, func(txCtx context.Context) error {
		normalizedValues, err := runtimeNormalizeFalseValues(h.metadata, values)
		if err != nil {
			return err
		}
		values = normalizedValues
		if err := ensureModelAccess(txCtx, EnvironmentFromContext(txCtx), h.metadata, "write", ids, values); err != nil {
			return err
		}
		if hasRecordEvents {
			rows, err := h.RuntimeRead(txCtx, ids, nil)
			if err != nil {
				return err
			}
			writePreImages = recordMapsByID(rows)
		}
		directValues, relationalValues, err := h.splitRuntimeValues(values)
		if err != nil {
			return err
		}
		// Translated columns are jsonb (SRS-F-021): they leave the generic Bun
		// UPDATE and are written with language-scoped jsonb_set statements so a
		// write under one lang never clobbers the other languages' values.
		directValues, translatedValues := splitTranslatedValues(h.metadata, directValues)
		if len(directValues) > 0 {
			result, err := h.repo.updateWithDB(txCtx, runtimeDB(txCtx, h), RecordMap{"id": idsToFilterValues(ids)}, directValues)
			if err != nil {
				return err
			}
			updated = result.ModifiedCount > 0
		}
		if err := applyTranslatedValues(txCtx, runtimeDB(txCtx, h), h.metadata, ids, translatedValues, false); err != nil {
			return err
		}
		updated = updated || len(translatedValues) > 0
		if err := h.applyRelationalValues(txCtx, ids, relationalValues); err != nil {
			return err
		}
		if err := h.runConstraints(txCtx, ids, values); err != nil {
			return err
		}
		if err := h.recomputeStored(txCtx, ids, values); err != nil {
			return err
		}
		if env := EnvironmentFromContext(txCtx); env != nil {
			env.Invalidate(h.metadata.Name, ids)
		}
		return nil
	})
	changed := updated || len(values) > 0
	// Post-image read and event dispatch happen strictly after commit, same
	// audit invariant as RuntimeCreate: never notify a workflow about a write
	// that could still be rolled back by the transaction that produced it.
	if err == nil && changed && hasRecordEvents {
		postRows, readErr := h.RuntimeRead(ctx, ids, nil)
		if readErr != nil {
			log.Errorf("orm: post-write record event read for %s failed: %v", h.metadata.Name, readErr)
		} else {
			postImages := recordMapsByID(postRows)
			events := make([]recordEvent, 0, 2*len(ids))
			for _, id := range ids {
				events = append(events,
					recordEvent{name: RecordEventBeforeWrite, id: id, record: writePreImages[id]},
					recordEvent{name: RecordEventAfterWrite, id: id, record: postImages[id]})
			}
			fireRecordEventsAfterCommit(ctx, h.metadata.Name, events)
		}
	}
	return changed, err
}

// recordMapsByID indexes read-back rows by their string id so per-id
// record-event dispatch (RuntimeWrite/RuntimeUnlink) can look up each
// record's snapshot in O(1) instead of re-scanning the slice per id.
func recordMapsByID(rows []RecordMap) map[string]RecordMap {
	byID := make(map[string]RecordMap, len(rows))
	for _, row := range rows {
		id, ok := runtimeStringID(row["id"])
		if !ok {
			continue
		}
		byID[id] = row
	}
	return byID
}
