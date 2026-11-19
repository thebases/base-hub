package orm

// File overview:
// - Responsibility: verifies the pure domain-equality-extraction and
//   lock-key-canonicalization helpers behind the find_or_create runtime RPC,
//   independent of the DB-backed runtimeFindOrCreate handler.
// - Key symbols: TestExtractDomainEqualities*, TestCanonicalDomainLockKey*.
// - Pattern: constructs DomainNode fixtures directly (as ParseDomain would
//   produce them) so the equality/lock-key math is exercised without a
//   database, mirroring how runtime_cohort_test.go isolates buildCohortMatrix
//   from runtimeCohortData.

import (
	"testing"
)

func TestExtractDomainEqualitiesFlatAnd(t *testing.T) {
	domain := And(
		Where("employee_id", "=", 5),
		Where("date", "=", "2026-01-06"),
	)

	equalities, err := extractDomainEqualities(domain)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(equalities) != 2 {
		t.Fatalf("expected 2 equalities, got %d (%+v)", len(equalities), equalities)
	}

	byField := map[string]any{}
	for _, eq := range equalities {
		byField[eq.Field] = eq.Value
	}
	if byField["employee_id"] != 5 {
		t.Errorf("expected employee_id=5, got %v", byField["employee_id"])
	}
	if byField["date"] != "2026-01-06" {
		t.Errorf("expected date=2026-01-06, got %v", byField["date"])
	}
}

func TestExtractDomainEqualitiesSingleCondition(t *testing.T) {
	domain := Where("employee_id", "=", 5)

	equalities, err := extractDomainEqualities(domain)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(equalities) != 1 || equalities[0].Field != "employee_id" || equalities[0].Value != 5 {
		t.Fatalf("unexpected equalities: %+v", equalities)
	}
}

func TestExtractDomainEqualitiesRejectsNonEqualityOperator(t *testing.T) {
	domain := Where("hours", ">", 5)

	_, err := extractDomainEqualities(domain)
	if err == nil {
		t.Fatal("expected an error for a non-equality operator, got nil")
	}
}

func TestExtractDomainEqualitiesRejectsOr(t *testing.T) {
	domain := Or(
		Where("employee_id", "=", 5),
		Where("employee_id", "=", 6),
	)

	_, err := extractDomainEqualities(domain)
	if err == nil {
		t.Fatal("expected an error for an \"or\" domain, got nil")
	}
}

func TestExtractDomainEqualitiesRejectsNot(t *testing.T) {
	domain := Not(Where("employee_id", "=", 5))

	_, err := extractDomainEqualities(domain)
	if err == nil {
		t.Fatal("expected an error for a \"not\" domain, got nil")
	}
}

func TestExtractDomainEqualitiesRejectsEmptyDomain(t *testing.T) {
	_, err := extractDomainEqualities(TrueDomain())
	if err == nil {
		t.Fatal("expected an error for an empty/true domain, got nil")
	}
}

func TestExtractDomainEqualitiesRejectsFalseDomain(t *testing.T) {
	_, err := extractDomainEqualities(FalseDomain())
	if err == nil {
		t.Fatal("expected an error for an always-false domain, got nil")
	}
}

func TestCanonicalDomainLockKeyStableAcrossFieldOrder(t *testing.T) {
	a := []domainEquality{
		{Field: "employee_id", Value: 5},
		{Field: "date", Value: "2026-01-06"},
	}
	b := []domainEquality{
		{Field: "date", Value: "2026-01-06"},
		{Field: "employee_id", Value: 5},
	}

	keyA := canonicalDomainLockKey("tbl.timesheet", a)
	keyB := canonicalDomainLockKey("tbl.timesheet", b)

	if keyA != keyB {
		t.Fatalf("expected canonicalized keys to match regardless of input order, got %q vs %q", keyA, keyB)
	}
}

func TestCanonicalDomainLockKeyDiffersByModel(t *testing.T) {
	eq := []domainEquality{{Field: "employee_id", Value: 5}}

	keyOne := canonicalDomainLockKey("tbl.timesheet", eq)
	keyTwo := canonicalDomainLockKey("tbl.other_model", eq)

	if keyOne == keyTwo {
		t.Fatalf("expected different models to produce different lock keys, got %q for both", keyOne)
	}
}

func TestCanonicalDomainLockKeyDiffersByValue(t *testing.T) {
	keyOne := canonicalDomainLockKey("tbl.timesheet", []domainEquality{{Field: "employee_id", Value: 5}})
	keyTwo := canonicalDomainLockKey("tbl.timesheet", []domainEquality{{Field: "employee_id", Value: 6}})

	if keyOne == keyTwo {
		t.Fatalf("expected different values to produce different lock keys, got %q for both", keyOne)
	}
}
