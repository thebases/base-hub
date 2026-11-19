package models

// File overview:
// - Responsibility: the `base_bus.retention` kernel cron handler that deletes aged base.service.bus rows (plan T70).
// - Key symbols: `serviceBusRetentionHandlerKey`, `runServiceBusRetention`, `deleteServiceBusRowsBefore`.
// - Why: the bus log had no retention, so every published event stayed forever. Rows are only a
//   replay buffer for reconnecting clients; a client whose cursor row was deleted gets
//   `/bus/has_missed_notifications` = true and resyncs (BUS:OUTDATED).
// - Scheduling: seeded sys.cron row `base_bus.cron_service_bus_retention` (data/01_sys_cron_service_bus_retention.json),
//   `payload.retention_days` overrides the default (the module has no settings model).

import (
	"context"
	"errors"
	"fmt"
	"time"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/cron"
	"git.thebase.vn/base_hub/internal/services/log"
	"git.thebase.vn/base_hub/internal/utils/anyconv"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/uptrace/bun"
)

const (
	serviceBusRetentionHandlerKey = "base_bus.retention"
	// defaultServiceBusRetentionDays covers a client offline over a long weekend; older events are
	// toasts/refresh hints whose replay is worthless next to a full resync.
	defaultServiceBusRetentionDays = 7
	// serviceBusRetentionBatch bounds each DELETE so a large backlog is removed in short lock windows.
	serviceBusRetentionBatch = 1000
	// serviceBusRetentionMaxBatches bounds one tick; the next tick resumes where this one stopped.
	serviceBusRetentionMaxBatches = 100
)

func init() {
	Models.Add("base.service.bus.retention", 310, func() { _ = registerServiceBusRetentionCron() }, unregisterServiceBusRetentionCron)
}

func registerServiceBusRetentionCron() error {
	err := cron.RegisterHandler(serviceBusRetentionHandlerKey, runServiceBusRetention)
	if err != nil {
		log.Errorf("base-bus: register retention cron handler: %v", err)
	}
	return err
}

func unregisterServiceBusRetentionCron() {
	cron.UnregisterHandler(serviceBusRetentionHandlerKey)
}

// runServiceBusRetention deletes rows older than retention_days (payload) in bounded batches.
func runServiceBusRetention(ctx context.Context, payload map[string]any) error {
	db := orm.DBFromContext(ctx)
	if db == nil {
		return errors.New("base-bus: retention requires a database-bound context")
	}
	days, ok := anyconv.Int64(payload["retention_days"])
	if !ok || days <= 0 {
		days = defaultServiceBusRetentionDays
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -int(days))
	deleted, err := deleteServiceBusRowsBefore(ctx, db, cutoff, serviceBusRetentionBatch, serviceBusRetentionMaxBatches)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "42P01" { // undefined_table: module table not migrated yet
			return nil
		}
		return fmt.Errorf("base-bus: retention: %w", err)
	}
	if deleted > 0 {
		log.Infof("base-bus: retention deleted %d service bus rows older than %d days", deleted, days)
	}
	return nil
}

// deleteServiceBusRowsBefore deletes rows created before cutoff, oldest ids first, batch rows per
// statement, until a short batch or maxBatches. Each statement commits on its own (autocommit or
// the cron's handle), so locks are held for one batch only.
func deleteServiceBusRowsBefore(ctx context.Context, db bun.IDB, cutoff time.Time, batch, maxBatches int) (int64, error) {
	const sql = `
		DELETE FROM base_service_bus
		WHERE id IN (SELECT id FROM base_service_bus WHERE created_at < ? ORDER BY id LIMIT ?)
	`
	var total int64
	for range maxBatches {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		result, err := db.ExecContext(ctx, sql, cutoff, batch)
		if err != nil {
			return total, err
		}
		affected, _ := result.RowsAffected()
		total += affected
		if affected < int64(batch) {
			return total, nil
		}
	}
	return total, nil
}
