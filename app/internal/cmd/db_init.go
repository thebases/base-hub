package cmd

// File overview:
// - Responsibility: `init-db` CLI command — creates a named PostgreSQL database,
//   migrates the schema, and seeds master data for all modules.
// - Usage: base-hub init-db --config ./build/config.yaml <logical-name>
//   where <logical-name> is the PostgreSQL database name verbatim (dots allowed).

import (
	"context"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"git.thebase.vn/base_hub/internal/config"
	kernelmodules "git.thebase.vn/base_hub/internal/modules"
	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
	"github.com/spf13/cobra"
)

var initDBCmd = &cobra.Command{
	Use:   "init-db <name>",
	Short: "Initialize a named database: create, migrate schema, seed master data",
	Long: `init-db creates the PostgreSQL database <name> if it does not exist,
runs all schema migrations, and seeds master data for all modules (base, workflow, mcp).

<name> is the PostgreSQL database name used verbatim (dots are allowed, e.g. hub.thebase.vn).`,
	Args: cobra.ExactArgs(1),
	RunE: runInitDBCommand,
}

func runInitDBCommand(cmd *cobra.Command, args []string) error {
	config.LoadConfig(configPath)

	// If the configured server port is already bound (a running server instance),
	// bump by 1 so any internal worker socket opened by this CLI does not collide.
	if port := config.Get().Server.Port; port > 0 {
		if ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port)); err != nil {
			config.Update(func(c *config.AppConfig) { c.Server.Port = port + 1 })
			log.Infof("init-db: port %d is in use, using %d for this run", port, port+1)
		} else {
			_ = ln.Close()
		}
	}

	ctx := context.Background()
	logicalName := strings.TrimSpace(args[0])
	if logicalName == "" {
		return fmt.Errorf("init-db: database name must not be empty")
	}

	hooks, ok := kernelmodules.DatabaseProvisioningHooks()
	if !ok {
		return fmt.Errorf("init-db: database module not installed: cannot bootstrap database %q", logicalName)
	}

	// Phase 1: Connect to the target database BEFORE registering models.
	// Module bootstrap routines (e.g. workflow trigger sync) execute DB reads
	// during initRuntimeModels(). Connecting first means GetBunDB() returns a
	// valid handle so those reads succeed instead of panicking on a nil *bun.DB.
	if err := hooks.ConnectDatabase(ctx, logicalName); err != nil {
		return fmt.Errorf("init-db: %w", err)
	}

	// Phase 2: Register all runtime models. The DB is now live so handler
	// repos capture a real connection and module bootstraps can run safely.
	shutdownModels := initRuntimeModels()
	defer shutdownModels()

	// Phase 3: Migrate schema, sync catalog, seed master data.
	start := time.Now()
	initErr := hooks.SetupDatabase(ctx, logicalName)
	elapsed := time.Since(start)
	if registeredDB := orm.ResolveRegisteredDatabase(logicalName); registeredDB != nil {
		orm.SetNamedBunDB(logicalName, nil)
		_ = registeredDB.Close()
	}

	if initErr != nil {
		fmt.Fprintf(os.Stderr, "\n--- init-db result ---\nstatus:   failed\ndatabase: %s\nerror:    %v\nelapsed:  %s\n",
			logicalName, initErr, elapsed.Round(time.Millisecond))
		return initErr
	}
	fmt.Printf("\n--- init-db result ---\nstatus:   ok\ndatabase: %s\nelapsed:  %s\n",
		logicalName, elapsed.Round(time.Millisecond))
	return nil
}

func init() {
	rootCmd.AddCommand(initDBCmd)
	initDBCmd.Flags().StringVarP(&configPath, "config", "c", "./build/config.yaml", "Path to config file")
}
