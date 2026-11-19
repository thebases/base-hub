package orm

// File overview:
// - Responsibility: generic `use_sequence` field rule. A field tagged `use_sequence` is filled
//   from the sys.sequence row matching its code on create, and previewed in default_get.
// - Key symbols: `sequenceCodeForField`, `applySequenceFields`, `previewSequenceFields`.
// - Code convention: `<model name>.<field name>` unless the field sets `sequence_code`.

import (
	"context"
	"fmt"

	"git.thebase.vn/base_hub/internal/services/log"
)

const sysSequenceModel = "sys.sequence"

// sequenceCodeForField resolves the sys.sequence code a use_sequence field draws from.
func sequenceCodeForField(model *ModelMetadata, field FieldMetadata) string {
	if field.SequenceCode != "" {
		return field.SequenceCode
	}
	return model.Name + "." + field.Name
}

// applySequenceFields claims the next number for every use_sequence field on create. A client
// value is always replaced: the form only ever holds a non-reserved preview, which can be stale
// by save time, and issuing here keeps numbers unique and (for no_gap) rollback-safe.
func applySequenceFields(ctx context.Context, model *ModelMetadata, values RecordMap) error {
	for _, field := range model.Fields {
		if !field.UseSequence {
			continue
		}
		code := sequenceCodeForField(model, field)
		result, err := ExecuteRuntimeMethod(ctx, sysSequenceModel, "next_by_code", nil, []any{code}, nil)
		if err != nil {
			return fmt.Errorf("%s.%s: issue sequence %q: %w", model.Name, field.Name, code, err)
		}
		number, ok := result.(string)
		if !ok || number == "" {
			return fmt.Errorf("%s.%s: sequence %q returned an unexpected result", model.Name, field.Name, code)
		}
		values[field.Name] = number
	}
	return nil
}

// previewSequenceFields adds the upcoming (unreserved) number of each requested use_sequence
// field to a default_get result. A missing sequence is skipped so forms still open.
func previewSequenceFields(ctx context.Context, model *ModelMetadata, requested map[string]struct{}, defaults map[string]any) {
	for _, field := range model.Fields {
		if !field.UseSequence {
			continue
		}
		if _, ok := requested[field.Name]; len(requested) > 0 && !ok {
			continue
		}
		result, err := ExecuteRuntimeMethod(ctx, sysSequenceModel, "peek_by_code", nil, []any{sequenceCodeForField(model, field)}, nil)
		if err != nil {
			log.Warnf("%s.%s: sequence preview failed: %v", model.Name, field.Name, err)
			continue
		}
		if preview, ok := result.(string); ok && preview != "" {
			defaults[field.Name] = preview
		}
	}
}
