package orm

// File overview:
// - Responsibility: implements the metadata-driven ORM/runtime kernel for postgresql, where storage, runtime dispatch, and security rules stay centralized.
// - Key symbols: `GetBunDB`, `GetPGXPool`, `BunDB`.
// - Pattern: Leans on metadata-driven registration so storage mapping, runtime methods, and behavior hooks stay centralized instead of being scattered across handlers.
// - Pattern: Uses Bun-facing model or repository patterns so schema mapping stays explicit and transaction-aware inside the backend data layer.

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"git.thebase.vn/base_hub/internal/config"
	"git.thebase.vn/base_hub/internal/services/log"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"golang.org/x/sync/singleflight"
)

var dbNameSanitizeRe = regexp.MustCompile(`[^a-z0-9]+`)

// SanitizeDBName converts a logical name (e.g. a hostname such as
// "hub.thebase.vn") into a valid lower-case PostgreSQL identifier.
func SanitizeDBName(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = dbNameSanitizeRe.ReplaceAllString(s, "_")
	return strings.Trim(s, "_")
}

type BunDB struct {
	DB   *bun.DB
	Pool *pgxpool.Pool
}

var bunDBRegistry = struct {
	mu          sync.RWMutex
	defaultName string
	instances   map[string]*BunDB
}{
	instances: map[string]*BunDB{},
}

var ensureGroup singleflight.Group

// GetBunDB returns the global BunDB instance
func GetBunDB() *bun.DB {
	return ResolveBunDB("")
}

// GetPGXPool returns the default shared pgx pool for SQL-owned workflows.
func GetPGXPool() *pgxpool.Pool {
	db := ResolveDatabase("")
	if db == nil {
		return nil
	}
	return db.Pool
}

// BunDBFromContext returns the request-bound Bun handle when the ORM
// environment carries one, otherwise it falls back to the default database.
func BunDBFromContext(ctx context.Context) *bun.DB {
	if env := EnvironmentFromContext(ctx); env != nil {
		if envName := strings.TrimSpace(env.DBName()); envName != "" {
			if db, err := EnsureNamedBunDB(envName); err == nil && db != nil {
				return db.DB
			}
		}
		if db, ok := env.DB().(*bun.DB); ok && db != nil {
			return db
		}
		if db := ResolveBunDB(env.DBName()); db != nil {
			return db
		}
	}
	return GetBunDB()
}

// PGXPoolFromContext returns the request-bound pgx pool when the ORM
// environment carries a logical database name, otherwise it falls back to the
// default pool.
func PGXPoolFromContext(ctx context.Context) *pgxpool.Pool {
	if env := EnvironmentFromContext(ctx); env != nil {
		if envName := strings.TrimSpace(env.DBName()); envName != "" {
			if db, err := EnsureNamedBunDB(envName); err == nil && db != nil {
				return db.Pool
			}
		}
		if pool := ResolvePGXPool(env.DBName()); pool != nil {
			return pool
		}
	}
	return GetPGXPool()
}

// SetBunDB sets the global BunDB instance
func SetBunDB(db *BunDB) {
	// The process-wide default logical DB is the home control plane; a data-plane
	// database must not silently become the default handle.
	defaultName := strings.TrimSpace(config.HomeDBName())
	if defaultName == "" {
		defaultName = "default"
	}
	SetNamedBunDB(defaultName, db)
}

// SetNamedBunDB registers one Bun database under a stable logical name.
func SetNamedBunDB(name string, db *BunDB) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "default"
	}

	bunDBRegistry.mu.Lock()
	defer bunDBRegistry.mu.Unlock()

	if db == nil || db.DB == nil {
		delete(bunDBRegistry.instances, name)
		if bunDBRegistry.defaultName == name {
			bunDBRegistry.defaultName = firstRegisteredDBNameLocked()
		}
		return
	}

	bunDBRegistry.instances[name] = db
	if bunDBRegistry.defaultName == "" {
		bunDBRegistry.defaultName = name
	}
}

// SetDefaultBunDBName marks one registered database as the default fallback.
func SetDefaultBunDBName(name string) {
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	bunDBRegistry.mu.Lock()
	defer bunDBRegistry.mu.Unlock()
	if _, ok := bunDBRegistry.instances[name]; ok {
		bunDBRegistry.defaultName = name
	}
}

// ResolveLogicalDBName returns name, or the default logical database name when name is blank, so
// process-global registries can key one database the same way whether the caller's context named
// it explicitly or fell back to the default handle.
func ResolveLogicalDBName(name string) string {
	return resolvedLogicalDBName(name)
}

func resolvedLogicalDBName(name string) string {
	name = strings.TrimSpace(name)
	if name != "" {
		return name
	}

	bunDBRegistry.mu.RLock()
	defaultName := strings.TrimSpace(bunDBRegistry.defaultName)
	bunDBRegistry.mu.RUnlock()
	if defaultName != "" {
		return defaultName
	}

	// When nothing is registered yet, fall back to the home control-plane DB.
	if configured := strings.TrimSpace(config.HomeDBName()); configured != "" {
		return configured
	}
	return "default"
}

// Ping budgets for registeredDBUsable: wider on the request hot path, tighter
// inside the reconnect singleflight where a reconnect is already suspected.
// Variables so tests can shorten them.
var (
	registeredDBPingTimeout = 5 * time.Second
	reconnectDBPingTimeout  = 2 * time.Second
)

// registeredDBUsable reports nil when a registered handle can keep serving.
// A pool with live connections is trusted without a round trip: pgxpool drops
// broken connections itself, so only a pool with zero connections (closed, or
// every dial failing) is probed. A probe that times out is treated as usable,
// since under pool exhaustion the ping only waits for a free connection and a
// busy pool must never be replaced and closed under in-flight requests.
func registeredDBUsable(db *BunDB, timeout time.Duration) error {
	if db != nil && db.Pool != nil && db.Pool.Stat().TotalConns() > 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	err := pingRegisteredBunDB(ctx, db)
	if err != nil && isPingTimeout(err) {
		log.Warnf("orm: database ping timed out, keeping the registered pool: %v", err)
		return nil
	}
	return err
}

// isPingTimeout matches a ping that ran out of time (context deadline or a
// pgconn/net timeout), as opposed to a refused, closed or failed connection.
func isPingTimeout(err error) bool {
	return errors.Is(err, context.DeadlineExceeded) || pgconn.Timeout(err)
}

func pingRegisteredBunDB(ctx context.Context, db *BunDB) error {
	if db == nil || db.DB == nil {
		return fmt.Errorf("bun db is not initialized")
	}

	var one int
	if err := db.DB.NewRaw(`SELECT 1`).Scan(ctx, &one); err != nil {
		return err
	}
	if one != 1 {
		return fmt.Errorf("unexpected database ping result: %d", one)
	}
	return nil
}

// EnsureNamedBunDB returns one open named logical database, reconnecting it
// from config when the registry still holds a stale or closed handle, and
// auto-creating it in Postgres if it doesn't exist yet. Only call this with a
// name from a trusted source (config, an already-provisioned control-plane
// record) — see EnsureNamedBunDBNoCreate for names that originate from
// unauthenticated request input.
// Concurrent calls for the same name share one reconnect attempt via singleflight.
func EnsureNamedBunDB(name string) (*BunDB, error) {
	return ensureNamedBunDB(name, true)
}

// EnsureNamedBunDBNoCreate behaves like EnsureNamedBunDB but never creates a
// missing database. Use this for any name sourced from unauthenticated
// request input (X-Database-Name header, ?db=/db form field, a DB-selector
// cookie) — EnsureNamedBunDB's auto-create path uses admin credentials, so
// wiring it directly off client-controlled input would let anyone spray-create
// arbitrary Postgres databases just by naming one that doesn't exist yet.
func EnsureNamedBunDBNoCreate(name string) (*BunDB, error) {
	return ensureNamedBunDB(name, false)
}

func ensureNamedBunDB(name string, allowCreate bool) (*BunDB, error) {
	logicalName := resolvedLogicalDBName(name)

	// Exact match only: ResolveDatabase's default-name fallback would let a
	// not-yet-registered logical name silently reuse whichever database
	// happens to be open as the process default instead of ever attempting to
	// open the one actually requested.
	if registered := ResolveRegisteredDatabase(logicalName); registered != nil {
		// Hot path of every DB resolution: no per-call SELECT 1 while the pool
		// holds live connections (registeredDBUsable). A ping that merely times
		// out means a busy pool, not a dead one; reconnecting then used to close
		// the shared pool under in-flight requests and turn contention into an
		// outage, so only a real connection error leads to a reconnect.
		pingErr := registeredDBUsable(registered, registeredDBPingTimeout)
		if pingErr == nil {
			return registered, nil
		}
		log.Warnf("orm: reconnecting stale database handle %q: %v", logicalName, pingErr)
	}

	// Keyed by name+allowCreate so a permissive caller (e.g. startup bootstrap)
	// racing a restricted one (e.g. a concurrent request) for the same name
	// never lets the restricted caller ride a create it didn't authorize.
	groupKey := logicalName
	if !allowCreate {
		groupKey += "|no-create"
	}
	result, err, _ := ensureGroup.Do(groupKey, func() (any, error) {
		return ensureNamedBunDBReconnect(logicalName, allowCreate)
	})
	if err != nil {
		return nil, err
	}
	return result.(*BunDB), nil
}

// NamedDBOpenHook runs against a database handle the first time this
// process opens it lazily (request-time DB selection by cookie/header/host),
// before the handle is registered and serves any query.
type NamedDBOpenHook func(ctx context.Context, logicalName string, db *bun.DB) error

// namedDBOpenHook is installed by core bootstrap; nil disables it. Stored
// atomically because request goroutines read it while boot may still set it.
var namedDBOpenHook atomic.Pointer[NamedDBOpenHook]

// namedDBOpenHookTimeout bounds the hook (typically a schema migration) so a
// wedged DDL lock can't hold the singleflight group open indefinitely.
const namedDBOpenHookTimeout = 2 * time.Minute

// SetNamedDBOpenHook installs (or, with nil, clears) the first-open hook.
func SetNamedDBOpenHook(hook NamedDBOpenHook) {
	if hook == nil {
		namedDBOpenHook.Store(nil)
		return
	}
	namedDBOpenHook.Store(&hook)
}

// runNamedDBOpenHook executes the installed first-open hook. Its error is
// returned so the caller leaves the DB unregistered (BUG-K-6): a handle whose
// schema is behind the registered models must not serve requests.
func runNamedDBOpenHook(logicalName string, db *BunDB) error {
	hook := namedDBOpenHook.Load()
	if hook == nil || *hook == nil || db == nil || db.DB == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), namedDBOpenHookTimeout)
	defer cancel()
	if err := (*hook)(ctx, logicalName, db.DB); err != nil {
		log.Errorf("orm: first-open hook for database %q failed: %v", logicalName, err)
		return fmt.Errorf("orm: open database %q: schema migration failed: %w", logicalName, err)
	}
	return nil
}

// ensureNamedBunDBReconnect runs under singleflight: only one goroutine per
// (logicalName, allowCreate) executes this at a time, preventing duplicate
// reconnect pools.
func ensureNamedBunDBReconnect(logicalName string, allowCreate bool) (*BunDB, error) {
	// Re-check: a concurrent singleflight winner may have already reconnected.
	// Deliberately do NOT evict the registry entry yet even when the ping
	// fails: evicting here up front used to open a multi-second window (2
	// pings plus a fresh connect, worst case ~4s+) where every other
	// concurrent request for this still-healthy tenant DB saw it missing from
	// the registry and was rejected with a false-positive "requested database
	// is not available" 403 in production. Keep the stale handle registered
	// until the reconnect attempt's outcome is known, then evict/replace it
	// atomically. Exact match only (ResolveRegisteredDatabase, not
	// ResolveDatabase): the latter's default-name fallback would let a
	// not-yet-registered logicalName be "confirmed stale-but-healthy" against
	// the wrong (default) database instead of ever dialing the one requested.
	stale := ResolveRegisteredDatabase(logicalName)
	if stale != nil && registeredDBUsable(stale, reconnectDBPingTimeout) == nil {
		return stale, nil
	}

	cfg := resolveNamedDatabaseConfig(logicalName)
	reconnected, err := NewBunDB(cfg)
	if err != nil {
		// When a reconnect targets a logical database that was expected to exist,
		// create it through the shared admin credentials before retrying once —
		// only when the caller trusts this name enough to authorize creation.
		if allowCreate && shouldCreateNamedDatabaseOnReconnect(logicalName, err) {
			if createErr := createNamedDatabaseFromConfig(logicalName, cfg); createErr != nil {
				evictStaleNamedBunDB(logicalName, stale)
				return nil, fmt.Errorf("create missing database %q during reconnect: %w", logicalName, createErr)
			}
			reconnected, err = NewBunDB(cfg)
		}
	}
	if err != nil {
		evictStaleNamedBunDB(logicalName, stale)
		return nil, err
	}

	pingCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	pingErr := pingRegisteredBunDB(pingCtx, reconnected)
	cancel()
	if pingErr != nil {
		_ = reconnected.Close()
		evictStaleNamedBunDB(logicalName, stale)
		return nil, pingErr
	}

	// First open of this DB in the process (not a reconnect of a handle that
	// already passed the hook): bring its schema up to the registered models
	// before any request can query it. Runs inside the singleflight, so
	// concurrent requests for the same DB wait instead of racing the DDL.
	// A failed migration must not register the DB: it would serve "column does
	// not exist" until restart. Returning the error keeps it unregistered so
	// the next request retries the open and the migration.
	if stale == nil {
		if err := runNamedDBOpenHook(logicalName, reconnected); err != nil {
			_ = reconnected.Close()
			return nil, err
		}
	}

	SetNamedBunDB(logicalName, reconnected)
	if stale != nil {
		_ = stale.Close()
	}
	return reconnected, nil
}

// evictStaleNamedBunDB removes a confirmed-dead handle from the registry and
// closes it, once a reconnect attempt has actually failed. Called only from
// the terminal failure branches of ensureNamedBunDBReconnect, so healthy
// concurrent readers of RegisteredDBNames() never observe the gap that a
// pre-emptive evict used to create.
func evictStaleNamedBunDB(logicalName string, stale *BunDB) {
	if stale == nil {
		return
	}
	SetNamedBunDB(logicalName, nil)
	_ = stale.Close()
}

func resolveNamedDatabaseConfig(logicalName string) config.PostgresqlConfig {
	logicalName = strings.TrimSpace(logicalName)
	homeDBName := strings.TrimSpace(config.HomeDBName())
	bootstrapCfg := config.BootstrapPostgresqlConfig()

	// Keep explicit named database DSNs authoritative so reconnects preserve
	// per-database host, port, or credential overrides instead of inheriting the global runtime config.
	if namedCfg, ok := config.Get().PostgresqlDb.Databases[logicalName]; ok {
		if strings.TrimSpace(namedCfg.Name) == "" {
			namedCfg.Name = logicalName
		}
		return namedCfg
	}

	// Keep reconnects on the boot-time provisioning DSN so sys.config_param
	// rows in cloned or stale databases cannot silently retarget admin traffic.
	switch logicalName {
	case "", homeDBName:
		bootstrapCfg.Name = logicalName
		return bootstrapCfg
	default:
		if strings.TrimSpace(bootstrapCfg.Host) != "" {
			bootstrapCfg.Name = logicalName
			return bootstrapCfg
		}
	}

	runtimeCfg := config.Get().PostgresqlDb
	runtimeCfg.Name = logicalName
	return runtimeCfg
}

func shouldCreateNamedDatabaseOnReconnect(logicalName string, err error) bool {
	if strings.TrimSpace(logicalName) == "" || err == nil {
		return false
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "3D000" {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "does not exist")
}

func createNamedDatabaseFromConfig(logicalName string, cfg config.PostgresqlConfig) error {
	logicalName = strings.TrimSpace(logicalName)
	if logicalName == "" {
		return fmt.Errorf("logical database name is empty")
	}
	// CREATE DATABASE can block on template locks; bound the whole attempt.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	adminCfg := cfg
	// Reconnect-time auto-create rides the home control-plane DB for its admin
	// connection (never a data-plane target), with the pg-native "postgres" DB
	// as the last resort.
	adminCandidates := uniqueDatabaseNames(config.BootstrapHomeDBName(), "postgres")
	for _, candidate := range adminCandidates {
		if candidate == logicalName {
			continue
		}
		adminCfg.Name = candidate
		adminDB, err := NewBunDB(adminCfg)
		if err != nil {
			continue
		}

		createErr := func() error {
			defer adminDB.Close()

			pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			pingErr := pingRegisteredBunDB(pingCtx, adminDB)
			cancel()
			if pingErr != nil {
				return pingErr
			}

			exists, err := databaseExists(ctx, adminDB.DB, logicalName)
			if err != nil {
				return err
			}
			if exists {
				return nil
			}

			// Quote the identifier directly so logical names like hostnames stay
			// valid PostgreSQL database names during auto-create.
			quotedName := `"` + strings.ReplaceAll(logicalName, `"`, `""`) + `"`
			if _, err := adminDB.DB.ExecContext(ctx, fmt.Sprintf("CREATE DATABASE %s", quotedName)); err != nil {
				return err
			}
			return nil
		}()
		if createErr == nil {
			return nil
		}
	}
	return fmt.Errorf("no admin database connection could create %q", logicalName)
}

func databaseExists(ctx context.Context, db *bun.DB, dbName string) (bool, error) {
	if db == nil {
		return false, fmt.Errorf("bun db is not initialized")
	}
	var exists bool
	if err := db.NewRaw(`SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname = ?)`, dbName).
		Scan(ctx, &exists); err != nil {
		return false, err
	}
	return exists, nil
}

func uniqueDatabaseNames(names ...string) []string {
	seen := make(map[string]struct{}, len(names))
	unique := make([]string, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		unique = append(unique, name)
	}
	return unique
}

// ResolveBunDB returns the named Bun database, falling back to the default.
func ResolveBunDB(name string) *bun.DB {
	if db := ResolveDatabase(name); db != nil {
		return db.DB
	}
	return nil
}

// ResolvePGXPool returns the named pgx pool, falling back to the default.
func ResolvePGXPool(name string) *pgxpool.Pool {
	if db := ResolveDatabase(name); db != nil {
		return db.Pool
	}
	return nil
}

// ResolveDatabase returns the named logical database bundle, falling back to
// the default registration.
func ResolveDatabase(name string) *BunDB {
	if exact := ResolveRegisteredDatabase(name); exact != nil {
		return exact
	}
	bunDBRegistry.mu.RLock()
	defer bunDBRegistry.mu.RUnlock()
	if db := bunDBRegistry.instances[bunDBRegistry.defaultName]; db != nil {
		return db
	}
	return nil
}

// ResolveRegisteredDatabase returns only an exact logical database registration.
// Callers that manage database lifecycle must use this helper so a missing
// tenant name never silently falls back to the default/home connection.
func ResolveRegisteredDatabase(name string) *BunDB {
	bunDBRegistry.mu.RLock()
	defer bunDBRegistry.mu.RUnlock()

	name = strings.TrimSpace(name)
	if name != "" {
		if db := bunDBRegistry.instances[name]; db != nil {
			return db
		}
	}
	return nil
}

// RegisteredDBNames returns the configured logical database names.
func RegisteredDBNames() []string {
	bunDBRegistry.mu.RLock()
	defer bunDBRegistry.mu.RUnlock()

	names := make([]string, 0, len(bunDBRegistry.instances))
	for name := range bunDBRegistry.instances {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func firstRegisteredDBNameLocked() string {
	if len(bunDBRegistry.instances) == 0 {
		return ""
	}
	// Single O(n) scan to find the lexically smallest name avoids allocating a
	// full slice and sorting it just to return one element.
	var min string
	for name := range bunDBRegistry.instances {
		if min == "" || name < min {
			min = name
		}
	}
	return min
}

// NewBunDB constructs and returns the bun db handled by this package.
func NewBunDB(cfg config.PostgresqlConfig) (*BunDB, error) {
	// Use key=value DSN form so database names containing special characters
	// (e.g. dots in "hub.thebase.vn") are passed verbatim without URL encoding.
	dsn := fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		cfg.Host,
		cfg.Port,
		cfg.User,
		cfg.Password,
		cfg.Name,
		cfg.SSLMode,
	)
	// Health check on startup
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	poolConfig, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		log.Errorf("parse postgres config: %v", err)
		return nil, fmt.Errorf("parse postgres config: %w", err)
	}
	// Keep simple protocol enabled for Bun / SQL compatibility, but do it on the
	// parsed pool config so pgxpool keeps its internal initialization intact.
	poolConfig.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol

	// pgxpool's default MaxConns is max(4, runtime.NumCPU()) — on a small dev
	// box (or a constrained container) that is easily saturated by one page's
	// fan-out (e.g. expanding the chatter panel fires tbl.discuss_activity/
	// followers/message/activity_type search_reads concurrently alongside the
	// form's own queries). When the pool is saturated, EnsureNamedBunDB's
	// health-check ping can itself queue for a connection and trip its 2s
	// timeout, misreporting a merely-busy pool as a stale handle and forcing a
	// disruptive reconnect that makes the contention worse. Set an explicit,
	// more generous floor so ordinary request bursts don't starve the ping.
	poolConfig.MaxConns = 20
	poolConfig.MinConns = 2
	poolConfig.MaxConnLifetime = 30 * time.Minute
	poolConfig.MaxConnIdleTime = 5 * time.Minute

	dbpool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		log.Errorf("create postgres pool: %v", err)
		return nil, fmt.Errorf("create postgres pool: %w", err)
	}

	sqldb := stdlib.OpenDBFromPool(dbpool)

	// if err := sqldb.PingContext(ctx); err != nil {
	// 	return nil, err
	// }

	bunDB := bun.NewDB(sqldb, pgdialect.New())
	return &BunDB{DB: bunDB, Pool: dbpool}, nil
}

func (b *BunDB) Close() error {
	if b == nil {
		return nil
	}
	var firstErr error
	if b.DB != nil {
		if err := b.DB.DB.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	// Close the underlying pgx pool explicitly so SQL-owned helpers and the Bun
	// stdlib bridge share one well-defined lifecycle.
	if b.Pool != nil {
		// Tear down the resource cleanly so later calls do not inherit stale state.
		b.Pool.Close()
	}
	return firstErr
}
