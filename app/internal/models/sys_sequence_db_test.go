package models

// File overview:
// - Responsibility: verifies SRS-F-017 sequence semantics end to end against PostgreSQL — no-gap concurrency, rollback safety, standard-mode gaps, company scoping, and validation guards.
// - Key symbols: focused Test cases plus file-local fixtures and assertion helpers.
// - Pattern: Locks down runtime invariants so refactors preserve transaction, locking, and numbering behavior.

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"sync"
	"testing"
	"time"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/test"
)

// setupSysSequenceFixture provisions a clean sys_sequence table (and clears
// stale native sequences from earlier runs) against the RUNTIME_GRAPH_TEST_DB
// PostgreSQL, mirroring the runtime-graph integration fixture convention.
func setupSysSequenceFixture(t *testing.T) {
	t.Helper()

	bunDB, err := orm.NewBunDB(test.EnvPostgresConfig())
	if err != nil {
		t.Fatalf("open postgres sys.sequence fixture: %v", err)
	}
	test.SwapBunDB(t, bunDB)
	t.Cleanup(func() {
		_ = bunDB.Close()
	})

	SysSequenceModel.InitDefault()
	t.Cleanup(SysSequenceModel.Shutdown)

	ctx := context.Background()
	db := bunDB.DB
	if _, err := db.NewDropTable().Model((*SysSequenceRecord)(nil)).ModelTableExpr("sys_sequence").IfExists().Cascade().Exec(ctx); err != nil {
		t.Fatalf("drop sys_sequence fixture table: %v", err)
	}
	if _, err := db.NewCreateTable().Model((*SysSequenceRecord)(nil)).ModelTableExpr("sys_sequence").IfNotExists().Exec(ctx); err != nil {
		t.Fatalf("create sys_sequence fixture table: %v", err)
	}
	// Recreating the table restarts row IDs at 1, so native sequences left by a
	// previous run would silently resume old counters and break absolute-value
	// assertions. Clearing a generous ID range keeps every run deterministic.
	for id := int64(1); id <= 32; id++ {
		if _, err := db.ExecContext(ctx, fmt.Sprintf("DROP SEQUENCE IF EXISTS %s", standardPGSequenceName(id))); err != nil {
			t.Fatalf("drop stale native sequence %d: %v", id, err)
		}
	}
}

// sysSequenceTestContext binds a plain (non-sudo, no auth mode) environment to
// the shared test DB so runtime create/write flows behave like internal calls.
func sysSequenceTestContext(values map[string]any) context.Context {
	env := orm.NewEnvironment(orm.GetBunDB(), 0, values, false)
	return orm.ContextWithEnvironment(context.Background(), env)
}

func createTestSequence(t *testing.T, ctx context.Context, values map[string]any) int64 {
	t.Helper()
	record, err := createSysSequence(ctx, nil, nil, nil, map[string]any{"values": values})
	if err != nil {
		t.Fatalf("create sys.sequence fixture row: %v", err)
	}
	recordMap, ok := record.(orm.RecordMap)
	if !ok {
		t.Fatalf("expected single created record map, got %#v", record)
	}
	return int64Value(recordMap["id"])
}

func nextTestSequenceValue(t *testing.T, ctx context.Context, code string) string {
	t.Helper()
	value, err := nextBySequenceCode(ctx, nil, nil, []any{code}, nil)
	if err != nil {
		t.Fatalf("next_by_code(%q): %v", code, err)
	}
	formatted, ok := value.(string)
	if !ok {
		t.Fatalf("expected string sequence value, got %#v", value)
	}
	return formatted
}

// TestSysSequenceNoGapConcurrencyIssuesUniqueMonotonicNumbers proves the
// SRS-F-017 no-gap acceptance criterion: concurrent claims never duplicate and
// never skip a number.
func TestSysSequenceNoGapConcurrencyIssuesUniqueMonotonicNumbers(t *testing.T) {
	setupSysSequenceFixture(t)
	ctx := sysSequenceTestContext(nil)
	createTestSequence(t, ctx, map[string]any{
		"name":           "Concurrency",
		"code":           "test.concurrency",
		"implementation": SysSequenceImplementationNoGap,
	})

	const workers = 2
	const perWorker = 25
	var mu sync.Mutex
	issued := make([]int64, 0, workers*perWorker)
	var wg sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				// Each call binds its own environment so goroutines claim through
				// independent local transactions, which is the contended path.
				callCtx := sysSequenceTestContext(nil)
				raw, err := nextBySequenceCode(callCtx, nil, nil, []any{"test.concurrency"}, nil)
				if err != nil {
					t.Errorf("concurrent next_by_code: %v", err)
					return
				}
				parsed, err := strconv.ParseInt(raw.(string), 10, 64)
				if err != nil {
					t.Errorf("parse issued number %q: %v", raw, err)
					return
				}
				mu.Lock()
				issued = append(issued, parsed)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if len(issued) != workers*perWorker {
		t.Fatalf("expected %d issued numbers, got %d", workers*perWorker, len(issued))
	}
	sort.Slice(issued, func(i, j int) bool { return issued[i] < issued[j] })
	// The sorted stream must be exactly 1..N: any duplicate or gap breaks the
	// no-gap financial numbering contract.
	for index, value := range issued {
		if value != int64(index+1) {
			t.Fatalf("expected contiguous sequence 1..%d, got %v", workers*perWorker, issued)
		}
	}
}

// TestSysSequenceNoGapRollbackConsumesNothing proves the second SRS-F-017
// acceptance criterion: a rolled-back caller transaction returns its number.
func TestSysSequenceNoGapRollbackConsumesNothing(t *testing.T) {
	setupSysSequenceFixture(t)
	ctx := sysSequenceTestContext(nil)
	createTestSequence(t, ctx, map[string]any{
		"name":           "Rollback",
		"code":           "test.rollback",
		"implementation": SysSequenceImplementationNoGap,
	})

	tx, err := orm.GetBunDB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin caller transaction: %v", err)
	}
	// Bind the open transaction as the environment DB so next_by_code joins the
	// caller's unit of work instead of opening its own.
	txCtx := orm.ContextWithEnvironment(context.Background(), orm.NewEnvironment(tx, 0, nil, false))
	insideTx := nextTestSequenceValue(t, txCtx, "test.rollback")
	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback caller transaction: %v", err)
	}

	afterRollback := nextTestSequenceValue(t, ctx, "test.rollback")
	if insideTx != afterRollback {
		t.Fatalf("rollback consumed a no-gap number: inside tx %q, after rollback %q", insideTx, afterRollback)
	}
}

// TestSysSequenceStandardModeIssuesUniqueNumbersAndToleratesGaps locks the
// standard-mode contract: unique increasing numbers, and a rollback does
// consume the number (nextval is non-transactional by design).
func TestSysSequenceStandardModeIssuesUniqueNumbersAndToleratesGaps(t *testing.T) {
	setupSysSequenceFixture(t)
	ctx := sysSequenceTestContext(nil)
	createTestSequence(t, ctx, map[string]any{
		"name":           "Standard",
		"code":           "test.standard",
		"implementation": SysSequenceImplementationStandard,
	})

	first := nextTestSequenceValue(t, ctx, "test.standard")
	second := nextTestSequenceValue(t, ctx, "test.standard")
	if first != "1" || second != "2" {
		t.Fatalf("expected standard sequence to issue 1 then 2, got %q then %q", first, second)
	}

	tx, err := orm.GetBunDB().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin caller transaction: %v", err)
	}
	txCtx := orm.ContextWithEnvironment(context.Background(), orm.NewEnvironment(tx, 0, nil, false))
	insideTx := nextTestSequenceValue(t, txCtx, "test.standard")
	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback caller transaction: %v", err)
	}
	afterRollback := nextTestSequenceValue(t, ctx, "test.standard")
	// The rolled-back claim must have consumed "3": gaps are the accepted
	// trade-off that buys standard mode its lock-free concurrency.
	if insideTx != "3" || afterRollback != "4" {
		t.Fatalf("expected standard rollback to leave a gap (3 then 4), got %q then %q", insideTx, afterRollback)
	}
}

// TestSysSequenceFormattingAppliesPrefixPaddingAndDates covers the SRS-F-017
// formatting rule through the full claim path.
func TestSysSequenceFormattingAppliesPrefixPaddingAndDates(t *testing.T) {
	setupSysSequenceFixture(t)
	ctx := sysSequenceTestContext(nil)
	createTestSequence(t, ctx, map[string]any{
		"name":           "Formatted",
		"code":           "test.formatted",
		"prefix":         "INV/%(year)s/",
		"suffix":         "/X",
		"padding":        int64(5),
		"implementation": SysSequenceImplementationNoGap,
	})

	expected := fmt.Sprintf("INV/%s/00001/X", time.Now().UTC().Format("2006"))
	if got := nextTestSequenceValue(t, ctx, "test.formatted"); got != expected {
		t.Fatalf("expected formatted value %q, got %q", expected, got)
	}
}

// TestSysSequenceCompanyScopePrefersCompanyRowAndFallsBackToShared verifies
// the SRS-F-004 scoping rule wired into the lookup.
func TestSysSequenceCompanyScopePrefersCompanyRowAndFallsBackToShared(t *testing.T) {
	setupSysSequenceFixture(t)
	mainCompanyCtx := sysSequenceTestContext(map[string]any{
		orm.ContextKeyAllowedCompanyIDs: []int64{1},
		orm.ContextKeyCurrentCompanyID:  int64(1),
	})
	// The shared definition passes an explicit NULL company: without it the
	// runtime would pin the caller's active company on create.
	createTestSequence(t, mainCompanyCtx, map[string]any{
		"name":           "Scoped Shared",
		"code":           "test.scope",
		"prefix":         "SHARED/",
		"company_id":     nil,
		"implementation": SysSequenceImplementationNoGap,
	})
	createTestSequence(t, mainCompanyCtx, map[string]any{
		"name":           "Scoped Company",
		"code":           "test.scope",
		"prefix":         "CO/",
		"company_id":     int64(1),
		"implementation": SysSequenceImplementationNoGap,
	})

	// The caller's company row must win over the shared row.
	if got := nextTestSequenceValue(t, mainCompanyCtx, "test.scope"); got != "CO/1" {
		t.Fatalf("expected company-scoped row to win, got %q", got)
	}

	otherCompanyCtx := sysSequenceTestContext(map[string]any{
		orm.ContextKeyAllowedCompanyIDs: []int64{2},
		orm.ContextKeyCurrentCompanyID:  int64(2),
	})
	// A company without its own definition must fall back to the shared row.
	if got := nextTestSequenceValue(t, otherCompanyCtx, "test.scope"); got != "SHARED/1" {
		t.Fatalf("expected shared-row fallback for other company, got %q", got)
	}
}

// TestSysSequenceValidationGuards exercises the closed validation surface:
// duplicate codes, unknown codes, and in-place implementation changes.
func TestSysSequenceValidationGuards(t *testing.T) {
	setupSysSequenceFixture(t)
	ctx := sysSequenceTestContext(nil)
	recordID := createTestSequence(t, ctx, map[string]any{
		"name":           "Guarded",
		"code":           "test.guarded",
		"implementation": SysSequenceImplementationNoGap,
	})

	// Duplicate code in the same (shared) company scope must be rejected.
	if _, err := createSysSequence(ctx, nil, nil, nil, map[string]any{"values": map[string]any{
		"name": "Guarded Duplicate",
		"code": "test.guarded",
	}}); err == nil {
		t.Fatalf("expected duplicate code create to fail")
	}

	// Unknown codes are a validation error, not a silent empty value.
	if _, err := nextBySequenceCode(ctx, nil, nil, []any{"test.missing"}, nil); err == nil {
		t.Fatalf("expected unknown code to fail")
	}

	// Implementation is immutable after creation (state migration hazard).
	if _, err := writeSysSequence(ctx, nil, []string{strconv.FormatInt(recordID, 10)}, nil, map[string]any{"values": map[string]any{
		"implementation": SysSequenceImplementationStandard,
	}}); err == nil {
		t.Fatalf("expected implementation change to be rejected")
	}
}

// TestFormatSequenceValueResolvesPlaceholders unit-tests the pure formatting
// helper against a fixed timestamp (no database required).
func TestFormatSequenceValueResolvesPlaceholders(t *testing.T) {
	moment := time.Date(2026, time.July, 8, 21, 4, 5, 0, time.UTC)
	got := formatSequenceValue("DOC/%(year)s/%(month)s/%(day)s-", "-%(y)s", 4, 42, moment)
	want := "DOC/2026/07/08-0042-26"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
	// Zero padding disabled renders the raw number.
	if got := formatSequenceValue("", "", 0, 7, moment); got != "7" {
		t.Fatalf("expected unpadded %q, got %q", "7", got)
	}
}

// TestSysSequenceMultiWriteRejectsDuplicateCodes covers K-9: one write giving
// two rows the same code must fail instead of creating ambiguous lookups.
func TestSysSequenceMultiWriteRejectsDuplicateCodes(t *testing.T) {
	setupSysSequenceFixture(t)
	ctx := sysSequenceTestContext(nil)
	first := createTestSequence(t, ctx, map[string]any{"name": "A", "code": "test.multi.a", "implementation": SysSequenceImplementationNoGap})
	second := createTestSequence(t, ctx, map[string]any{"name": "B", "code": "test.multi.b", "implementation": SysSequenceImplementationNoGap})
	ids := []string{strconv.FormatInt(first, 10), strconv.FormatInt(second, 10)}
	if _, err := writeSysSequence(ctx, nil, ids, nil, map[string]any{"values": map[string]any{"code": "test.multi.same"}}); err == nil {
		t.Fatalf("expected a duplicate-code conflict for a multi-id code write")
	}
	if _, err := writeSysSequence(ctx, nil, ids, nil, map[string]any{"values": map[string]any{"prefix": "X-"}}); err != nil {
		t.Fatalf("multi-id write without code change: %v", err)
	}
}

// TestSysSequencePeekStandardFollowsNativeSequence covers K-10: for standard
// rows peek_by_code must report the native sequence's next value, not the
// row's never-advanced number_next.
func TestSysSequencePeekStandardFollowsNativeSequence(t *testing.T) {
	setupSysSequenceFixture(t)
	ctx := sysSequenceTestContext(nil)
	createTestSequence(t, ctx, map[string]any{"name": "Peek", "code": "test.peek.standard", "implementation": SysSequenceImplementationStandard})
	peek := func() string {
		value, err := peekBySequenceCode(ctx, nil, nil, []any{"test.peek.standard"}, nil)
		if err != nil {
			t.Fatalf("peek_by_code: %v", err)
		}
		return value.(string)
	}
	before := peek()
	issued := nextTestSequenceValue(t, ctx, "test.peek.standard")
	if before != issued {
		t.Fatalf("peek before first claim = %q, want the number then issued %q", before, issued)
	}
	after := peek()
	next := nextTestSequenceValue(t, ctx, "test.peek.standard")
	if after != next {
		t.Fatalf("peek after a claim = %q, want the next issued %q", after, next)
	}
}
