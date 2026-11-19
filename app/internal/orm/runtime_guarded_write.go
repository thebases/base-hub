package orm

// File overview:
// - Responsibility: the one guarded create/write sequence (plan T87, continues T15). The default
//   `create`/`write` runtime methods call it, and model overrides call it instead of
//   RuntimeRepositoryCreate/Write so they keep the record-rule, computed-field and company guards.
// - Key symbols: `WriteOp`, `CreateRecord`, `WriteRecords`, `ModelSpec.Prepare`.

import (
	"context"
	"fmt"

	"git.thebase.vn/base_hub/internal/services/log"
)

// WriteOp is what ModelSpec.Prepare sees: the target model, the operation, the ids of a write,
// and the normalised payload. Prepare may mutate Values in place.
type WriteOp struct {
	Model  *ModelMetadata
	Create bool
	IDs    []string
	Values RecordMap
}

// CreateRecord runs the guarded default create: payload normalisation, computed-field rejection,
// ModelSpec.Prepare, company pinning from the environment, company validation, then the repository
// insert (test stub seam and constraint-message translation included, like RuntimeRepositoryCreate).
// Sudo and identity-less callers skip only what the guards themselves skip for them today.
func CreateRecord(ctx context.Context, model *ModelMetadata, values RecordMap) (RecordMap, error) {
	if values == nil {
		log.Errorf("orm: create requires values")
		return nil, fmt.Errorf("orm: create requires values")
	}
	values, err := prepareGuardedValues(ctx, WriteOp{Model: model, Create: true, Values: values})
	if err != nil {
		return nil, err
	}
	if err := applyImplicitCreateCompanyValue(ctx, model, values); err != nil {
		return nil, err
	}
	if err := validateImplicitCompanyAccess(ctx, model, values, "create"); err != nil {
		return nil, err
	}
	return RuntimeRepositoryCreate(ctx, model, values)
}

// WriteRecords runs the guarded default write: record-rule check on ids (skipped for sudo and
// identity-less callers on models without an explicit rule handler, as ensureRecordRuleAccess
// always did), payload normalisation, computed-field rejection, ModelSpec.Prepare, company
// validation, then the repository update through the RuntimeRepositoryWrite path.
func WriteRecords(ctx context.Context, model *ModelMetadata, ids []string, values RecordMap) (bool, error) {
	// The rule check comes first so a caller learns nothing about the payload rules of rows it
	// cannot write.
	if err := ensureRecordRuleAccess(ctx, model, "write", ids); err != nil {
		return false, err
	}
	if values == nil {
		log.Errorf("orm: write requires values")
		return false, fmt.Errorf("orm: write requires values")
	}
	values, err := prepareGuardedValues(ctx, WriteOp{Model: model, IDs: ids, Values: values})
	if err != nil {
		return false, err
	}
	if err := validateImplicitCompanyAccess(ctx, model, values, "write"); err != nil {
		return false, err
	}
	return RuntimeRepositoryWrite(ctx, model, ids, values)
}

// prepareGuardedValues is the shared front half of both sequences. Prepare runs after
// normalisation (it sees canonical field names) and before the company guards, so a company it
// inherits from a parent record is still validated against the caller's allowed companies.
func prepareGuardedValues(ctx context.Context, op WriteOp) (RecordMap, error) {
	values, err := normalizePayload(op.Model, op.Values)
	if err != nil {
		return nil, err
	}
	// Computed fields are read-only through the create/write dispatch (SRS-F-018);
	// rejecting here covers both RPC callers and seed imports with one rule.
	if err := rejectComputedFieldValues(op.Model, values); err != nil {
		return nil, err
	}
	if op.Model == nil || op.Model.prepare == nil {
		return values, nil
	}
	op.Values = values
	if err := op.Model.prepare(ctx, op); err != nil {
		return nil, err
	}
	return op.Values, nil
}
