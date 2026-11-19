package orm

// File overview:
// - Responsibility: verifies renameTableIfNeeded (the RegisterLegacyTableRename step) moves a
//   module table to its new storage name in place, keeping rows and the id sequence.
// - Key symbols: TestRenameTableIfNeededAgainstPostgres.
// - Pattern: Locks down runtime invariants so refactors preserve transaction,
//   RBAC, registry, and storage behavior.

import (
	"context"
	"testing"
)

func TestRenameTableIfNeededAgainstPostgres(t *testing.T) {
	bunDB, err := NewBunDB(runtimeGraphEnvPostgresConfig())
	if err != nil {
		t.Skipf("postgres fixture unavailable: %v", err)
	}
	t.Cleanup(func() { _ = bunDB.Close() })
	db := bunDB.DB
	ctx := context.Background()

	const oldTable, newTable = "legacy_rename_fixture", "tbl_legacy_rename_fixture"
	dropFixtures := func() {
		_, _ = db.NewRaw("DROP TABLE IF EXISTS " + oldTable).Exec(ctx)
		_, _ = db.NewRaw("DROP TABLE IF EXISTS " + newTable).Exec(ctx)
	}
	dropFixtures()
	t.Cleanup(dropFixtures)

	// Absent old table (fresh database / module not installed) is a no-op.
	if err := renameTableIfNeeded(ctx, db, oldTable, newTable); err != nil {
		t.Fatalf("rename with no tables: %v", err)
	}

	// Plain rename path: rows survive and the BIGSERIAL keeps counting from where it was.
	for _, stmt := range []string{
		"CREATE TABLE " + oldTable + " (id BIGSERIAL PRIMARY KEY, name TEXT)",
		"INSERT INTO " + oldTable + " (name) VALUES ('a'), ('b')",
	} {
		if _, err := db.NewRaw(stmt).Exec(ctx); err != nil {
			t.Fatalf("prepare fixture: %v", err)
		}
	}
	if err := renameTableIfNeeded(ctx, db, oldTable, newTable); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if exists, err := relationTableExists(ctx, db, oldTable); err != nil || exists {
		t.Fatalf("old table still present (exists=%v err=%v)", exists, err)
	}
	var nextID int64
	if err := db.NewRaw("INSERT INTO "+newTable+" (name) VALUES ('c') RETURNING id").Scan(ctx, &nextID); err != nil {
		t.Fatalf("insert into renamed table: %v", err)
	}
	if nextID != 3 {
		t.Fatalf("renamed table id sequence = %d, want 3", nextID)
	}

	// Rerun after success is idempotent.
	if err := renameTableIfNeeded(ctx, db, oldTable, newTable); err != nil {
		t.Fatalf("idempotent rerun: %v", err)
	}

	// Both tables present is ambiguous and must fail instead of orphaning either side.
	if _, err := db.NewRaw("CREATE TABLE " + oldTable + " (id BIGSERIAL PRIMARY KEY)").Exec(ctx); err != nil {
		t.Fatalf("prepare conflict fixture: %v", err)
	}
	if err := renameTableIfNeeded(ctx, db, oldTable, newTable); err == nil {
		t.Fatalf("expected conflict error when both tables exist")
	}
}
