package service

// Covers T69: the signing key and activation secret cache only a successful load (a failed
// first load is retried), concurrent callers share one load, and nodes racing to create the
// secret converge on the single stored row (insert-if-absent, then re-read).

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
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

func TestActivationSecretRetriesAfterFailedLoad(t *testing.T) {
	t.Cleanup(SetActivationCodeSecretForTests(""))
	var calls atomic.Int32
	prev := loadActivationSecret
	loadActivationSecret = func(context.Context) (string, error) {
		if calls.Add(1) == 1 {
			return "", errors.New("home db not ready")
		}
		return "stored-secret", nil
	}
	t.Cleanup(func() { loadActivationSecret = prev })

	if _, err := LoadOrCreateActivationCodeSecret(context.Background()); err == nil {
		t.Fatal("first load: want error")
	}
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if got, err := LoadOrCreateActivationCodeSecret(context.Background()); err != nil || got != "stored-secret" {
				t.Errorf("retry: got %q, %v", got, err)
			}
		}()
	}
	wg.Wait()
	if n := calls.Load(); n != 2 {
		t.Fatalf("loader calls = %d, want 2 (one failure, one cached success)", n)
	}
}

func TestSigningKeyRetriesAfterFailedLoad(t *testing.T) {
	t.Cleanup(SetSigningKeyForTests(nil))
	key, err := NewEphemeralSigningKeyForTests()
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	prev := loadSigningKey
	loadSigningKey = func(context.Context) (*SigningKey, error) {
		if calls.Add(1) == 1 {
			return nil, errors.New("home db not ready")
		}
		return key, nil
	}
	t.Cleanup(func() { loadSigningKey = prev })

	if _, err := LoadOrCreateSigningKey(context.Background()); err == nil {
		t.Fatal("first load: want error")
	}
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if got, err := LoadOrCreateSigningKey(context.Background()); err != nil || got != key {
				t.Errorf("retry: got %v, %v", got, err)
			}
		}()
	}
	wg.Wait()
	if n := calls.Load(); n != 2 {
		t.Fatalf("loader calls = %d, want 2", n)
	}
}

// setupConfigParamDB opens a search_path-pinned handle on a throwaway schema holding only
// sys_config_param. Skips when Postgres is unreachable.
func setupConfigParamDB(t *testing.T) *bun.DB {
	t.Helper()
	ctx := context.Background()
	cfg := testutil.EnvPostgresConfig()
	admin, err := orm.NewBunDB(cfg)
	if err != nil {
		t.Skipf("open postgres fixture: %v", err)
	}
	t.Cleanup(func() { _ = admin.Close() })
	schema := fmt.Sprintf("auth_secret_test_%d", time.Now().UnixNano())
	if _, err := admin.DB.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Skipf("create schema (postgres unreachable?): %v", err)
	}
	t.Cleanup(func() { _, _ = admin.DB.ExecContext(ctx, "DROP SCHEMA "+schema+" CASCADE") })
	poolCfg, err := pgxpool.ParseConfig(fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
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
	db := bun.NewDB(stdlib.OpenDBFromPool(pool), pgdialect.New())
	t.Cleanup(func() { _ = db.Close(); pool.Close() })
	if _, err := db.ExecContext(ctx, `CREATE TABLE sys_config_param (name TEXT PRIMARY KEY, value JSONB)`); err != nil {
		t.Fatal(err)
	}
	return db
}

// TestRuntimeSecretsConvergeAcrossNodes simulates nodes booting together: each runs the
// create path concurrently and all must end up with the one stored value.
func TestRuntimeSecretsConvergeAcrossNodes(t *testing.T) {
	db := setupConfigParamDB(t)
	ctx := context.Background()
	const nodes = 4
	secrets := make([]string, nodes)
	keys := make([]*SigningKey, nodes)
	errs := make([]error, 2*nodes)
	var wg sync.WaitGroup
	for i := range nodes {
		wg.Add(2)
		go func() {
			defer wg.Done()
			secrets[i], errs[i] = loadOrCreateActivationCodeSecretFromDB(ctx, db)
		}()
		go func() {
			defer wg.Done()
			keys[i], errs[nodes+i] = loadOrCreateSigningKeyFromDB(ctx, db)
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("load %d: %v", i, err)
		}
	}
	for i := 1; i < nodes; i++ {
		if secrets[i] != secrets[0] {
			t.Fatalf("node %d secret differs: nodes did not converge", i)
		}
		if !keys[i].PrivateKey.Equal(keys[0].PrivateKey) || keys[i].KeyID != keys[0].KeyID {
			t.Fatalf("node %d signing key differs: nodes did not converge", i)
		}
	}
	stored, err := readConfigParamString(ctx, db, activationCodeSecretConfigParam)
	if err != nil || stored != secrets[0] {
		t.Fatalf("stored secret = %q, %v; want the returned one", stored, err)
	}
}

// SetActivationCodeSecretForTests overrides the process-wide activation
// secret so DB-free unit tests can issue/verify activation codes without a
// live Postgres connection. Returns a restore func the caller must invoke
// (typically via t.Cleanup) once done.
func SetActivationCodeSecretForTests(secret string) (restore func()) {
	activationSecretMu.Lock()
	prevValue := activationSecretValue
	activationSecretValue = secret
	activationSecretMu.Unlock()
	return func() {
		activationSecretMu.Lock()
		activationSecretValue = prevValue
		activationSecretMu.Unlock()
	}
}
