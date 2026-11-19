package orm

import (
	"context"
	"errors"
	"fmt"
	"net"
	"testing"
	"time"

	"git.thebase.vn/base_hub/internal/config"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
)

// swapBunDBRegistry gives a test an empty registry and restores the previous one afterwards.
func swapBunDBRegistry(t *testing.T) {
	t.Helper()
	bunDBRegistry.mu.Lock()
	previousDefault := bunDBRegistry.defaultName
	previous := bunDBRegistry.instances
	bunDBRegistry.instances = map[string]*BunDB{}
	bunDBRegistry.defaultName = ""
	bunDBRegistry.mu.Unlock()
	t.Cleanup(func() {
		bunDBRegistry.mu.Lock()
		bunDBRegistry.instances = previous
		bunDBRegistry.defaultName = previousDefault
		bunDBRegistry.mu.Unlock()
	})
}

// silentPostgres accepts TCP connections and never answers, like a server whose pool is
// exhausted: every ping waits until its deadline.
func silentPostgres(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	var held []net.Conn
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			held = append(held, conn)
		}
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		for _, conn := range held {
			_ = conn.Close()
		}
	})
	return ln.Addr().String()
}

func TestEnsureNamedBunDBKeepsPoolWhenPingTimesOut(t *testing.T) {
	swapBunDBRegistry(t)
	previousTimeout := registeredDBPingTimeout
	registeredDBPingTimeout = 200 * time.Millisecond
	t.Cleanup(func() { registeredDBPingTimeout = previousTimeout })

	addr := silentPostgres(t)
	poolCfg, err := pgxpool.ParseConfig(fmt.Sprintf("postgres://u:p@%s/tenant_busy?sslmode=disable", addr))
	if err != nil {
		t.Fatalf("parse pool config: %v", err)
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), poolCfg)
	if err != nil {
		t.Fatalf("new pool: %v", err)
	}
	busy := &BunDB{DB: bun.NewDB(stdlib.OpenDBFromPool(pool), pgdialect.New()), Pool: pool}
	t.Cleanup(func() { _ = busy.Close() })
	SetNamedBunDB("tenant_busy", busy)

	got, err := EnsureNamedBunDBNoCreate("tenant_busy")
	if err != nil {
		t.Fatalf("EnsureNamedBunDBNoCreate: %v", err)
	}
	if got != busy || ResolveRegisteredDatabase("tenant_busy") != busy {
		t.Fatalf("a ping timeout replaced or evicted the registered pool")
	}
	// The pool is closed only together with replacing or evicting its registry
	// entry, so an unchanged entry also means the pool stayed open.
}

func TestIsPingTimeout(t *testing.T) {
	if !isPingTimeout(fmt.Errorf("ping: %w", context.DeadlineExceeded)) {
		t.Fatal("deadline exceeded must count as a timeout")
	}
	if isPingTimeout(errors.New("connection refused")) {
		t.Fatal("a refused connection is a real connection error")
	}
}

func TestEnsureNamedBunDBDoesNotRegisterWhenFirstOpenMigrationFails(t *testing.T) {
	cfg := runtimeGraphEnvPostgresConfig()
	if probeDB, err := NewBunDB(cfg); err != nil || pingRegisteredBunDB(context.Background(), probeDB) != nil {
		t.Skip("postgres test database unavailable")
	} else {
		_ = probeDB.Close()
	}
	swapBunDBRegistry(t)
	previousConfig := *config.Get()
	t.Cleanup(func() { config.Set(previousConfig) })
	config.Update(func(c *config.AppConfig) {
		c.PostgresqlDb.Databases = map[string]config.PostgresqlConfig{"tenant_lazy": cfg}
	})
	errMigrate := errors.New("add column failed")
	calls := 0
	SetNamedDBOpenHook(func(context.Context, string, *bun.DB) error {
		calls++
		if calls == 1 {
			return errMigrate
		}
		return nil
	})
	t.Cleanup(func() { SetNamedDBOpenHook(nil) })

	if _, err := EnsureNamedBunDBNoCreate("tenant_lazy"); !errors.Is(err, errMigrate) {
		t.Fatalf("first open err = %v, want the migration error", err)
	}
	if ResolveRegisteredDatabase("tenant_lazy") != nil {
		t.Fatal("a DB whose first-open migration failed was registered")
	}
	db, err := EnsureNamedBunDBNoCreate("tenant_lazy")
	if err != nil || db == nil || calls != 2 {
		t.Fatalf("retry: db=%v err=%v hook calls=%d, want registered after a second migration", db, err, calls)
	}
	t.Cleanup(func() { _ = db.Close() })
}
