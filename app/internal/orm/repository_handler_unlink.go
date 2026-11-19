package orm

// File overview:
// - Responsibility: runtime unlink and active-toggle paths.
// - Key symbols: `runtimeToggleDocumentState`.

import (
	"context"
	"fmt"

	"git.thebase.vn/base_hub/internal/services/log"
)

// RuntimeToggleActive loads concrete documents so embedded BaseModel instances
// can apply ToggleActive before the handler persists the resulting active state.
func (h *Handler[T]) RuntimeToggleActive(ctx context.Context, ids []string) (bool, error) {
	if len(ids) == 0 {
		log.Errorf("orm: toggle_active requires at least one id")
		return false, fmt.Errorf("orm: toggle_active requires at least one id")
	}

	activeField, err := runtimeActiveFieldName(h.metadata)
	if err != nil {
		return false, err
	}

	updated := false
	err = withRuntimeTx(ctx, h, func(txCtx context.Context) error {
		// Reuse the standard record-rule check so toggle_active keeps the same
		// row-level protections as the generic write runtime method.
		if err := ensureRecordRuleAccess(txCtx, h.metadata, "write", ids); err != nil {
			return err
		}

		documents, err := h.runtimeSearchDocuments(txCtx, RecordMap{"id": idsToFilterValues(ids)}, TrueDomain(), "id asc", 0, 0, "")
		if err != nil {
			return err
		}

		for _, document := range documents {
			recordID, nextState, err := runtimeToggleDocumentState(document, activeField)
			if err != nil {
				return err
			}

			result, err := h.repo.updateWithDB(txCtx, runtimeDB(txCtx, h), RecordMap{"id": []any{recordID}}, RecordMap{activeField: nextState})
			if err != nil {
				return err
			}
			updated = updated || result.ModifiedCount > 0
		}
		if env := EnvironmentFromContext(txCtx); env != nil {
			env.Invalidate(h.metadata.Name, ids)
		}
		return nil
	})
	return updated, err
}

func (h *Handler[T]) RuntimeUnlink(ctx context.Context, ids []string) (bool, error) {
	if len(ids) == 0 {
		log.Errorf("orm: unlink requires at least one id")
		return false, fmt.Errorf("orm: unlink requires at least one id")
	}
	deleted := false
	hasRecordEvents := HasRecordEventHandlers(h.metadata.Name)
	var unlinkPreImages map[string]RecordMap
	err := withRuntimeTx(ctx, h, func(txCtx context.Context) error {
		if err := ensureModelAccess(txCtx, EnvironmentFromContext(txCtx), h.metadata, "unlink", ids, nil); err != nil {
			return err
		}
		if hasRecordEvents {
			rows, err := h.RuntimeRead(txCtx, ids, nil)
			if err != nil {
				return err
			}
			unlinkPreImages = recordMapsByID(rows)
		}
		result, err := h.repo.deleteWithDB(txCtx, runtimeDB(txCtx, h), RecordMap{"id": idsToFilterValues(ids)})
		if err != nil {
			return err
		}
		deleted = result.DeletedCount > 0
		if env := EnvironmentFromContext(txCtx); env != nil {
			env.Invalidate(h.metadata.Name, ids)
		}
		return nil
	})
	// Unlink has no post-image (the row is gone), so before_unlink and
	// after_unlink both carry the same pre-delete snapshot — fired only after
	// commit, same audit invariant as create/write.
	if err == nil && deleted && hasRecordEvents {
		events := make([]recordEvent, 0, 2*len(ids))
		for _, id := range ids {
			snapshot := unlinkPreImages[id]
			events = append(events,
				recordEvent{name: RecordEventBeforeUnlink, id: id, record: snapshot},
				recordEvent{name: RecordEventAfterUnlink, id: id, record: snapshot})
		}
		fireRecordEventsAfterCommit(ctx, h.metadata.Name, events)
	}
	return deleted, err
}

// runtimeToggleDocumentState resolves one document's id and next active state,
// preferring the document's ToggleActive hook when the standard active field is
// backed by an embedded BaseModel.
func runtimeToggleDocumentState[T any](document T, activeField string) (any, bool, error) {
	recordID := extractModelID(document)
	if isZeroModelID(recordID) {
		log.Errorf("orm: toggle_active requires a persisted record id")
		return nil, false, fmt.Errorf("orm: toggle_active requires a persisted record id")
	}

	if activeField == "active" {
		// Use the model hook when available so shared BaseModel toggle semantics
		// remain centralized on the concrete document type.
		if toggler, ok := any(&document).(interface{ ToggleActive() bool }); ok {
			return recordID, toggler.ToggleActive(), nil
		}
	}

	record, err := toRecordMap(document)
	if err != nil {
		return nil, false, err
	}
	current, _ := record[activeField].(bool)
	return recordID, !current, nil
}
