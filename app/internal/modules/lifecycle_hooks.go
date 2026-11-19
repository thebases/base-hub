package modules

// File overview:
// - Responsibility: lets the master-data import service supply seed import/delete behavior to module lifecycle actions without a compile-time import (masterdata already depends on internal/models, which itself depends on this package, so a direct import here would cycle).
// - Key symbols: ModuleDataRunner, RegisterModuleDataRunner, RegisterModuleSeededHook, runModuleSeededHooks.
// - Pattern: Mirrors the DatabaseProvisioning hook idiom in database_hooks.go.

import (
	"context"
	"strings"
	"sync"
)

// ModuleDataRunner is implemented by internal/services/masterdata's adapter
// (registered from internal/cmd, which can safely import both packages) so
// module install/uninstall/reinit can replay or remove seed data without this
// package importing masterdata directly.
type ModuleDataRunner interface {
	ImportModuleData(ctx context.Context, roots []string, forceOverrideByBID bool) (created, updated, skipped int, err error)
	DeleteModuleData(ctx context.Context, moduleName string) (deletedByModel map[string]int, err error)
}

var moduleDataRunnerImpl ModuleDataRunner

// RegisterModuleDataRunner lets the process wiring supply its master-data
// implementation once bootstrap runs.
func RegisterModuleDataRunner(impl ModuleDataRunner) {
	moduleDataRunnerImpl = impl
}

// moduleDataRunner returns the registered implementation. ok is false when
// nothing has registered yet, so callers can fail clearly instead of panicking.
func moduleDataRunner() (ModuleDataRunner, bool) {
	return moduleDataRunnerImpl, moduleDataRunnerImpl != nil
}

// ModuleSeededHook finishes work a module's seed files cannot express (for
// example publishing a seeded website to disk). It must be idempotent: install
// re-runs it when a later step failed, and reinit runs it after every replay.
type ModuleSeededHook func(ctx context.Context) error

var (
	moduleSeededHooksMu sync.RWMutex
	moduleSeededHooks   = map[string][]ModuleSeededHook{}
)

// RegisterModuleSeededHook runs fn right after moduleName's seed data is
// imported by InstallModule or ReinitModule, so a module can order "seed rows
// first, then derive runtime artifacts from them" without a second manual step.
func RegisterModuleSeededHook(moduleName string, fn ModuleSeededHook) {
	moduleName = strings.TrimSpace(moduleName)
	if moduleName == "" || fn == nil {
		return
	}
	moduleSeededHooksMu.Lock()
	defer moduleSeededHooksMu.Unlock()
	moduleSeededHooks[moduleName] = append(moduleSeededHooks[moduleName], fn)
}

// runModuleSeededHooks runs the hooks registered for moduleName in order and
// stops at the first error.
func runModuleSeededHooks(ctx context.Context, moduleName string) error {
	moduleSeededHooksMu.RLock()
	hooks := append([]ModuleSeededHook(nil), moduleSeededHooks[strings.TrimSpace(moduleName)]...)
	moduleSeededHooksMu.RUnlock()
	for _, hook := range hooks {
		if err := hook(ctx); err != nil {
			return err
		}
	}
	return nil
}
