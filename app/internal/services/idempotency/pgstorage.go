package idempotency

// File overview:
// - Responsibility: implements a PostgreSQL-backed fiber.Storage adapter on the home database so
//   idempotency state survives process restarts and is shared across instances (SRS Open Question #2,
//   owner decision 2026-07-06).
// - Key symbols: `NewPGStorage`, `PGStorage`, `DeleteExpired`.
// - Pattern: Uses Bun-facing raw SQL with an explicit table so schema mapping stays explicit and the
//   adapter has no dependency on the runtime model registry.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"git.thebase.vn/base_hub/internal/services/log"
	"github.com/uptrace/bun"
)

// storageTable is the physical home-DB table owned by this adapter; it is
// created by NewPGStorage rather than the model registry because the payload
// is opaque middleware state, not a runtime model.
const storageTable = "idempotency_store"

// PGStorage implements fiber.Storage on the home database. The non-context
// methods (Get/Set/Delete/Reset) exist only to satisfy the interface and
// delegate to their WithContext counterparts using context.Background().
type PGStorage struct {
	db *bun.DB
}

// NewPGStorage binds the adapter to the given (home) database and ensures the
// backing table exists so first use never races table creation.
func NewPGStorage(ctx context.Context, db *bun.DB) (*PGStorage, error) {
	if db == nil {
		log.Errorf("idempotency: storage requires a database handle")
		return nil, fmt.Errorf("idempotency: storage requires a database handle")
	}
	// expitbl_at is NULL for non-expiring entries (fiber contract: exp == 0).
	_, err := db.NewRaw(fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s (
			key        text PRIMARY KEY,
			value      bytea NOT NULL,
			expitbl_at timestamptz
		)
	`, storageTable)).Exec(ctx)
	if err != nil {
		return nil, fmt.Errorf("idempotency: ensure %s table: %w", storageTable, err)
	}
	return &PGStorage{db: db}, nil
}

// Get returns the stored value, or nil,nil when the key is missing or expired
// (the fiber.Storage miss contract). Expired rows are left for the scheduled
// GC job so the hot read path never pays for deletes.
func (s *PGStorage) Get(key string) ([]byte, error) {
	return s.GetWithContext(context.Background(), key)
}

// GetWithContext is the context-aware form fiber v3's Storage interface requires.
func (s *PGStorage) GetWithContext(ctx context.Context, key string) ([]byte, error) {
	if strings.TrimSpace(key) == "" {
		return nil, nil
	}
	var value []byte
	err := s.db.NewRaw(
		fmt.Sprintf("SELECT value FROM %s WHERE key = ? AND (expitbl_at IS NULL OR expitbl_at > now())", storageTable),
		key,
	).Scan(ctx, &value)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return value, nil
}

// Set upserts the value; per the fiber contract an empty key or value is
// ignored without error and exp == 0 means no expiration.
func (s *PGStorage) Set(key string, val []byte, exp time.Duration) error {
	return s.SetWithContext(context.Background(), key, val, exp)
}

// SetWithContext is the context-aware form fiber v3's Storage interface requires.
func (s *PGStorage) SetWithContext(ctx context.Context, key string, val []byte, exp time.Duration) error {
	if strings.TrimSpace(key) == "" || len(val) == 0 {
		return nil
	}
	var expiresAt *time.Time
	if exp > 0 {
		at := time.Now().Add(exp)
		expiresAt = &at
	}
	_, err := s.db.NewRaw(
		fmt.Sprintf(`
			INSERT INTO %s (key, value, expitbl_at) VALUES (?, ?, ?)
			ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, expitbl_at = EXCLUDED.expitbl_at
		`, storageTable),
		key, val, expiresAt,
	).Exec(ctx)
	return err
}

// Delete removes one key; missing keys are not an error per the fiber contract.
func (s *PGStorage) Delete(key string) error {
	return s.DeleteWithContext(context.Background(), key)
}

// DeleteWithContext is the context-aware form fiber v3's Storage interface requires.
func (s *PGStorage) DeleteWithContext(ctx context.Context, key string) error {
	if strings.TrimSpace(key) == "" {
		return nil
	}
	_, err := s.db.NewRaw(fmt.Sprintf("DELETE FROM %s WHERE key = ?", storageTable), key).Exec(ctx)
	return err
}

// Reset drops all keys.
func (s *PGStorage) Reset() error {
	return s.ResetWithContext(context.Background())
}

// ResetWithContext is the context-aware form fiber v3's Storage interface requires.
func (s *PGStorage) ResetWithContext(ctx context.Context) error {
	_, err := s.db.NewRaw(fmt.Sprintf("DELETE FROM %s", storageTable)).Exec(ctx)
	return err
}

// Close is a no-op: the adapter borrows the shared home-DB pool, whose
// lifecycle belongs to server bootstrap, so closing it here would tear down
// connections still used by the rest of the process.
func (s *PGStorage) Close() error {
	return nil
}

// DeleteExpired removes rows whose expiry has passed. It backs the scheduled
// `platform.idempotency_gc` cron job so expiry cleanup rides the Phase 2
// scheduler instead of the request path.
func (s *PGStorage) DeleteExpired(ctx context.Context) (int64, error) {
	res, err := s.db.NewRaw(
		fmt.Sprintf("DELETE FROM %s WHERE expitbl_at IS NOT NULL AND expitbl_at <= now()", storageTable),
	).Exec(ctx)
	if err != nil {
		return 0, err
	}
	affected, countErr := res.RowsAffected()
	if countErr != nil {
		return 0, countErr
	}
	return affected, nil
}
