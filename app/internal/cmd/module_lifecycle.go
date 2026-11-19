package cmd

// File overview:
// - Responsibility: wires internal/services/masterdata into internal/modules' ModuleDataRunner hook so module install/uninstall/reinit actions can replay or remove seed data without those two packages importing each other directly (masterdata already depends on internal/models, which depends on internal/modules).
// - Key symbols: `masterDataRunnerAdapter`.
// - Pattern: Mirrors the modules/database provisioning hook registration idiom (kernelmodules.RegisterDatabaseProvisioning).

import (
	"context"
	"os"

	kernelmodules "git.thebase.vn/base_hub/internal/modules"
	"git.thebase.vn/base_hub/internal/services/masterdata"
)

// masterDataRunnerAdapter satisfies kernelmodules.ModuleDataRunner by
// delegating to a fresh masterdata.Service per call.
type masterDataRunnerAdapter struct{}

// ImportModuleData imports the module handled by this package.
func (masterDataRunnerAdapter) ImportModuleData(ctx context.Context, roots []string, forceOverrideByBID bool) (created, updated, skipped int, err error) {
	service := masterdata.NewService()
	if forceOverrideByBID {
		service.SetForceOverrideByBID(true)
	}
	report, err := importModuleRoots(ctx, service, roots)
	if err != nil {
		return 0, 0, 0, err
	}
	return report.Created, report.Updated, report.Skipped, nil
}

// importModuleRoots dispatches roots to disk or embedded-FS import depending
// on which filesystem kernelmodules.DiscoverRuntimeImportRoots actually
// resolved them against. When the on-disk modules/ tree isn't present in the
// running process (packaged/production binaries), that discovery falls back
// to the embedded module bundle and returns FS-relative paths (e.g.
// "website/security") that os.Stat can never resolve, so those roots must go
// through ImportFSRoots against the same embedded fs.FS instead — mirroring
// the disk-then-embedded dispatch internal/cmd/import_data.go already uses.
func importModuleRoots(ctx context.Context, service *masterdata.Service, roots []string) (*masterdata.Report, error) {
	if len(roots) > 0 {
		if _, err := os.Stat(roots[0]); err != nil {
			if fsys := kernelmodules.EmbeddedFS(); fsys != nil {
				return service.ImportFSRoots(ctx, fsys, roots)
			}
		}
	}
	return service.ImportPaths(ctx, roots)
}

// DeleteModuleData deletes the module data handled by this package.
func (masterDataRunnerAdapter) DeleteModuleData(ctx context.Context, moduleName string) (map[string]int, error) {
	report, err := masterdata.NewService().DeleteModuleData(ctx, moduleName)
	if report == nil {
		return nil, err
	}
	return report.Deleted, err
}

// init registers this package's master-data adapter during bootstrap so
// module install/uninstall/reinit actions can reach it before requests arrive.
func init() {
	kernelmodules.RegisterModuleDataRunner(masterDataRunnerAdapter{})
}
