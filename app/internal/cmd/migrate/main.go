package main

// File overview:
// - Responsibility: provides the standalone migration executable entrypoint so schema tasks can run without starting the HTTP server.
// - Key symbols: `connectDB`, `main`.
// - Pattern: Uses Bun-facing model or repository patterns so schema mapping stays explicit and transaction-aware inside the backend data layer.
// - Pattern: Threads `context.Context` through the main execution paths so cancellation, auth scope, and transaction state can follow the work end to end.

import (
	"context"
	"database/sql"
	"embed"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"git.thebase.vn/base_hub/internal/config"
	servicelog "git.thebase.vn/base_hub/internal/services/log"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/driver/pgdriver"
	"github.com/uptrace/bun/migrate"
)

// migrationFiles embeds the .up.sql/.down.sql set at compile time so a
// compiled `migrate` binary is self-contained. DiscoverCaller() (bun's other
// option) resolves migrations via runtime.Caller against the *build
// machine's* absolute source path baked into the binary — fine for `go run`
// from a checkout, but it silently fails to find any migrations once the
// binary is compiled and shipped to a different host (exactly the failure
// mode `make build`/`make deploy` need to avoid; see backend-context.md).
//
//go:embed migrations
var migrationFiles embed.FS

// connectDB opens a Bun DB against cfg. cfg.Name must already be resolved by
// the caller: the shared PostgresqlConfig leaves Name blank (it's filled in
// per logical DB right before dialing elsewhere in the codebase), and dialing
// with an empty name lands on whatever database libpq defaults to rather than
// a database this tool ever provisioned or granted schema ownership on.
func connectDB(cfg config.PostgresqlConfig) *bun.DB {
	if !cfg.Enable {
		servicelog.Fatalf("PostgreSQL is disabled in config (postgresql.enable = false)")
	}

	dsn := fmt.Sprintf(
		"postgres://%s:%s@%s:%d/%s?sslmode=%s",
		cfg.User,
		cfg.Password,
		cfg.Host,
		cfg.Port,
		cfg.Name,
		cfg.SSLMode,
	)

	sqldb := sql.OpenDB(pgdriver.NewConnector(pgdriver.WithDSN(dsn)))
	db := bun.NewDB(sqldb, pgdialect.New())
	return db
}

// main is the process entrypoint and hands control to the configured application bootstrap path.
// The function normalizes missing context up front so downstream helpers can assume a usable request scope.
func main() {
	// CLI flags
	configPath := flag.String("config", "config.yaml", "Path to YAML config file")
	dbName := flag.String("db", "", "Logical database name to migrate (default: the configured home DB)")
	flag.Usage = printUsage
	// Parse the incoming payload into the shape the rest of the function expects.
	flag.Parse()

	if flag.NArg() == 0 {
		flag.Usage()
		os.Exit(1)
	}
	cmd := flag.Arg(0)

	// 1) Load config (your existing LoadConfig signature)
	config.LoadConfig(*configPath)
	servicelog.Init(config.Get().Logging.Path)
	servicelog.SetLevel(servicelog.Level(config.Get().Logging.Level))

	// 2) Connect DB via Bun. cfg.Name is never populated by LoadConfig, so
	// resolve the target explicitly: an explicit -db flag wins, otherwise
	// fall back to the configured home DB.
	cfg := config.Get().PostgresqlDb
	cfg.Name = strings.TrimSpace(*dbName)
	if cfg.Name == "" {
		cfg.Name = cfg.ResolvedHomeDBName()
	}
	db := connectDB(cfg)
	defer closeDB(db)
	servicelog.Infof("=====>>>>> CONNECT DB SUCCESSFUL")

	ctx := context.Background()

	// 3) Setup Bun migrations from the embedded migrations/ directory (see
	// migrationFiles above) instead of walking disk relative to the binary's
	// build-time source path.
	migrationsFS, err := fs.Sub(migrationFiles, "migrations")
	if err != nil {
		servicelog.Fatalf("=====>>>>> prepare embedded migrations: %v", err)
	}
	migrations := migrate.NewMigrations()
	if err := migrations.Discover(migrationsFS); err != nil {
		servicelog.Fatalf("=====>>>>> discover migrations: %v", err)
	}
	servicelog.Infof("=====>>>>> Start migrator ......")
	migrator := migrate.NewMigrator(db, migrations)

	// Ensure migrations table exists
	if err := migrator.Init(ctx); err != nil {
		servicelog.Fatalf("init migrator: %v", err)
	}

	// 4) Handle commands
	switch cmd {

	case "up":
		group, err := migrator.Migrate(ctx)
		if err != nil {
			servicelog.Fatalf("=====>>>>> Migrate up failed: %v", err)
		}
		if group.IsZero() {
			servicelog.Info("No new migrations to apply.")
		} else {
			servicelog.Infof("Applied migration group: %s", group.String())
		}

	case "down":
		group, err := migrator.Rollback(ctx)
		if err != nil {
			servicelog.Fatalf("migrate down failed: %v", err)
		}
		if group.IsZero() {
			servicelog.Info("No migrations to roll back.")
		} else {
			servicelog.Infof("Rolled back migration group: %s", group.String())
		}

	case "reset":
		if err := migrator.Reset(ctx); err != nil {
			servicelog.Fatalf("migrate reset failed: %v", err)
		}
		servicelog.Info("All migrations reset (rolled back).")

	case "status":
		ms, err := migrator.MigrationsWithStatus(ctx)
		servicelog.Infof("=====>>>>> Migrator status ......")
		if err != nil {
			servicelog.Fatalf("get migration status failed: %v", err)
		}
		for _, m := range ms {
			status := "down"
			if m.IsApplied() {
				status = "up"
			}
			servicelog.Infof("%-40s | %s", m.Name, status)
		}

	default:
		flag.Usage()
		os.Exit(1)
	}
}

func printUsage() {
	servicelog.Info("Usage:")
	servicelog.Info("  migrate -config=config.yaml [-db=name] <command>")
	servicelog.Info("")
	servicelog.Info("Commands:")
	servicelog.Info("  up       Apply all pending migrations")
	servicelog.Info("  down     Roll back the last migration group")
	servicelog.Info("  reset    Roll back all migrations")
	servicelog.Info("  status   Show migration status")
}

func closeDB(db *bun.DB) {
	if db != nil {
		_ = db.Close()
	}
}
