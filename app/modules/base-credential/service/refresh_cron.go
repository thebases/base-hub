package service

// File overview:
// - Responsibility: the base-automation go_handler that finds sys.credential rows whose token is
//   about to expire and re-authenticates them. Dispatched by base-automation's own sys.cron bridge
//   (via the seeded automation.action/automation.cron rows in data/), not by a credential-owned
//   sys.cron job, mirroring modules/base-automation/service/cron_dispatch.go's claim-then-execute
//   shape so one credential's failure never trips the outer bridge job's own backoff.
// - Key symbols: `refreshDueCredentialsHandlerKey`, `refreshDueCredentials`, `claimDueCredentialIDs`.

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/uptrace/bun"
	"golang.org/x/sync/errgroup"
)

const (
	refreshDueCredentialsHandlerKey = "credential.refresh_due_credentials"
	// sysCredentialTable is the physical table backing sys.credential.
	sysCredentialTable = "sys_credential"
	// refreshBatchLimit caps how many credentials one tick re-authenticates,
	// mirroring internal/services/cron's own claim batch limit.
	refreshBatchLimit = 16
)

// refreshConcurrency bounds simultaneous third-party login calls per tick.
const refreshConcurrency = 4

// refreshDueCredentials is the go_handler registered with base-automation
// (see runtime.go). It never returns an error for a single credential's login
// failure — only for infrastructure failures (no DB bound) — so one broken
// third-party auth endpoint never trips the outer automation.cron bridge
// job's own backoff/deactivation.
func refreshDueCredentials(ctx context.Context, _ map[string]any) error {
	db := orm.DBFromContext(ctx)
	if db == nil {
		return fmt.Errorf("credential: refresh requires a database-bound context")
	}

	ids, err := claimDueCredentialIDs(ctx, db, refreshBatchLimit)
	if err != nil {
		if isMissingSysCredentialRelation(err) {
			// A fresh or partially migrated DB without this module's table yet
			// is a normal condition, not a job failure.
			return nil
		}
		return err
	}

	var group errgroup.Group
	group.SetLimit(refreshConcurrency)
	for _, id := range ids {
		group.Go(func() error {
			runCredentialLoginWithRecover(ctx, id)
			return nil
		})
	}
	_ = group.Wait()
	return nil
}

// claimDueCredentialIDs selects active, non-apikey credentials whose token
// expires within RefreshBeforeSeconds and whose failure backoff
// (next_attempt_at, set by StoreSysCredentialLoginFailure) has elapsed. Rows
// with no pending backoff sort first, so credentials that keep failing never
// crowd healthy ones out of the per-tick limit. Unlike automation.cron's claim query,
// this does not need FOR UPDATE SKIP LOCKED row ownership semantics: a
// duplicate login for the same credential on a rare overlapping tick just
// re-authenticates and overwrites the token with an equally valid one, so a
// plain read keeps this query simple.
func claimDueCredentialIDs(ctx context.Context, db bun.IDB, limit int) ([]int64, error) {
	if limit <= 0 {
		limit = refreshBatchLimit
	}
	var ids []int64
	query := fmt.Sprintf(`
		SELECT id FROM %s
		WHERE active = TRUE
			AND credential_type != 'apikey'
			AND expired_time IS NOT NULL
			AND expired_time - make_interval(secs => GREATEST(refresh_before_seconds, 0)) <= now()
			AND (next_attempt_at IS NULL OR next_attempt_at <= now())
		ORDER BY (next_attempt_at IS NOT NULL) ASC, expired_time ASC, id ASC
		LIMIT ?
	`, sysCredentialTable)
	if err := db.NewRaw(query, limit).Scan(ctx, &ids); err != nil {
		return nil, err
	}
	return ids, nil
}

func runCredentialLoginWithRecover(ctx context.Context, credentialID int64) {
	defer func() {
		if r := recover(); r != nil {
			log.Errorf("credential: refresh %d panicked: %v\n%s", credentialID, r, debug.Stack())
		}
	}()
	if err := LoginCredential(ctx, credentialID); err != nil {
		log.Warnf("credential: refresh %d failed: %v", credentialID, err)
	}
}

// isMissingSysCredentialRelation matches SQLSTATE 42P01 (undefined_table):
// the query references only sys_credential, so any 42P01 here means that table.
func isMissingSysCredentialRelation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "42P01"
}
