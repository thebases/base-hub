package cmd

// File overview:
// - Responsibility: implements command-line workflow for import data so operational tasks reuse the same config and bootstrap rules as the main service.
// - Key symbols: `importModuleData`, `init`.
// - Pattern: Threads `context.Context` through the main execution paths so cancellation, auth scope, and transaction state can follow the work end to end.
// - Pattern: Reads operational behavior from shared config so environments can tune the same code paths without forking implementation logic.

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"

	"git.thebase.vn/base_hub/internal/config"
	kernelmodules "git.thebase.vn/base_hub/internal/modules"
	"git.thebase.vn/base_hub/internal/services/log"
	"git.thebase.vn/base_hub/internal/services/masterdata"
	"github.com/spf13/cobra"
)

var importDataPath string
var importDataForce bool

var importDataCmd = &cobra.Command{
	Use:   "import-data",
	Short: "Import JSON and CSV master data into runtime models",
	RunE:  runImportDataCommand,
}

func runImportDataCommand(cmd *cobra.Command, args []string) error {
	ctx := context.Background()
	cmd.Flags().Parse(args)

	config.LoadConfig(configPath)
	if !config.Get().PostgresqlDb.Enable {
		log.Errorf("postgresql.enable must be true")
		return fmt.Errorf("postgresql.enable must be true")
	}

	dbInstances, cleanupDBs, err := initConfiguredDatabases(ctx)
	if err != nil {
		log.Errorf("failed to init PostgreSQL DB: %v", err)
		return fmt.Errorf("failed to init PostgreSQL DB: %w", err)
	}
	defer cleanupDBs.Close()

	shutdownModels := initRuntimeModels()
	defer shutdownModels()

	if _, err := migrateConfiguredDatabases(ctx, dbInstances); err != nil {
		log.Errorf("runtime model registry auto-migrate failed: %v", err)
		return fmt.Errorf("runtime model registry auto-migrate failed: %w", err)
	}

	log.Init(config.Get().Logging.Path)
	log.SetLevel(log.Level(config.Get().Logging.Level))

	report, source, err := importModuleData(ctx, importDataPath, importDataForce)
	if err != nil {
		log.Errorf("module data import failed from %s: %v", source, err)
		return fmt.Errorf("module data import failed from %s: %w", source, err)
	}

	logImportDataReport(report, source)
	return nil
}

func logImportDataReport(report *masterdata.Report, source string) {
	for _, fileReport := range report.Files {
		log.Infof(
			"%s created=%d updated=%d skipped=%d",
			fileReport.Path,
			fileReport.Created,
			fileReport.Updated,
			fileReport.Skipped,
		)
	}
	log.Infof(
		"source=%s total created=%d updated=%d skipped=%d",
		source,
		report.Created,
		report.Updated,
		report.Skipped,
	)
}

func importModuleData(ctx context.Context, path string, force bool) (*masterdata.Report, string, error) {
	service := masterdata.NewService()
	if force {
		service.SetForceOverrideByBID(true)
	}
	if info, err := os.Stat(path); err == nil {
		if !info.IsDir() {
			report, importErr := service.ImportPath(ctx, path)
			return report, path, importErr
		}
		roots, discoverErr := kernelmodules.DiscoverImportRootsFromDisk(path)
		if discoverErr != nil {
			return nil, path, discoverErr
		}
		if len(roots) == 0 {
			report, importErr := service.ImportPath(ctx, path)
			return report, path, importErr
		}
		report, importErr := service.ImportPaths(ctx, roots)
		return report, path, importErr
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		log.Errorf("stat %s: %v", path, err)
		return nil, path, fmt.Errorf("stat %s: %w", path, err)
	}

	if path == kernelmodules.DefaultRoot && embeddedAssets.Modules != nil {
		roots, discoverErr := kernelmodules.DiscoverImportRootsFromFS(embeddedAssets.Modules, ".")
		if discoverErr != nil {
			return nil, "embedded modules", discoverErr
		}
		report, importErr := service.ImportFSRoots(ctx, embeddedAssets.Modules, roots)
		return report, "embedded modules", importErr
	}
	log.Errorf("%s not found", path)
	return nil, path, fmt.Errorf("%s not found", path)
}

func init() {
	rootCmd.AddCommand(importDataCmd)
	importDataCmd.Flags().StringVarP(&configPath, "config", "c", "./build/config.yaml", "Path to config file")
	importDataCmd.Flags().BoolVar(&forceConfigSync, "fc", false, "Force override sys.config_param from the loaded config file")
	importDataCmd.Flags().BoolVar(&forceConfigSync, "force-config", false, "Force override sys.config_param from the loaded config file")
	importDataCmd.Flags().BoolVarP(&importDataForce, "force", "f", false, "Force-update existing bid-matched master-data records, including embedded module data")
	importDataCmd.Flags().StringVarP(&importDataPath, "path", "p", kernelmodules.DefaultRoot, "Module root or module data directory to import")
}
