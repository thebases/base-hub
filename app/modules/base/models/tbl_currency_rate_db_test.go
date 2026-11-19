package models

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"git.thebase.vn/base_hub/internal/orm"
	testutil "git.thebase.vn/base_hub/internal/test"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
)

// setupCurrencyRateDB installs a pool whose connections are pinned to a throwaway
// schema holding minimal tbl_currency_rate / tbl_company tables, so the rate
// lookup SQL runs on real Postgres without depending on migrated app tables.
func setupCurrencyRateDB(t *testing.T) context.Context {
	t.Helper()
	cfg := testutil.EnvPostgresConfig()
	ctx := context.Background()
	admin, err := testutil.OpenAdminConn(ctx, cfg)
	if err != nil {
		t.Skipf("open postgres fixture: %v", err)
	}
	t.Cleanup(func() { _ = admin.Close(ctx) })
	schema := fmt.Sprintf("currency_rate_test_%d", time.Now().UnixNano())
	for _, stmt := range []string{
		"CREATE SCHEMA " + schema,
		"CREATE TABLE " + schema + ".tbl_company (id BIGINT PRIMARY KEY, parent_id BIGINT)",
		"CREATE TABLE " + schema + ".tbl_currency_rate (id BIGSERIAL PRIMARY KEY, currency_id BIGINT NOT NULL, name DATE NOT NULL, rate DOUBLE PRECISION NOT NULL, company_id BIGINT)",
	} {
		if _, err := admin.Exec(ctx, stmt); err != nil {
			t.Skipf("%s: %v", stmt, err)
		}
	}
	t.Cleanup(func() { _, _ = admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE") })

	poolCfg, err := pgxpool.ParseConfig(fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		cfg.Host, cfg.Port, cfg.User, cfg.Password, cfg.Name, cfg.SSLMode))
	if err != nil {
		t.Fatal(err)
	}
	poolCfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	poolCfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		t.Fatal(err)
	}
	bunDB := &orm.BunDB{DB: bun.NewDB(stdlib.OpenDBFromPool(pool), pgdialect.New()), Pool: pool}
	t.Cleanup(func() { _ = bunDB.Close() })
	testutil.SwapBunDB(t, bunDB)
	return ctx
}

func seedRates(t *testing.T, ctx context.Context, rows string) {
	t.Helper()
	db := orm.GetBunDB()
	if _, err := db.ExecContext(ctx, "INSERT INTO tbl_currency_rate (currency_id, name, rate, company_id) VALUES "+rows); err != nil {
		t.Fatal(err)
	}
}

func TestCollectCurrencyRatesAgainstPostgres(t *testing.T) {
	ctx := setupCurrencyRateDB(t)
	if _, err := orm.GetBunDB().ExecContext(ctx, "INSERT INTO tbl_company (id, parent_id) VALUES (10, NULL)"); err != nil {
		t.Fatal(err)
	}
	seedRates(t, ctx, `
		(1, '2026-01-01', 2.0, NULL), (1, '2026-06-01', 3.0, NULL),
		(2, '2026-12-01', 5.0, NULL),
		(4, '2026-02-01', 7.0, NULL), (4, '2026-06-01', 0, NULL),
		(5, '2026-05-01', 1.5, NULL), (5, '2026-06-01', 9.0, 10),
		(6, '2026-06-30', 4.0, NULL), (6, '2026-07-02', 8.0, NULL)`)

	company := int64(10)
	rates, missing, err := collectCurrencyRates(ctx, []int64{1, 2, 3, 4, 5, 6, 0}, &company, "2026-07-01")
	if err != nil {
		t.Fatal(err)
	}
	want := map[int64]float64{
		1: 3.0, // newest at or before the date
		2: 5.0, // nothing that old: earliest rate on record
		4: 7.0, // newest dated row has rate 0 -> falls back to the earliest usable one
		5: 1.5, // company_id NULLS FIRST: the global row wins over the company row
		6: 4.0, // a row after the date is ignored
	}
	if len(rates) != len(want) {
		t.Fatalf("rates = %v, want %v", rates, want)
	}
	for id, rate := range want {
		if rates[id] != rate {
			t.Errorf("currency %d rate = %v, want %v", id, rates[id], rate)
		}
	}
	if len(missing) != 1 || missing[0] != 3 {
		t.Fatalf("missing = %v, want [3]", missing)
	}

	if _, err := requiredRatesForCurrencies(ctx, []int64{1, 3}, &company, "2026-07-01"); !errors.Is(err, ErrNoRate) {
		t.Fatalf("requiredRatesForCurrencies err = %v, want ErrNoRate", err)
	}
	display, err := latestRatesForCurrencies(ctx, []int64{3}, &company, "2026-07-01")
	if err != nil || display[3] != 1 {
		t.Fatalf("display default = %v, %v; want 1, nil", display, err)
	}
}

func TestCollectCurrencyRatesReturnsDatabaseError(t *testing.T) {
	ctx := setupCurrencyRateDB(t)
	if _, err := orm.GetBunDB().ExecContext(ctx, "DROP TABLE tbl_currency_rate"); err != nil {
		t.Fatal(err)
	}
	_, _, err := collectCurrencyRates(ctx, []int64{1}, nil, "2026-07-01")
	if err == nil || errors.Is(err, ErrNoRate) {
		t.Fatalf("err = %v, want a real database error, not ErrNoRate", err)
	}
	if _, err := requiredRatesForCurrencies(ctx, []int64{1}, nil, "2026-07-01"); errors.Is(err, ErrNoRate) {
		t.Fatalf("a database failure must not read as a missing rate: %v", err)
	}
}
