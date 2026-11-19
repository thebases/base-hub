package models

// File overview:
// - Responsibility: defines built-in runtime model metadata and server-side behavior for sys sequence (SRS-F-017 document numbering) inside the kernel model catalog.
// - Key symbols: `SysSequenceRecord`, `SysSequenceModelName`, `SysSequenceImplementationNoGap`, `SysSequenceImplementationStandard`.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
	"github.com/uptrace/bun"
)

// SysSequenceRecord stores one document-numbering sequence definition
// (SRS-F-017). `next_by_code` issues formatted, monotonically increasing
// numbers from these rows. Financial artifacts (payout refs, receipt numbers)
// depend on the no-gap guarantees encoded here, so every mutation of
// `number_next` is audit-sensitive.
type SysSequenceRecord struct {
	orm.BaseModel
	Name string `bun:"name" json:"name" meta:"index,label:Name,help:Stores the human readable name for this sequence."`
	// Code is the lookup key used by next_by_code. It must stay unique per
	// company scope (shared rows use company_id NULL) so a lookup never
	// resolves ambiguously between two active definitions.
	Code   string `bun:"code" json:"code" meta:"index,label:Code,help:Stores the lookup code used by next_by_code. Unique per company scope."`
	Prefix string `bun:"prefix" json:"prefix" meta:"label:Prefix,help:Stores the formatted prefix. Supports date placeholders such as %(year)s and %(month)s."`
	Suffix string `bun:"suffix" json:"suffix" meta:"label:Suffix,help:Stores the formatted suffix. Supports the same date placeholders as the prefix."`
	// Padding zero-pads the issued number; 0 means no padding. Kept as int64 so
	// the generic runtime integer normalization applies.
	Padding   int64 `bun:"padding" json:"padding" meta:"label:Padding,help:Stores the zero padding width for the issued number. 0 disables padding."`
	Increment int64 `bun:"increment" json:"increment" meta:"label:Increment,help:Stores the step added on each issued number. Default 1."`
	// NumberNext is authoritative for no_gap rows. For standard rows it is only
	// the START value of the native PostgreSQL sequence (and the RESTART target
	// when edited); the live counter lives in the PG sequence itself.
	NumberNext int64 `bun:"number_next" json:"number_next" meta:"label:Next Number,help:Stores the next number to issue. For standard sequences this seeds the native PostgreSQL sequence."`
	// Implementation selects the issuing strategy per SRS-F-017: `no_gap` locks
	// the row inside the caller's transaction (audit-grade, rollback consumes
	// nothing); `standard` delegates to a native PostgreSQL sequence (gaps
	// allowed, higher concurrency).
	Implementation string `bun:"implementation" json:"implementation" meta:"label:Implementation,help:Stores the issuing mode. no_gap locks the row transactionally; standard uses a native PostgreSQL sequence."`
	// CompanyID scopes the sequence per SRS-F-004: NULL is a shared definition
	// readable by every allowed company; next_by_code prefers the caller's
	// company row and falls back to the shared one.
	CompanyID *int64 `bun:"company_id" json:"company_id" meta:"kind:many2one,model:tbl.company,ondelete:SET NULL,index,label:Company,help:Links this sequence to one company. Empty means shared across companies."`
}

const SysSequenceModelName = "sys.sequence"

// Implementation modes per SRS-F-017. The set is closed on purpose: an unknown
// mode would silently break the numbering guarantees callers rely on.
const (
	SysSequenceImplementationNoGap    = "no_gap"
	SysSequenceImplementationStandard = "standard"
)

var SysSequenceModel = orm.Define[SysSequenceRecord](&Models, 315, orm.ModelSpec{
	Name:        SysSequenceModelName,
	Label:       "Sequences",
	Description: "Document numbering sequences issuing formatted monotonic references (SRS-F-017)",
	RuntimeMethods: []orm.RuntimeMethod{
		{Name: "create", Public: true, ModelOnly: true, Call: createSysSequence},
		{Name: "write", Public: true, UsesPathID: true, Call: writeSysSequence},
		{Name: "unlink", Public: true, UsesPathID: true, Call: unlinkSysSequence},
		// next_by_code mutates counter state, so it is intentionally not
		// ReadOnly even though callers treat it like a getter.
		{Name: "next_by_code", Public: true, ModelOnly: true, Call: nextBySequenceCode},
		// peek_by_code previews the number next_by_code would issue without claiming it, so a
		// new-record form can show the upcoming reference. The preview is not reserved.
		{Name: "peek_by_code", Public: true, ModelOnly: true, ReadOnly: true, Call: peekBySequenceCode},
	},
})

// Indirection breaks the SysSequenceModelSpec -> createSysSequence ->
// SysSequenceModel initialization cycle, mirroring the sys_cron.go pattern.

// createSysSequence creates sequence definitions and provisions the native
// PostgreSQL sequence for standard-mode rows.
func createSysSequence(ctx context.Context, model *orm.ModelMetadata, _ []string, args []any, kwargs map[string]any) (any, error) {
	valuesList, err := recordMapListFromArgs(args, kwargs, "values")
	if err != nil {
		return nil, err
	}
	if len(valuesList) == 0 {
		log.Errorf("sys.sequence.create requires values")
		return nil, fmt.Errorf("sys.sequence.create requires values")
	}

	created := make([]orm.RecordMap, 0, len(valuesList))
	for _, values := range valuesList {
		normalized, err := normalizeSysSequenceValues(values, true)
		if err != nil {
			return nil, err
		}
		// Reject duplicate codes inside the same company scope before creating,
		// otherwise next_by_code lookups become ambiguous between two rows.
		if err := ensureSysSequenceCodeAvailable(ctx, normalized, 0); err != nil {
			return nil, err
		}
		// Raw insert on purpose: CreateRecord would pin company_id from the environment AFTER the
		// code-uniqueness check above ran on the unpinned (shared) scope, so the stored row could
		// collide with another company's code. The T15 dispatcher guard still validates an explicit
		// company_id against the caller's allowed companies.
		record, err := orm.RuntimeRepositoryCreate(ctx, orm.RuntimeModelRef(SysSequenceModelName, model), normalized)
		if err != nil {
			return nil, err
		}
		// Standard rows delegate counting to a native PostgreSQL sequence, so it
		// must exist before the first next_by_code call. Creating it here keeps
		// the DDL next to the row lifecycle instead of surprising callers later.
		if stringValue(record["implementation"]) == SysSequenceImplementationStandard {
			recordID := int64Value(record["id"])
			increment, _ := int64FromAny(record["increment"])
			numberNext, _ := int64FromAny(record["number_next"])
			if err := ensureStandardPGSequence(ctx, recordID, increment, numberNext); err != nil {
				return nil, err
			}
		}
		created = append(created, record)
	}
	if len(created) == 1 {
		return created[0], nil
	}
	return created, nil
}

// writeSysSequence validates edits and keeps the native PostgreSQL sequence of
// standard rows aligned with the edited row values.
func writeSysSequence(ctx context.Context, model *orm.ModelMetadata, ids []string, args []any, kwargs map[string]any) (any, error) {
	if len(ids) == 0 {
		log.Errorf("sys.sequence.write requires ids")
		return nil, fmt.Errorf("sys.sequence.write requires ids")
	}
	values, err := recordMapFromArgs(args, kwargs, "values")
	if err != nil {
		return nil, err
	}
	if values == nil {
		log.Errorf("sys.sequence.write requires values")
		return nil, fmt.Errorf("sys.sequence.write requires values")
	}
	normalized, err := normalizeSysSequenceValues(values, false)
	if err != nil {
		return nil, err
	}

	// Load the current rows before writing so implementation-change guards and
	// standard-sequence DDL decisions run against real stored state.
	rows, err := loadSysSequenceRowsByID(ctx, ids)
	if err != nil {
		return nil, err
	}
	// Walk each affected row in order so validation and side effects stay scoped to the current element.
	for _, row := range rows {
		// Switching the issuing strategy in place is a state migration (the live
		// counter would have to move between the row and a PG sequence), so it is
		// rejected outright: operators must archive and recreate instead. This
		// keeps the numbering audit trail unambiguous.
		if raw, ok := normalized["implementation"]; ok {
			requested := strings.TrimSpace(stringValue(raw))
			if requested != row.Implementation {
				log.Errorf("sys.sequence implementation cannot be changed after creation; archive and create a new sequence instead")
				return nil, fmt.Errorf("sys.sequence implementation cannot be changed after creation; archive and create a new sequence instead")
			}
		}
		// Re-check code uniqueness when the code or the company scope moves so a
		// write can never make two active definitions collide.
		if _, codeChanged := normalized["code"]; codeChanged || hasRecordMapKey(normalized, "company_id") {
			effective := cloneRecordMap(normalized)
			if _, ok := effective["code"]; !ok {
				effective["code"] = row.Code
			}
			if !hasRecordMapKey(effective, "company_id") {
				// Preserve the stored company scope when the write does not move it.
				if row.CompanyID != nil {
					effective["company_id"] = *row.CompanyID
				} else {
					effective["company_id"] = nil
				}
			}
			if err := ensureSysSequenceCodeAvailable(ctx, effective, row.ID); err != nil {
				return nil, err
			}
		}
	}

	if err := ensureSysSequenceBatchCodesDistinct(rows, normalized); err != nil {
		return nil, err
	}

	var result any
	err = withSysSequenceWriteTx(ctx, func(txCtx context.Context) error {
		var writeErr error
		result, writeErr = orm.WriteRecords(txCtx, orm.RuntimeModelRef(SysSequenceModelName, model), ids, normalized)
		if writeErr != nil {
			return writeErr
		}
		return alignStandardPGSequences(txCtx, rows, normalized)
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// alignStandardPGSequences propagates counter edits into the native PostgreSQL
// sequence of standard rows. It runs in the row write's transaction (sequence
// DDL is transactional), so a failed ALTER rolls the row write back and the
// row and the PG sequence never disagree about the next issued number.
func alignStandardPGSequences(ctx context.Context, rows []sysSequenceRow, normalized orm.RecordMap) error {
	for _, row := range rows {
		if row.Implementation != SysSequenceImplementationStandard {
			continue
		}
		newIncrement := row.Increment
		if raw, ok := normalized["increment"]; ok {
			if parsed, okParse := int64FromAny(raw); okParse {
				newIncrement = parsed
			}
		}
		newNumberNext := int64(0)
		restart := false
		if raw, ok := normalized["number_next"]; ok {
			if parsed, okParse := int64FromAny(raw); okParse {
				newNumberNext = parsed
				restart = true
			}
		}
		// Ensure the PG sequence exists first: seed-imported rows may never have
		// passed through createSysSequence, and ALTER on a missing sequence fails.
		if err := ensureStandardPGSequence(ctx, row.ID, newIncrement, row.NumberNext); err != nil {
			return err
		}
		if err := alterStandardPGSequence(ctx, row.ID, newIncrement, newNumberNext, restart); err != nil {
			return err
		}
	}
	return nil
}

// ensureSysSequenceBatchCodesDistinct rejects a multi-id write that would give
// two of the written rows the same (code, company) pair: the per-row check in
// writeSysSequence only compares each row against rows outside the write.
func ensureSysSequenceBatchCodesDistinct(rows []sysSequenceRow, normalized orm.RecordMap) error {
	_, codeChanged := normalized["code"]
	if len(rows) < 2 || (!codeChanged && !hasRecordMapKey(normalized, "company_id")) {
		return nil
	}
	seen := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		code := row.Code
		if codeChanged {
			code = strings.TrimSpace(stringValue(normalized["code"]))
		}
		company := "shared"
		if hasRecordMapKey(normalized, "company_id") {
			company = fmt.Sprint(normalized["company_id"])
		} else if row.CompanyID != nil {
			company = fmt.Sprint(*row.CompanyID)
		}
		key := code + "|" + company
		if _, dup := seen[key]; dup {
			return orm.Conflictf("sys.sequence code %q would be used by more than one sequence in the same company scope", code)
		}
		seen[key] = struct{}{}
	}
	return nil
}

// withSysSequenceWriteTx runs fn with the environment bound to one transaction
// so the row write and the native-sequence DDL commit or roll back together.
// It reuses a caller's transaction (or an environment without a plain pool
// handle) as is; when it opens the transaction it also owns the after-commit
// queue, so record events fire only after the commit.
func withSysSequenceWriteTx(ctx context.Context, fn func(context.Context) error) error {
	env := orm.EnvironmentFromContext(ctx)
	db, ok := env.DB().(*bun.DB)
	if !ok {
		return fn(ctx)
	}
	return orm.WithAfterCommitScope(ctx, func(scopeCtx context.Context) error {
		return db.RunInTx(scopeCtx, nil, func(txCtx context.Context, tx bun.Tx) error {
			return fn(orm.ContextWithEnvironment(txCtx, env.WithDB(tx)))
		})
	})
}

// unlinkSysSequence deletes sequence rows and drops the native PostgreSQL
// sequences owned by standard-mode rows so no orphan DB objects accumulate.
func unlinkSysSequence(ctx context.Context, model *orm.ModelMetadata, ids []string, _ []any, _ map[string]any) (any, error) {
	if len(ids) == 0 {
		log.Errorf("sys.sequence.unlink requires ids")
		return nil, fmt.Errorf("sys.sequence.unlink requires ids")
	}
	// Load the rows before the delete: after RuntimeUnlink succeeds the
	// implementation mode is gone and we would no longer know which native
	// sequences to drop.
	rows, err := loadSysSequenceRowsByID(ctx, ids)
	if err != nil {
		return nil, err
	}
	result, err := orm.RuntimeRepositoryUnlink(ctx, orm.RuntimeModelRef(SysSequenceModelName, model), ids)
	if err != nil {
		return nil, err
	}
	// Drop native sequences only after the row delete committed its intent; a
	// failed drop is logged but does not undo the unlink because an orphan PG
	// sequence is harmless while a half-deleted row is not.
	for _, row := range rows {
		if row.Implementation != SysSequenceImplementationStandard {
			continue
		}
		if _, execErr := sysSequenceDBFromContext(ctx).ExecContext(ctx, fmt.Sprintf("DROP SEQUENCE IF EXISTS %s", standardPGSequenceName(row.ID))); execErr != nil {
			log.Errorf("sys.sequence: drop native sequence for row %d failed: %v", row.ID, execErr)
		}
	}
	return result, nil
}

// nextBySequenceCode implements the SRS-F-017 `next_by_code(code)` runtime
// method: it resolves the sequence for the caller's company scope, claims the
// next number under the declared implementation mode, and returns the
// formatted reference string.
func nextBySequenceCode(ctx context.Context, _ *orm.ModelMetadata, _ []string, args []any, kwargs map[string]any) (any, error) {
	code := sequenceCodeFromArgs(args, kwargs)
	if code == "" {
		log.Errorf("sys.sequence.next_by_code requires a sequence code")
		return nil, fmt.Errorf("sys.sequence.next_by_code requires a sequence code")
	}

	// Resolve the caller's active company so the lookup can prefer the
	// company-specific definition and fall back to the shared NULL row
	// (SRS-F-017 processing rule 3).
	companyID := int64(0)
	if env := orm.EnvironmentFromContext(ctx); env != nil {
		resolved, err := env.CompanyID()
		if err != nil {
			return nil, err
		}
		companyID = resolved
	}

	var formatted string
	// Run the whole claim inside the caller's transaction when one is bound to
	// the environment; otherwise open a local transaction so the SELECT FOR
	// UPDATE and the counter UPDATE stay atomic. This is the core no-gap
	// guarantee: a rolled-back caller transaction consumes no number because
	// the counter update rolls back with it.
	err := runInSysSequenceTx(ctx, func(txCtx context.Context, db bun.IDB) error {
		row, err := lookupSysSequenceByCode(txCtx, db, code, companyID)
		if err != nil {
			return err
		}
		value, err := claimSysSequenceNumber(txCtx, db, row)
		if err != nil {
			return err
		}
		formatted = formatSequenceValue(row.Prefix, row.Suffix, row.Padding, value, sequenceNow(txCtx))
		return nil
	})
	if err != nil {
		return nil, err
	}
	return formatted, nil
}

// peekBySequenceCode formats the sequence's current number_next without locking or advancing
// the counter. It is advisory only: the real number is claimed by next_by_code at save time.
func peekBySequenceCode(ctx context.Context, _ *orm.ModelMetadata, _ []string, args []any, kwargs map[string]any) (any, error) {
	code := sequenceCodeFromArgs(args, kwargs)
	if code == "" {
		return nil, fmt.Errorf("sys.sequence.peek_by_code requires a sequence code")
	}
	companyID := int64(0)
	if env := orm.EnvironmentFromContext(ctx); env != nil {
		resolved, err := env.CompanyID()
		if err != nil {
			return nil, err
		}
		companyID = resolved
	}
	row, err := lookupSysSequenceByCode(ctx, sysSequenceDBFromContext(ctx), code, companyID)
	if err != nil {
		return nil, err
	}
	next := row.NumberNext
	if row.Implementation == SysSequenceImplementationStandard {
		next, err = peekStandardSequenceNumber(ctx, sysSequenceDBFromContext(ctx), row)
		if err != nil {
			return nil, err
		}
	}
	return formatSequenceValue(row.Prefix, row.Suffix, row.Padding, next, sequenceNow(ctx)), nil
}

// peekStandardSequenceNumber reads the next value of a standard row's native
// sequence without consuming it: the row's number_next is only the start value
// there, since nextval never writes it back. pg_sequences.last_value is NULL
// before the first nextval; a missing sequence (seed-imported row, created on
// first claim) falls back to number_next. Advisory only, like peek_by_code.
func peekStandardSequenceNumber(ctx context.Context, db bun.IDB, row *sysSequenceRow) (int64, error) {
	var next []int64
	if err := db.NewRaw(
		`SELECT CASE WHEN last_value IS NULL THEN start_value ELSE last_value + increment_by END
		FROM pg_sequences WHERE schemaname = current_schema() AND sequencename = ?`,
		standardPGSequenceName(row.ID),
	).Scan(ctx, &next); err != nil {
		return 0, fmt.Errorf("sys.sequence: peek native sequence for %q: %w", row.Code, err)
	}
	if len(next) == 0 {
		return row.NumberNext, nil
	}
	return next[0], nil
}

// sysSequenceRow is the raw storage projection used by the sequence claim and
// lifecycle paths. Raw SQL is intentional here: the claim must control locking
// (FOR UPDATE) precisely, which the generic runtime read path cannot express.
type sysSequenceRow struct {
	ID             int64
	Code           string
	Prefix         string
	Suffix         string
	Padding        int64
	Increment      int64
	NumberNext     int64
	Implementation string
	CompanyID      *int64
}

// sysSequenceDBFromContext resolves the SQL handle for sequence work: the
// environment-bound handle first (it may be the caller's open transaction),
// then the request database pool. Keeping this resolution in one place is what
// lets no-gap claims join the caller's transaction per the coding rules.
func sysSequenceDBFromContext(ctx context.Context) bun.IDB {
	if env := orm.EnvironmentFromContext(ctx); env != nil && env.DB() != nil {
		return env.DB()
	}
	// Fall back to the request-resolved pool for callers without an environment.
	if db := orm.BunDBFromContext(ctx); db != nil {
		return db
	}
	return nil
}

// runInSysSequenceTx executes fn inside the caller's transaction when the
// environment already carries one, and otherwise wraps fn in a fresh local
// transaction. Without this, a standalone RPC call would run SELECT FOR UPDATE
// and UPDATE as two autocommit statements and lose the atomic claim.
func runInSysSequenceTx(ctx context.Context, fn func(context.Context, bun.IDB) error) error {
	handle := sysSequenceDBFromContext(ctx)
	if handle == nil {
		log.Errorf("sys.sequence: no database handle available")
		return fmt.Errorf("sys.sequence: no database handle available")
	}
	// Split behavior by concrete type so each transaction shape is handled safely.
	switch current := handle.(type) {
	case bun.Tx:
		// Reuse the caller's transaction so a rollback releases the claimed number.
		return fn(ctx, current)
	case *bun.Tx:
		// Reuse pointer transactions for the same reason as bun.Tx above.
		return fn(ctx, current)
	case *bun.DB:
		// Open a local transaction so the two-step no-gap claim stays atomic for
		// standalone calls.
		return current.RunInTx(ctx, nil, func(txCtx context.Context, tx bun.Tx) error {
			return fn(txCtx, tx)
		})
	default:
		// Unknown Bun-compatible handles are treated as already-managed scopes,
		// mirroring the ORM's own withRuntimeTx behavior.
		return fn(ctx, handle)
	}
}

// lookupSysSequenceByCode resolves the active sequence definition for one code
// under the caller's company scope: the company-specific row wins, the shared
// NULL row is the fallback (ORDER BY company_id NULLS LAST).
func lookupSysSequenceByCode(ctx context.Context, db bun.IDB, code string, companyID int64) (*sysSequenceRow, error) {
	row := &sysSequenceRow{}
	query := db.NewSelect().
		// Bind the explicit storage table: package-level queries on runtime
		// models must never rely on Bun struct-name inference (tbl_lang rule).
		TableExpr("sys_sequence").
		// COALESCE the nullable columns so legacy or partially seeded rows can
		// never fail the scan into non-pointer Go fields.
		ColumnExpr("id, COALESCE(code, '') AS code, COALESCE(prefix, '') AS prefix, COALESCE(suffix, '') AS suffix").
		ColumnExpr("COALESCE(padding, 0) AS padding, COALESCE(increment, 1) AS increment, COALESCE(number_next, 1) AS number_next").
		ColumnExpr("COALESCE(implementation, ?) AS implementation, company_id", SysSequenceImplementationNoGap).
		Where("code = ?", code).
		Where("active = TRUE").
		OrderExpr("company_id ASC NULLS LAST, id ASC").
		Limit(1)
	// Restrict to the caller's company plus shared rows; callers without a
	// resolved company (system jobs, sudo bootstrap) only see shared rows.
	if companyID > 0 {
		query = query.Where("(company_id = ? OR company_id IS NULL)", companyID)
	} else {
		query = query.Where("company_id IS NULL")
	}
	err := query.Scan(ctx, &row.ID, &row.Code, &row.Prefix, &row.Suffix, &row.Padding, &row.Increment, &row.NumberNext, &row.Implementation, &row.CompanyID)
	if errors.Is(err, sql.ErrNoRows) {
		// Unknown and inactive codes are one validation error per SRS-F-017 —
		// existence must not leak through a different message shape.
		log.Errorf("sys.sequence: no active sequence found for code %q", code)
		return nil, fmt.Errorf("sys.sequence: no active sequence found for code %q", code)
	}
	if err != nil {
		return nil, err
	}
	return row, nil
}

// claimSysSequenceNumber issues the next raw number for one resolved sequence
// row under its declared implementation mode.
func claimSysSequenceNumber(ctx context.Context, db bun.IDB, row *sysSequenceRow) (int64, error) {
	switch row.Implementation {
	case SysSequenceImplementationStandard:
		return claimStandardSequenceNumber(ctx, db, row)
	default:
		return claimNoGapSequenceNumber(ctx, db, row)
	}
}

// claimNoGapSequenceNumber implements the audit-grade path: lock the row with
// SELECT ... FOR UPDATE, take the locked value, and advance the counter in the
// same transaction. Concurrent callers serialize on the row lock, so numbers
// can never duplicate, and a caller rollback returns the number to the pool
// because the UPDATE rolls back too. Lock waits are bounded only by the
// caller's context / server timeouts (SRS-F-017 contention rule).
func claimNoGapSequenceNumber(ctx context.Context, db bun.IDB, row *sysSequenceRow) (int64, error) {
	var lockedNumber int64
	// Re-read the counter under the lock: the unlocked lookup value may be
	// stale by the time this transaction wins the row lock.
	err := db.NewSelect().
		TableExpr("sys_sequence").
		Column("number_next").
		Where("id = ?", row.ID).
		For("UPDATE").
		Scan(ctx, &lockedNumber)
	if err != nil {
		return 0, fmt.Errorf("sys.sequence: lock no-gap sequence %q: %w", row.Code, err)
	}
	// Advance the counter inside the same transaction; this write is what makes
	// the issued number durable only when the caller commits.
	if _, err := db.NewUpdate().
		Table("sys_sequence").
		Set("number_next = number_next + ?", row.Increment).
		Where("id = ?", row.ID).
		Exec(ctx); err != nil {
		return 0, fmt.Errorf("sys.sequence: advance no-gap sequence %q: %w", row.Code, err)
	}
	return lockedNumber, nil
}

// claimStandardSequenceNumber delegates to the native PostgreSQL sequence.
// nextval is non-transactional by design: a caller rollback still consumes the
// number, which is exactly the SRS-F-017 standard-mode trade-off (gaps allowed
// in exchange for no row lock contention).
func claimStandardSequenceNumber(ctx context.Context, db bun.IDB, row *sysSequenceRow) (int64, error) {
	sequenceName := standardPGSequenceName(row.ID)
	// Probe the catalog cheaply before nextval: seed-imported rows may not have
	// gone through createSysSequence, and a failed nextval would abort the
	// caller's transaction, so the existence check must come first.
	var exists bool
	if err := db.NewSelect().
		ColumnExpr("to_regclass(?) IS NOT NULL", sequenceName).
		Scan(ctx, &exists); err != nil {
		return 0, fmt.Errorf("sys.sequence: probe native sequence for %q: %w", row.Code, err)
	}
	if !exists {
		if err := ensureStandardPGSequenceOn(ctx, db, row.ID, row.Increment, row.NumberNext); err != nil {
			return 0, err
		}
	}
	var value int64
	// nextval issues the number outside transactional rollback semantics.
	if err := db.NewSelect().
		ColumnExpr("nextval(?)", sequenceName).
		Scan(ctx, &value); err != nil {
		return 0, fmt.Errorf("sys.sequence: nextval for %q: %w", row.Code, err)
	}
	return value, nil
}

// standardPGSequenceName derives the deterministic native sequence identifier
// for one row. The zero-padded numeric suffix keeps the name identifier-safe
// without quoting because the row ID is the only variable part.
func standardPGSequenceName(id int64) string {
	return fmt.Sprintf("sys_sequence_%06d", id)
}

// ensureStandardPGSequence provisions the native PostgreSQL sequence backing
// one standard-mode row on the environment-bound database.
func ensureStandardPGSequence(ctx context.Context, id, increment, start int64) error {
	db := sysSequenceDBFromContext(ctx)
	if db == nil {
		log.Errorf("sys.sequence: no database handle available for sequence DDL")
		return fmt.Errorf("sys.sequence: no database handle available for sequence DDL")
	}
	return ensureStandardPGSequenceOn(ctx, db, id, increment, start)
}

// ensureStandardPGSequenceOn creates the native sequence when missing. The
// defaults guard against invalid stored values so DDL can never be generated
// with a zero increment or a sub-minimum start.
func ensureStandardPGSequenceOn(ctx context.Context, db bun.IDB, id, increment, start int64) error {
	if increment <= 0 {
		increment = 1
	}
	if start <= 0 {
		start = 1
	}
	// IF NOT EXISTS keeps this idempotent across create/write/claim paths; the
	// numeric arguments are validated int64s so string interpolation is safe.
	statement := fmt.Sprintf("CREATE SEQUENCE IF NOT EXISTS %s INCREMENT BY %d START WITH %d MINVALUE 1", standardPGSequenceName(id), increment, start)
	if _, err := db.ExecContext(ctx, statement); err != nil {
		return fmt.Errorf("sys.sequence: create native sequence for row %d: %w", id, err)
	}
	return nil
}

// alterStandardPGSequence pushes edited increment/number_next values into the
// native sequence so operator edits behave identically across both modes.
func alterStandardPGSequence(ctx context.Context, id, increment, numberNext int64, restart bool) error {
	db := sysSequenceDBFromContext(ctx)
	if db == nil {
		log.Errorf("sys.sequence: no database handle available for sequence DDL")
		return fmt.Errorf("sys.sequence: no database handle available for sequence DDL")
	}
	if increment <= 0 {
		increment = 1
	}
	clauses := fmt.Sprintf("INCREMENT BY %d", increment)
	if restart && numberNext > 0 {
		// RESTART repositions the counter; validation already enforced >= 1.
		clauses = fmt.Sprintf("%s RESTART WITH %d", clauses, numberNext)
	}
	if _, err := db.ExecContext(ctx, fmt.Sprintf("ALTER SEQUENCE %s %s", standardPGSequenceName(id), clauses)); err != nil {
		return fmt.Errorf("sys.sequence: alter native sequence for row %d: %w", id, err)
	}
	return nil
}

// loadSysSequenceRowsByID reads the raw storage rows for lifecycle decisions
// (implementation guards, DDL targets) on the environment-bound handle.
func loadSysSequenceRowsByID(ctx context.Context, ids []string) ([]sysSequenceRow, error) {
	db := sysSequenceDBFromContext(ctx)
	if db == nil {
		log.Errorf("sys.sequence: no database handle available")
		return nil, fmt.Errorf("sys.sequence: no database handle available")
	}
	numericIDs := make([]int64, 0, len(ids))
	for _, raw := range ids {
		parsed, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
		if err != nil {
			log.Errorf("sys.sequence: invalid record id %q", raw)
			return nil, fmt.Errorf("sys.sequence: invalid record id %q", raw)
		}
		numericIDs = append(numericIDs, parsed)
	}
	rows := make([]sysSequenceRow, 0, len(numericIDs))
	err := db.NewSelect().
		TableExpr("sys_sequence").
		// COALESCE mirrors lookupSysSequenceByCode so lifecycle reads share the
		// same NULL-tolerance as the claim path.
		ColumnExpr("id, COALESCE(code, '') AS code, COALESCE(prefix, '') AS prefix, COALESCE(suffix, '') AS suffix").
		ColumnExpr("COALESCE(padding, 0) AS padding, COALESCE(increment, 1) AS increment, COALESCE(number_next, 1) AS number_next").
		ColumnExpr("COALESCE(implementation, ?) AS implementation, company_id", SysSequenceImplementationNoGap).
		Where("id IN (?)", bun.In(numericIDs)).
		Scan(ctx, &rows)
	if err != nil {
		return nil, err
	}
	return rows, nil
}

// ensureSysSequenceCodeAvailable rejects a create/write whose (code, company
// scope) already has another active-or-archived definition. The check is
// advisory (two racing creates could still slip through between check and
// insert); it exists to give operators a clear validation error in the normal
// path rather than a low-level constraint failure.
func ensureSysSequenceCodeAvailable(ctx context.Context, values orm.RecordMap, excludeID int64) error {
	db := sysSequenceDBFromContext(ctx)
	if db == nil {
		log.Errorf("sys.sequence: no database handle available")
		return fmt.Errorf("sys.sequence: no database handle available")
	}
	code := strings.TrimSpace(stringValue(values["code"]))
	if code == "" {
		return nil
	}
	// Resolve the company scope the row will land in: an explicit value wins,
	// otherwise the runtime will pin the caller's active company on create, so
	// the uniqueness check must anticipate the same target scope.
	companyID := int64(0)
	if raw, ok := values["company_id"]; ok {
		companyID, _ = int64FromAny(raw)
	} else if env := orm.EnvironmentFromContext(ctx); env != nil {
		if resolved, err := env.CompanyID(); err == nil {
			companyID = resolved
		}
	}
	query := db.NewSelect().
		TableExpr("sys_sequence").
		ColumnExpr("count(*)").
		Where("code = ?", code)
	if companyID > 0 {
		query = query.Where("company_id = ?", companyID)
	} else {
		query = query.Where("company_id IS NULL")
	}
	if excludeID > 0 {
		query = query.Where("id <> ?", excludeID)
	}
	var count int64
	if err := query.Scan(ctx, &count); err != nil {
		return err
	}
	if count > 0 {
		log.Errorf("sys.sequence code %q already exists in this company scope", code)
		return fmt.Errorf("sys.sequence code %q already exists in this company scope", code)
	}
	return nil
}
