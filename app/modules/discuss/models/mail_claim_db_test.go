package models

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"git.thebase.vn/base_hub/internal/orm"
	testutil "git.thebase.vn/base_hub/internal/test"
	"github.com/uptrace/bun"
)

// openMailClaimConn opens one pooled connection pinned to a throwaway schema
// holding a minimal tbl_discuss_mail, so the claim SQL runs against real
// Postgres without touching (or depending on) the migrated application tables.
func openMailClaimConn(t *testing.T, db *bun.DB, schema string) bun.Conn {
	t.Helper()
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatalf("conn: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if _, err := conn.ExecContext(context.Background(), fmt.Sprintf("SET search_path TO %s", schema)); err != nil {
		t.Fatalf("search_path: %v", err)
	}
	return conn
}

func setupMailClaimSchema(t *testing.T) (*bun.DB, string) {
	t.Helper()
	bunDB, err := orm.NewBunDB(testutil.EnvPostgresConfig())
	if err != nil {
		t.Skipf("open postgres fixture: %v", err)
	}
	t.Cleanup(func() { _ = bunDB.Close() })
	db := bunDB.DB
	schema := fmt.Sprintf("mail_claim_test_%d", time.Now().UnixNano())
	ctx := context.Background()
	for _, stmt := range []string{
		"CREATE SCHEMA " + schema,
		"CREATE TABLE " + schema + `.tbl_discuss_mail (
			id BIGSERIAL PRIMARY KEY, active BOOLEAN NOT NULL DEFAULT TRUE, state TEXT NOT NULL,
			email_to TEXT NOT NULL DEFAULT 'a@x', email_cc TEXT, subject TEXT, body_html TEXT,
			scheduled_at TIMESTAMPTZ, updated_at TIMESTAMPTZ NOT NULL DEFAULT now())`,
	} {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	t.Cleanup(func() { _, _ = db.ExecContext(ctx, "DROP SCHEMA "+schema+" CASCADE") })
	return db, schema
}

func TestClaimOutgoingMailClaimsOnceAndRecoversStaleSending(t *testing.T) {
	db, schema := setupMailClaimSchema(t)
	conn := openMailClaimConn(t, db, schema)
	ctx := context.Background()
	if _, err := conn.ExecContext(ctx, fmt.Sprintf(`INSERT INTO %s.tbl_discuss_mail (state, updated_at) VALUES
		('outgoing', now()), ('outgoing', now()),
		('sending', now()),
		('sending', now() - interval '1 hour'),
		('sent', now())`, schema)); err != nil {
		t.Fatal(err)
	}
	// An inactive outgoing row must never be claimed.
	if _, err := conn.ExecContext(ctx, fmt.Sprintf("INSERT INTO %s.tbl_discuss_mail (state, active) VALUES ('outgoing', FALSE)", schema)); err != nil {
		t.Fatal(err)
	}

	// An outgoing row scheduled in the future waits; one scheduled in the past is due.
	if _, err := conn.ExecContext(ctx, fmt.Sprintf(`INSERT INTO %s.tbl_discuss_mail (state, scheduled_at) VALUES
		('outgoing', now() + interval '1 hour'), ('outgoing', now() - interval '1 minute')`, schema)); err != nil {
		t.Fatal(err)
	}

	claimed, err := claimOutgoingMail(ctx, conn, 100)
	if err != nil {
		t.Fatal(err)
	}
	// 3 due outgoing + 1 stale sending; the future-scheduled, fresh sending, sent and inactive rows stay put.
	if len(claimed) != 4 {
		t.Fatalf("claimed %d rows, want 4", len(claimed))
	}
	again, err := claimOutgoingMail(ctx, conn, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != 0 {
		t.Fatalf("second claim returned %d rows, want 0 (rows are now sending)", len(again))
	}

	releaseClaimedMail(ctx, conn, claimed)
	back, err := claimOutgoingMail(ctx, conn, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(back) != 2 {
		t.Fatalf("after release, limit-2 claim returned %d rows, want 2", len(back))
	}
}

func TestClaimOutgoingMailConcurrentSweepsNeverShareRows(t *testing.T) {
	db, schema := setupMailClaimSchema(t)
	ctx := context.Background()
	setup := openMailClaimConn(t, db, schema)
	if _, err := setup.ExecContext(ctx, fmt.Sprintf(
		"INSERT INTO %s.tbl_discuss_mail (state) SELECT 'outgoing' FROM generate_series(1, 200)", schema)); err != nil {
		t.Fatal(err)
	}

	const workers = 6
	var (
		mu      sync.Mutex
		seen    = map[int64]int{}
		wg      sync.WaitGroup
		failure error
	)
	for i := 0; i < workers; i++ {
		conn := openMailClaimConn(t, db, schema)
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				rows, err := claimOutgoingMail(ctx, conn, 7)
				mu.Lock()
				if err != nil {
					failure = err
				}
				for _, row := range rows {
					seen[int64Value(row["id"])]++
				}
				mu.Unlock()
				if err != nil || len(rows) == 0 {
					return
				}
			}
		}()
	}
	wg.Wait()
	if failure != nil {
		t.Fatal(failure)
	}
	if len(seen) != 200 {
		t.Fatalf("claimed %d distinct rows, want 200", len(seen))
	}
	for id, n := range seen {
		if n != 1 {
			t.Fatalf("row %d claimed %d times", id, n)
		}
	}
}
