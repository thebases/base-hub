package orm

// File overview:
// - Responsibility: implements the `find_or_create` runtime RPC — a generic,
//   atomic find-or-create primitive usable by any caller (Grid view pivot
//   cell edits are the first consumer) that needs to close the classic
//   TOCTOU race of "search then create/write from two separate statements".
// - Key symbols: `runtimeFindOrCreate` (RPC handler, DB-backed, transactional),
//   `extractDomainEqualities` and `canonicalDomainLockKey` (pure helpers,
//   unit-testable without a DB).
// - Pattern: mirrors `runtime_cohort.go`'s split between the DB-touching RPC
//   glue and pure, directly-testable helper functions. Reuses the existing
//   `withRuntimeTxDB` transaction-join helper (internal/orm/runtime_tx.go)
//   rather than hand-rolling a new one, and follows this codebase's existing
//   "lock inside the caller's transaction" convention (see
//   internal/models/sys_sequence.go's no_gap row locking) — the only
//   difference is the lock primitive: a domain (not a single row) identifies
//   the contended resource here, so a Postgres advisory lock keyed on the
//   canonicalized (model, domain) pair is used instead of `SELECT ... FOR
//   UPDATE` on a row that may not exist yet.

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// domainEquality is one `field = value` constraint extracted from a parsed
// domain. find_or_create only understands this simple shape (see
// extractDomainEqualities) since it needs to turn the search filter into
// concrete field values for the create-if-missing path, which is not
// meaningful for arbitrary domain operators (">", "in", "like", ...) or
// boolean combinators other than AND.
type domainEquality struct {
	Field string
	Value any
}

// runtimeFindOrCreate handles the `find_or_create` runtime RPC. Kwargs:
//   - domain (or positional args[0]): the search filter identifying the
//     target record, e.g. [["employee_id","=",5],["date","=","2026-01-06"]].
//     Must be a non-empty, flat AND of "=" conditions — any other operator or
//     boolean combinator (">", "in", "or", "not", ...) is rejected with a
//     clear error naming the unsupported piece.
//   - values (or positional args[0] when it resolves to a record map, per
//     runtimeRecordMapArg's convention): fields to write on the record
//     whether it was found or newly created.
//
// Semantics: search for at most one record matching domain; if found, write
// values onto it; if not found, create a new record whose fields are the
// domain's equality constraints merged with values (values wins on overlap).
// Returns the resulting record in the same RecordMap shape a normal
// create/write RPC response uses.
func runtimeFindOrCreate(ctx context.Context, model *ModelMetadata, _ []string, args []any, kwargs map[string]any) (any, error) {
	if model == nil {
		return nil, fmt.Errorf("orm: model metadata is required")
	}

	domainRaw := valueAt(args, 0, kwargs["domain"])
	if domainRaw == nil {
		return nil, fmt.Errorf("orm: find_or_create requires a non-empty domain")
	}
	domainNode, err := ParseDomain(domainRaw)
	if err != nil {
		return nil, err
	}
	equalities, err := extractDomainEqualities(domainNode)
	if err != nil {
		return nil, err
	}
	if len(equalities) == 0 {
		return nil, fmt.Errorf("orm: find_or_create requires a non-empty domain")
	}
	// Fail every equality field lookup up front, before any DB work, so a
	// typo in the domain surfaces as a clear error instead of a confusing
	// downstream SQL failure.
	for _, eq := range equalities {
		if _, ok := runtimeLookupField(model, eq.Field); !ok {
			return nil, fmt.Errorf("orm: find_or_create domain field %q does not exist on model %q", eq.Field, model.Name)
		}
	}

	values, err := runtimeRecordMapArg(args, kwargs, "values")
	if err != nil {
		return nil, err
	}
	if values == nil {
		values = RecordMap{}
	}
	values, err = normalizePayload(model, values)
	if err != nil {
		return nil, err
	}
	// Computed fields are read-only through the create/write dispatch
	// (SRS-F-018); enforce the same rule here since find_or_create is a
	// create+write primitive.
	if err := rejectComputedFieldValues(model, values); err != nil {
		return nil, err
	}

	lockKey := canonicalDomainLockKey(model.Name, equalities)

	// Atomicity boundary: everything from the advisory lock acquisition
	// through the search and the resulting create-or-write happens inside a
	// single Postgres transaction. `withRuntimeTxDB` joins the caller's
	// already-open transaction when one exists, or opens a fresh one via
	// *bun.DB.RunInTx otherwise (same helper `runtime_cohort_data`'s sibling
	// mutating methods, create/write, rely on transitively). The very first
	// statement issued inside that transaction is
	// `pg_advisory_xact_lock(hashtext(<model>|<canonical domain>))`, so two
	// concurrent find_or_create calls targeting the same (model, domain) pair
	// serialize on the same lock key: whichever call acquires the lock first
	// runs its search and create/write and commits (releasing the lock on
	// commit), and the second call's search — which only starts once it in
	// turn acquires the lock — then observes the first call's just-committed
	// row instead of racing it into a duplicate create. The lock is
	// transaction-scoped (pg_advisory_xact_lock, not the session-scoped
	// pg_advisory_lock) so it is always released automatically at commit or
	// rollback, even on panic/early-return, with no explicit unlock call
	// needed.
	var result RecordMap
	txErr := withRuntimeTxDB(ctx, GetBunDB(), func(txCtx context.Context) error {
		if err := acquireFindOrCreateAdvisoryLock(txCtx, lockKey); err != nil {
			return err
		}

		filter := RecordMap{"__domain": domainNode, "__limit": 1}
		records, err := RuntimeRepositorySearchRead(txCtx, model, filter, nil)
		if err != nil {
			return err
		}

		if len(records) > 0 {
			id, ok := runtimeStringID(records[0]["id"])
			if !ok {
				return fmt.Errorf("orm: find_or_create matched a record with no usable id on model %q", model.Name)
			}
			if len(values) == 0 {
				result = records[0]
				return nil
			}
			if err := ensureRecordRuleAccess(txCtx, model, "write", []string{id}); err != nil {
				return err
			}
			// Same front half as WriteRecords (Prepare sanitises/validates per model), run after
			// the rule check so a caller learns nothing about rows it cannot write.
			writeValues, err := prepareGuardedValues(txCtx, WriteOp{Model: model, IDs: []string{id}, Values: values})
			if err != nil {
				return err
			}
			if err := validateImplicitCompanyAccess(txCtx, model, writeValues, "write"); err != nil {
				return err
			}
			if _, err := RuntimeRepositoryWrite(txCtx, model, []string{id}, writeValues); err != nil {
				return err
			}
			updated, err := runtimeRepositoryRead(txCtx, model, []string{id}, nil)
			if err != nil {
				return err
			}
			if len(updated) == 0 {
				return fmt.Errorf("orm: find_or_create could not re-read record %q on model %q after write", id, model.Name)
			}
			result = updated[0]
			return nil
		}

		// Not found: create a record seeded from the domain's equality
		// constraints (so the new row actually matches the search that just
		// missed it), then overlay the caller's values — values wins on
		// overlap per the documented contract.
		createValues := make(RecordMap, len(equalities)+len(values))
		for _, eq := range equalities {
			createValues[eq.Field] = eq.Value
		}
		for key, value := range values {
			createValues[key] = value
		}
		// Same front half as CreateRecord: Prepare must see the full create payload (domain
		// seed included) so model sanitising/validation cannot be bypassed via find_or_create.
		createValues, err = prepareGuardedValues(txCtx, WriteOp{Model: model, Create: true, Values: createValues})
		if err != nil {
			return err
		}
		// The pin fills company_id only when neither the domain nor values named one.
		if err := applyImplicitCreateCompanyValue(txCtx, model, createValues); err != nil {
			return err
		}
		if err := validateImplicitCompanyAccess(txCtx, model, createValues, "create"); err != nil {
			return err
		}
		created, err := RuntimeRepositoryCreate(txCtx, model, createValues)
		if err != nil {
			return err
		}
		result = created
		return nil
	})
	if txErr != nil {
		return nil, txErr
	}
	return result, nil
}

// acquireFindOrCreateAdvisoryLock issues `pg_advisory_xact_lock` as a raw
// statement against the transaction-bound DB handle in ctx. hashtext()
// collapses the arbitrary-length canonical lock key string into the int4
// Postgres's advisory-lock functions expect (hashtext returns int4, which
// implicitly widens to the bigint pg_advisory_xact_lock(bigint) expects).
func acquireFindOrCreateAdvisoryLock(ctx context.Context, lockKey string) error {
	db := DBFromContext(ctx)
	if db == nil {
		return fmt.Errorf("orm: find_or_create: no database handle available to acquire advisory lock")
	}
	if _, err := db.ExecContext(ctx, "SELECT pg_advisory_xact_lock(hashtext(?))", lockKey); err != nil {
		return fmt.Errorf("orm: find_or_create: failed to acquire advisory lock: %w", err)
	}
	return nil
}

// extractDomainEqualities walks a parsed DomainNode and returns the flat list
// of field=value equality constraints it represents. find_or_create
// deliberately supports only the simple "flat AND of equality conditions"
// domain shape — not a general domain evaluator — because it needs to turn
// unmatched search criteria into concrete create() field values, which has no
// sensible meaning for a range, membership, or OR/NOT domain.
func extractDomainEqualities(node DomainNode) ([]domainEquality, error) {
	switch node.Kind {
	case DomainKindCondition:
		if node.Operator != "=" {
			return nil, fmt.Errorf("orm: find_or_create only supports equality (\"=\") domain conditions, got operator %q for field %q", node.Operator, node.Field)
		}
		if strings.TrimSpace(node.Field) == "" {
			return nil, fmt.Errorf("orm: find_or_create domain condition is missing a field name")
		}
		return []domainEquality{{Field: node.Field, Value: node.Value}}, nil
	case DomainKindAnd:
		out := make([]domainEquality, 0, len(node.Children))
		for _, child := range node.Children {
			childEqualities, err := extractDomainEqualities(child)
			if err != nil {
				return nil, err
			}
			out = append(out, childEqualities...)
		}
		return out, nil
	case DomainKindTrue:
		return nil, fmt.Errorf("orm: find_or_create requires a non-empty domain")
	case DomainKindFalse:
		return nil, fmt.Errorf("orm: find_or_create does not support an always-false domain")
	case DomainKindOr:
		return nil, fmt.Errorf("orm: find_or_create does not support an \"or\" domain combinator")
	case DomainKindNot:
		return nil, fmt.Errorf("orm: find_or_create does not support a \"not\" domain combinator")
	default:
		return nil, fmt.Errorf("orm: find_or_create does not support domain kind %q", node.Kind)
	}
}

// canonicalDomainLockKey builds the deterministic string hashed into the
// advisory lock id: the model name plus every equality's field=value pair,
// sorted by field name and JSON-encoded, so two calls whose domain arrived in
// a different field order (e.g. [["a","=",1],["b","=",2]] vs
// [["b","=",2],["a","=",1]]) still collide on the same lock key.
func canonicalDomainLockKey(modelName string, equalities []domainEquality) string {
	sorted := append([]domainEquality(nil), equalities...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Field < sorted[j].Field })

	parts := make([]string, 0, len(sorted))
	for _, eq := range sorted {
		valueJSON, err := json.Marshal(eq.Value)
		if err != nil {
			// A value that cannot be JSON-encoded (should not happen for the
			// scalar equality values ParseDomain produces) still needs a
			// stable fallback rather than aborting lock-key derivation.
			valueJSON = []byte(fmt.Sprintf("%v", eq.Value))
		}
		parts = append(parts, eq.Field+"="+string(valueJSON))
	}
	return modelName + "|" + strings.Join(parts, "&")
}
