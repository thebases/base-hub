package queries

// File overview:
// - Responsibility: holds read-oriented query logic for the base-bus module, separating optimized fetch paths from mutation-heavy runtime code.
// - Key symbols: `PollServiceBus`, `PollServiceBusPage`, `LastServiceBusID`, `RecentServiceBus`, `ServiceBusRow`, `serviceBusSettledHorizon`.
// - Pattern: Threads `context.Context` through the main execution paths so cancellation, auth scope, and transaction state can follow the work end to end.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"git.thebase.vn/base_hub/internal/services/log"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type ServiceBusRow struct {
	ID        int64
	Channel   string
	EventType string
	Payload   map[string]any
	CreatedBy int64
	CreatedAt time.Time
}

const errServiceBusQueryPoolNotInitialized = "service bus query pool is not initialized"

const (
	// ServiceBusPollLimit caps one poll. A client that receives a full page
	// continues from the last id it got (streams loop at once, HTTP pollers
	// on their next tick), so a long backlog never becomes one huge response.
	ServiceBusPollLimit = 200
	// ServiceBusCommitLag is how long a missing id below a visible one is
	// treated as "maybe still in flight". Ids come from a sequence at INSERT
	// time, before commit, so id 10 can commit after id 11; a plain
	// `id > cursor` reader that already moved to 11 would skip 10 forever.
	// Delivery therefore stops below the first young gap (see
	// serviceBusSettledHorizon) until the gap fills or ages out. 15 s covers
	// the short request/workflow transactions that publish; a transaction held
	// open longer than this after its insert can still be skipped, and a
	// rolled-back publish delays rows after it by at most this long.
	ServiceBusCommitLag = 15 * time.Second
	// serviceBusHorizonScanLimit bounds the gap scan to the young tail.
	serviceBusHorizonScanLimit = 1000
)

// serviceBusUnbounded is the horizon when no young gap holds delivery back.
const serviceBusUnbounded = math.MaxInt64

// serviceBusTailRow is one young row plus the id just below it (any age).
type serviceBusTailRow struct {
	ID   int64
	Prev int64
}

// PollServiceBus is PollServiceBusPage with the default page size and the
// current time; kept for callers outside this module (base-workflow's
// trigger registry watch), which continue from the last id they read.
func PollServiceBus(ctx context.Context, pool *pgxpool.Pool, channels []string, lastID int64, catchupSince time.Time) ([]ServiceBusRow, error) {
	rows, _, err := PollServiceBusPage(ctx, pool, channels, lastID, catchupSince, time.Now().UTC(), ServiceBusPollLimit)
	return rows, err
}

// PollServiceBusPage returns at most limit rows on channels newer than lastID
// (or, for a fresh client with lastID <= 0, created after catchupSince), in
// id order, never past the settled horizon. held reports that a young gap is
// holding rows back, so a stream should re-check soon even without a NOTIFY
// (a rolled-back publish sends none).
func PollServiceBusPage(ctx context.Context, pool *pgxpool.Pool, channels []string, lastID int64, catchupSince, now time.Time, limit int) (rows []ServiceBusRow, held bool, err error) {
	if err := requireServiceBusPool(pool); err != nil {
		return nil, false, err
	}
	if len(channels) == 0 {
		return []ServiceBusRow{}, false, nil
	}
	if limit <= 0 {
		limit = ServiceBusPollLimit
	}

	const sql = `
		SELECT id, channel, event_type, payload, created_by, created_at
		FROM base_service_bus
		WHERE channel = ANY($1)
		  AND id <= $4
		  AND (($2 > 0 AND id > $2) OR ($2 <= 0 AND created_at > $3))
		ORDER BY id ASC
		LIMIT $5
	`
	// The horizon scan and the row read must share one snapshot: a row that
	// commits between them could otherwise be delivered past a gap the scan
	// never saw, which is exactly the skip this guards against.
	err = readServiceBusSnapshot(ctx, pool, func(tx pgx.Tx) error {
		horizon, scanErr := serviceBusHorizon(ctx, tx, max(lastID, 0), now)
		if scanErr != nil {
			return scanErr
		}
		held = horizon != serviceBusUnbounded
		result, queryErr := tx.Query(ctx, sql, channels, lastID, catchupSince, horizon, limit)
		if queryErr != nil {
			return queryErr
		}
		defer result.Close()
		rows, queryErr = collectServiceBusRows(result)
		return queryErr
	})
	return rows, held, err
}

// serviceBusHorizon is the highest id at or below which every id above
// after is settled (committed or abandoned), or serviceBusUnbounded.
func serviceBusHorizon(ctx context.Context, tx pgx.Tx, after int64, now time.Time) (int64, error) {
	// Only rows younger than the commit lag can sit just above an id that is
	// still in flight, so the scan reads the young tail only; prev is the
	// next lower id of any age (one PK index probe per row).
	const sql = `
		SELECT y.id, COALESCE((SELECT max(p.id) FROM base_service_bus p WHERE p.id < y.id), 0)
		FROM base_service_bus y
		WHERE y.id > $1 AND y.created_at > $2
		ORDER BY y.id ASC
		LIMIT $3
	`
	rows, err := tx.Query(ctx, sql, after, now.Add(-ServiceBusCommitLag), serviceBusHorizonScanLimit)
	if err != nil {
		return 0, err
	}
	tail, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (serviceBusTailRow, error) {
		var r serviceBusTailRow
		return r, row.Scan(&r.ID, &r.Prev)
	})
	if err != nil {
		return 0, err
	}
	return serviceBusSettledHorizon(after, tail, len(tail) == serviceBusHorizonScanLimit), nil
}

// serviceBusSettledHorizon walks the young tail in id order and stops just
// below the first gap. truncated means the scan hit its limit, so nothing
// past the last scanned row is known to be settled yet.
func serviceBusSettledHorizon(after int64, tail []serviceBusTailRow, truncated bool) int64 {
	for _, row := range tail {
		below := max(row.Prev, after)
		if row.ID > below+1 {
			return below
		}
	}
	if truncated && len(tail) > 0 {
		return tail[len(tail)-1].ID
	}
	return serviceBusUnbounded
}

// readServiceBusSnapshot runs fn in one read-only REPEATABLE READ
// transaction so every statement sees the same snapshot.
func readServiceBusSnapshot(ctx context.Context, pool *pgxpool.Pool, fn func(pgx.Tx) error) error {
	return pgx.BeginTxFunc(ctx, pool, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, fn)
}

func RecentServiceBus(ctx context.Context, pool *pgxpool.Pool, channels []string, limit int64) ([]ServiceBusRow, error) {
	if err := requireServiceBusPool(pool); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 50
	}

	const withChannelsSQL = `
		SELECT id, channel, event_type, payload, created_by, created_at
		FROM base_service_bus
		WHERE channel = ANY($1)
		ORDER BY id DESC
		LIMIT $2
	`

	rows, err := pool.Query(ctx, withChannelsSQL, channels, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectServiceBusRows(rows)
}

// LastServiceBusID is the resume cursor handed to a fresh subscriber: the
// newest id, capped at the settled horizon so an id still in flight below it
// is not skipped by a client that starts from here.
func LastServiceBusID(ctx context.Context, pool *pgxpool.Pool) (int64, error) {
	now := time.Now().UTC()
	if err := requireServiceBusPool(pool); err != nil {
		return 0, err
	}

	var id int64
	err := readServiceBusSnapshot(ctx, pool, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT COALESCE(max(id), 0) FROM base_service_bus`).Scan(&id); err != nil {
			return err
		}
		horizon, err := serviceBusHorizon(ctx, tx, 0, now)
		id = min(id, horizon)
		return err
	})
	if err != nil {
		return 0, err
	}
	return id, nil
}

// requireServiceBusPool centralizes the pool guard so every query path fails
// with the same operator-facing message before touching pgx.
func requireServiceBusPool(pool *pgxpool.Pool) error {
	if pool != nil {
		return nil
	}
	log.Errorf(errServiceBusQueryPoolNotInitialized)
	return errors.New(errServiceBusQueryPoolNotInitialized)
}

// collectServiceBusRows collects the service bus rows handled by this package.
func collectServiceBusRows(rows pgx.Rows) ([]ServiceBusRow, error) {
	result := make([]ServiceBusRow, 0, 32)
	for rows.Next() {
		var (
			row         ServiceBusRow
			payloadJSON []byte
		)
		if err := rows.Scan(&row.ID, &row.Channel, &row.EventType, &payloadJSON, &row.CreatedBy, &row.CreatedAt); err != nil {
			return nil, err
		}
		if len(payloadJSON) > 0 {
			if err := json.Unmarshal(payloadJSON, &row.Payload); err != nil {
				log.Errorf("decode service bus payload for row %d: %v", row.ID, err)
				return nil, fmt.Errorf("decode service bus payload for row %d: %w", row.ID, err)
			}
		}
		if row.Payload == nil {
			row.Payload = map[string]any{}
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}
