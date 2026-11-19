package models

import (
	"context"
	"errors"
	"os"
	"strconv"
	"testing"

	"git.thebase.vn/base_hub/internal/config"
	"git.thebase.vn/base_hub/internal/orm"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/uptrace/bun"
)

// websiteTestDB opens the local base_hub_test database (WEBSITE_TEST_DB_* overrides) or skips.
func websiteTestDB(t *testing.T) *bun.DB {
	t.Helper()
	env := func(key, fallback string) string {
		if v := os.Getenv(key); v != "" {
			return v
		}
		return fallback
	}
	port, _ := strconv.Atoi(env("WEBSITE_TEST_DB_PORT", "5432"))
	handle, err := orm.NewBunDB(config.PostgresqlConfig{
		Enable: true, Host: env("WEBSITE_TEST_DB_HOST", "localhost"), Port: port,
		User: env("WEBSITE_TEST_DB_USER", "thebase"), Password: env("WEBSITE_TEST_DB_PASSWORD", "theBase15112023@"),
		Name: env("WEBSITE_TEST_DB_NAME", "base_hub_test"), SSLMode: "disable",
	})
	if err != nil {
		t.Skipf("base_hub_test database not reachable: %v", err)
	}
	t.Cleanup(func() { _ = handle.DB.Close() })
	return handle.DB
}

func createWebsiteScratchTable(t *testing.T, db *bun.DB, table string) {
	t.Helper()
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, "DROP TABLE IF EXISTS "+table); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "CREATE TABLE "+table+" (id bigserial PRIMARY KEY, site_id bigint, route_path text, revision bigint NOT NULL DEFAULT 1)"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = db.ExecContext(context.Background(), "DROP TABLE IF EXISTS "+table) })
}

func TestCasWebsiteRevisionIsOneConditionalUpdate(t *testing.T) {
	db := websiteTestDB(t)
	ctx := context.Background()
	table := "website_t76_cas"
	createWebsiteScratchTable(t, db, table)
	if _, err := db.ExecContext(ctx, "INSERT INTO "+table+" (id, revision) VALUES (1, 5)"); err != nil {
		t.Fatal(err)
	}
	next, err := casWebsiteRevision(ctx, db, "x", table, 1, 5)
	if err != nil || next != 6 {
		t.Fatalf("matching revision: next=%d err=%v", next, err)
	}
	// A second writer still holding revision 5 must lose, not overwrite.
	_, err = casWebsiteRevision(ctx, db, "x", table, 1, 5)
	var typed *orm.Error
	if !errors.As(err, &typed) || typed.Code != orm.ErrConflict {
		t.Fatalf("stale revision: want Conflict, got %v", err)
	}
	if next, err = casWebsiteRevision(ctx, db, "x", table, 1, 0); err != nil || next != 7 {
		t.Fatalf("revision 0 bypasses the check: next=%d err=%v", next, err)
	}
	if _, err = casWebsiteRevision(ctx, db, "x", table, 99, 0); !errors.As(err, &typed) || typed.Code != orm.ErrNotFound {
		t.Fatalf("missing row: want NotFound, got %v", err)
	}
}

func TestEnsureWebsiteUniqueIndexSkipsDuplicatesThenCreates(t *testing.T) {
	db := websiteTestDB(t)
	ctx := context.Background()
	table := "website_t76_routes"
	createWebsiteScratchTable(t, db, table)
	spec := UniqueIndexSpec{Table: table, Index: table + "_uniq", Columns: []string{"site_id", "route_path"}, Where: "site_id IS NOT NULL AND route_path <> ''"}
	if _, err := db.ExecContext(ctx, "INSERT INTO "+table+" (site_id, route_path) VALUES (1, '/a'), (1, '/a'), (1, ''), (1, '')"); err != nil {
		t.Fatal(err)
	}
	if err := EnsureWebsiteUniqueIndex(ctx, db, spec); err != nil {
		t.Fatalf("duplicates must not fail boot: %v", err)
	}
	var indexed bool
	if err := db.NewRaw("SELECT to_regclass(?) IS NOT NULL", spec.Index).Scan(ctx, &indexed); err != nil || indexed {
		t.Fatalf("index must be skipped while duplicates exist (indexed=%v err=%v)", indexed, err)
	}
	var rows int
	if err := db.NewRaw("SELECT count(*) FROM "+table).Scan(ctx, &rows); err != nil || rows != 4 {
		t.Fatalf("no row may be deleted: rows=%d err=%v", rows, err)
	}
	if _, err := db.ExecContext(ctx, "UPDATE "+table+" SET route_path = '/b' WHERE id = (SELECT max(id) FROM "+table+" WHERE route_path = '/a')"); err != nil {
		t.Fatal(err)
	}
	if err := EnsureWebsiteUniqueIndex(ctx, db, spec); err != nil {
		t.Fatal(err)
	}
	// Blank routes stay outside the partial index; a real duplicate now fails with 23505 on the index.
	_, err := db.ExecContext(ctx, "INSERT INTO "+table+" (site_id, route_path) VALUES (1, '/a')")
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != pgUniqueViolation || pgErr.ConstraintName != spec.Index {
		t.Fatalf("want unique violation on %s, got %v", spec.Index, err)
	}
	var typed *orm.Error
	if mapped := websiteUniqueViolation(err, spec.Index, "taken"); !errors.As(mapped, &typed) || typed.Code != orm.ErrConflict {
		t.Fatalf("23505 must map to Conflict, got %v", mapped)
	}
	if other := errors.New("boom"); websiteUniqueViolation(other, spec.Index, "taken") != other {
		t.Fatal("other errors must pass through")
	}
}

func TestEnsureWebsiteUniqueIndexIgnoresMissingTable(t *testing.T) {
	db := websiteTestDB(t)
	if err := EnsureWebsiteUniqueIndex(context.Background(), db, UniqueIndexSpec{
		Table: "website_t76_absent", Index: "website_t76_absent_uniq", Columns: []string{"site_id"}, Where: "true",
	}); err != nil {
		t.Fatal(err)
	}
}

func TestSeedBuiltinWebsiteThemesIsIdempotentPerDatabase(t *testing.T) {
	db := websiteTestDB(t)
	ctx := context.Background()
	var exists bool
	if err := db.NewRaw("SELECT to_regclass('tbl_website_theme') IS NOT NULL").Scan(ctx, &exists); err != nil || !exists {
		t.Skip("tbl_website_theme not migrated in base_hub_test")
	}
	err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		for range 2 {
			if err := seedBuiltinWebsiteThemes(ctx, tx); err != nil {
				return err
			}
		}
		var count int
		if err := tx.NewRaw("SELECT count(*) FROM tbl_website_theme WHERE slug IN ('default','slate')").Scan(ctx, &count); err != nil {
			return err
		}
		if count < 2 {
			t.Errorf("builtin themes missing: %d", count)
		}
		return errors.New("rollback")
	})
	if err == nil || err.Error() != "rollback" {
		t.Fatal(err)
	}
}
