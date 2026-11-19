package secretbox

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"

	"git.thebase.vn/base_hub/internal/config"
	"git.thebase.vn/base_hub/internal/orm"
	"github.com/uptrace/bun"
)

// openStoreTestDB connects to the integration database named by the RUNTIME_GRAPH_TEST_DB_* env
// vars (same names as the orm integration tests). It skips unless a password is given, so no
// credential is stored in this file.
func openStoreTestDB(t *testing.T) *bun.DB {
	t.Helper()
	password := os.Getenv("RUNTIME_GRAPH_TEST_DB_PASSWORD")
	if password == "" {
		t.Skip("RUNTIME_GRAPH_TEST_DB_PASSWORD not set: skipping Postgres-backed test")
	}
	env := func(key, fallback string) string {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
		return fallback
	}
	port, _ := strconv.Atoi(env("RUNTIME_GRAPH_TEST_DB_PORT", "5432"))
	db, err := orm.NewBunDB(config.PostgresqlConfig{
		Enable:   true,
		Host:     env("RUNTIME_GRAPH_TEST_DB_HOST", "localhost"),
		Port:     port,
		User:     env("RUNTIME_GRAPH_TEST_DB_USER", "thebase"),
		Password: password,
		Name:     env("RUNTIME_GRAPH_TEST_DB_NAME", "base_hub_test"),
		SSLMode:  env("RUNTIME_GRAPH_TEST_DB_SSLMODE", "disable"),
	})
	if err != nil {
		t.Skipf("integration database unavailable: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db.DB
}

// Rows written before the fix (zero key, no prefix) are re-encrypted by the boot migration, a
// rerun changes nothing, and rotation then moves every value to the new key.
func TestResealColumnsAgainstPostgres(t *testing.T) {
	db := openStoreTestDB(t)
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()

	const table = "tmp_secretbox_reseal_test"
	if _, err := tx.ExecContext(ctx, `CREATE TEMP TABLE `+table+` (id bigserial PRIMARY KEY, a text, b text)`); err != nil {
		t.Fatal(err)
	}
	legacy := legacyZeroKeySeal(t, "legacy-a")
	current, err := Seal(testKey, testLabel, "current-b")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO `+table+` (a, b) VALUES (?, ?), (NULL, ''), (?, NULL)`, legacy, current, "garbage"); err != nil {
		t.Fatal(err)
	}

	result, err := ResealColumns(ctx, tx, table, []string{"a", "b"}, testLabel, testKey, testKey)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Installed || result.Resealed != 1 || result.Failed != 1 {
		t.Fatalf("migration result = %+v, want installed, 1 resealed, 1 failed (garbage)", result)
	}
	var a, b string
	if err := tx.NewRaw(`SELECT a, b FROM `+table+` WHERE id = 1`).Scan(ctx, &a, &b); err != nil {
		t.Fatal(err)
	}
	if !IsCurrent(a) || b != current {
		t.Fatalf("after migration: a current=%v, b unchanged=%v", IsCurrent(a), b == current)
	}
	if plain, err := Open(testKey, testLabel, a); err != nil || plain != "legacy-a" {
		t.Fatalf("migrated a = %q, %v", plain, err)
	}

	again, err := ResealColumns(ctx, tx, table, []string{"a", "b"}, testLabel, testKey, testKey)
	if err != nil || again.Resealed != 0 {
		t.Fatalf("rerun must be a no-op: %+v, %v", again, err)
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE a = 'garbage'`); err != nil {
		t.Fatal(err)
	}
	rotated, err := ResealColumns(ctx, tx, table, []string{"a", "b"}, testLabel, testKey, testKey2)
	if err != nil || rotated.Resealed != 2 || rotated.Failed != 0 {
		t.Fatalf("rotation = %+v, %v; want 2 resealed", rotated, err)
	}
	if err := tx.NewRaw(`SELECT a, b FROM `+table+` WHERE id = 1`).Scan(ctx, &a, &b); err != nil {
		t.Fatal(err)
	}
	for value, want := range map[string]string{a: "legacy-a", b: "current-b"} {
		if plain, err := Open(testKey2, testLabel, value); err != nil || plain != want {
			t.Fatalf("after rotation: %q, %v; want %q", plain, err, want)
		}
	}

	missing, err := ResealColumns(ctx, tx, "tmp_secretbox_absent_table", []string{"a"}, testLabel, testKey, testKey)
	if err != nil || missing.Installed {
		t.Fatalf("absent table must report not installed: %+v, %v", missing, err)
	}
}
