package orm

// File overview:
// - Responsibility: verifies the res.* -> tbl.* legacy logical-model rename
//   migration rewrites persisted metadata rows in place on existing databases.
// - Key symbols: TestEnsureLegacyModelNameRenamesAgainstPostgres.
// - Pattern: Locks down runtime invariants so refactors preserve transaction,
//   RBAC, registry, and storage behavior.

import (
	"context"
	"testing"

	"github.com/uptrace/bun"
)

// legacyRenameTestTables creates the minimal shape of every metadata table the
// rename touches so the test runs on a bare fixture database. IF NOT EXISTS
// keeps the real schema when another suite already migrated the full tables.
func legacyRenameTestTables(ctx context.Context, t *testing.T, db *bun.DB) {
	t.Helper()
	statements := []string{
		"CREATE TABLE IF NOT EXISTS sys_model (id BIGSERIAL PRIMARY KEY, name TEXT)",
		"CREATE UNIQUE INDEX IF NOT EXISTS sys_model_name_key ON sys_model (name)",
		"CREATE TABLE IF NOT EXISTS sys_model_field (id BIGSERIAL PRIMARY KEY, model_name TEXT, field_name TEXT)",
		"CREATE UNIQUE INDEX IF NOT EXISTS sys_model_field_model_name_field_name_key ON sys_model_field (model_name, field_name)",
		"CREATE TABLE IF NOT EXISTS sys_model_access (id BIGSERIAL PRIMARY KEY, model_name TEXT)",
		"CREATE TABLE IF NOT EXISTS ui_view (id BIGSERIAL PRIMARY KEY, model TEXT, name TEXT, key TEXT)",
		"CREATE TABLE IF NOT EXISTS ui_menu (id BIGSERIAL PRIMARY KEY, route_path TEXT)",
		"CREATE TABLE IF NOT EXISTS base_workflow_action (id BIGSERIAL PRIMARY KEY, model_name TEXT)",
	}
	for _, stmt := range statements {
		if _, err := db.NewRaw(stmt).Exec(ctx); err != nil {
			t.Fatalf("prepare legacy rename fixture: %v", err)
		}
	}
}

// cleanupLegacyRenameRows removes every row this test may have created under
// either the old or the new model names so reruns start from a clean slate.
func cleanupLegacyRenameRows(ctx context.Context, db *bun.DB) {
	names := []string{"res.partner", "tbl.partner", "res.company", "tbl.company"}
	for _, name := range names {
		_, _ = db.NewRaw("DELETE FROM sys_model WHERE name = ?", name).Exec(ctx)
		_, _ = db.NewRaw("DELETE FROM sys_model_field WHERE model_name = ?", name).Exec(ctx)
		_, _ = db.NewRaw("DELETE FROM sys_model_access WHERE model_name = ?", name).Exec(ctx)
		_, _ = db.NewRaw("DELETE FROM ui_view WHERE model = ?", name).Exec(ctx)
		_, _ = db.NewRaw("DELETE FROM base_workflow_action WHERE model_name = ?", name).Exec(ctx)
		_, _ = db.NewRaw("DELETE FROM ui_menu WHERE route_path = ?", "/web/"+name).Exec(ctx)
	}
}

func countLegacyRenameRows(ctx context.Context, t *testing.T, db *bun.DB, query string, args ...any) int {
	t.Helper()
	var count int
	if err := db.NewRaw(query, args...).Scan(ctx, &count); err != nil {
		t.Fatalf("count rows (%s): %v", query, err)
	}
	return count
}

func TestEnsureLegacyModelNameRenamesAgainstPostgres(t *testing.T) {
	bunDB, err := NewBunDB(runtimeGraphEnvPostgresConfig())
	if err != nil {
		t.Skipf("postgres fixture unavailable: %v", err)
	}
	t.Cleanup(func() { _ = bunDB.Close() })
	db := bunDB.DB
	ctx := context.Background()

	legacyRenameTestTables(ctx, t, db)
	cleanupLegacyRenameRows(ctx, db)
	t.Cleanup(func() { cleanupLegacyRenameRows(ctx, db) })

	seeds := []struct {
		query string
		args  []any
	}{
		// Plain rename path: only the old dotted name exists.
		{"INSERT INTO sys_model (name) VALUES (?)", []any{"res.partner"}},
		{"INSERT INTO sys_model_field (model_name, field_name) VALUES (?, ?)", []any{"res.partner", "name"}},
		{"INSERT INTO sys_model_access (model_name) VALUES (?)", []any{"res.partner"}},
		{"INSERT INTO ui_view (model, name, key) VALUES (?, ?, ?)", []any{"res.partner", "res.partner.form", "res.partner.form"}},
		{"INSERT INTO ui_menu (route_path) VALUES (?)", []any{"/web/res.partner"}},
		{"INSERT INTO base_workflow_action (model_name) VALUES (?)", []any{"res.partner"}},
		// Unique-conflict path: both old and new rows already exist, so the
		// stale old row must be dropped instead of renamed.
		{"INSERT INTO sys_model (name) VALUES (?) ON CONFLICT DO NOTHING", []any{"res.company"}},
		{"INSERT INTO sys_model (name) VALUES (?) ON CONFLICT DO NOTHING", []any{"tbl.company"}},
		{"INSERT INTO sys_model_field (model_name, field_name) VALUES (?, ?) ON CONFLICT DO NOTHING", []any{"res.company", "name"}},
		{"INSERT INTO sys_model_field (model_name, field_name) VALUES (?, ?) ON CONFLICT DO NOTHING", []any{"tbl.company", "name"}},
	}
	for _, seed := range seeds {
		if _, err := db.NewRaw(seed.query, seed.args...).Exec(ctx); err != nil {
			t.Fatalf("seed legacy rename fixture (%s): %v", seed.query, err)
		}
	}

	if err := ensureLegacyModelNameRenames(ctx, db); err != nil {
		t.Fatalf("ensureLegacyModelNameRenames: %v", err)
	}

	// No old dotted names may survive on any rewritten surface.
	if got := countLegacyRenameRows(ctx, t, db, "SELECT COUNT(*) FROM sys_model WHERE name LIKE 'res.%'"); got != 0 {
		t.Fatalf("stale sys_model res.* rows remain: %d", got)
	}
	if got := countLegacyRenameRows(ctx, t, db, "SELECT COUNT(*) FROM sys_model_field WHERE model_name LIKE 'res.%'"); got != 0 {
		t.Fatalf("stale sys_model_field res.* rows remain: %d", got)
	}
	if got := countLegacyRenameRows(ctx, t, db, "SELECT COUNT(*) FROM sys_model WHERE name = ?", "tbl.partner"); got != 1 {
		t.Fatalf("sys_model tbl.partner rows = %d, want 1", got)
	}
	// The dedupe branch keeps exactly one row for the already-renamed model.
	if got := countLegacyRenameRows(ctx, t, db, "SELECT COUNT(*) FROM sys_model WHERE name = ?", "tbl.company"); got != 1 {
		t.Fatalf("sys_model tbl.company rows = %d, want 1", got)
	}
	if got := countLegacyRenameRows(ctx, t, db, "SELECT COUNT(*) FROM sys_model_field WHERE model_name = ? AND field_name = ?", "tbl.company", "name"); got != 1 {
		t.Fatalf("sys_model_field tbl.company rows = %d, want 1", got)
	}
	if got := countLegacyRenameRows(ctx, t, db, "SELECT COUNT(*) FROM sys_model_access WHERE model_name = ?", "tbl.partner"); got != 1 {
		t.Fatalf("sys_model_access tbl.partner rows = %d, want 1", got)
	}
	// View rows rewrite the model column and the derived dotted name/key prefix.
	if got := countLegacyRenameRows(ctx, t, db, "SELECT COUNT(*) FROM ui_view WHERE model = ? AND name = ? AND key = ?", "tbl.partner", "tbl.partner.form", "tbl.partner.form"); got != 1 {
		t.Fatalf("renamed ui_view rows = %d, want 1", got)
	}
	if got := countLegacyRenameRows(ctx, t, db, "SELECT COUNT(*) FROM ui_menu WHERE route_path = ?", "/web/tbl.partner"); got != 1 {
		t.Fatalf("renamed ui_menu rows = %d, want 1", got)
	}
	if got := countLegacyRenameRows(ctx, t, db, "SELECT COUNT(*) FROM base_workflow_action WHERE model_name = ?", "tbl.partner"); got != 1 {
		t.Fatalf("renamed base_workflow_action rows = %d, want 1", got)
	}

	// Second run must be a clean no-op so every startup can execute it.
	if err := ensureLegacyModelNameRenames(ctx, db); err != nil {
		t.Fatalf("ensureLegacyModelNameRenames rerun: %v", err)
	}
}
