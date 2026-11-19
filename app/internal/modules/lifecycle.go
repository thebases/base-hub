package modules

// File overview:
// - Responsibility: module-scoped lookups used by the sys.module.module install/uninstall/reinit lifecycle.
// - Key symbols: `ManifestByName`, `DependentsOf`, `ImportRootsForModule`.
// - Pattern: Reuses the existing manifest discovery and import-root filtering primitives instead of introducing a second module-scanning path.

import (
	"context"
	"fmt"
	"io/fs"
	"strings"

	"git.thebase.vn/base_hub/internal/i18n"
	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/queries"
)

// ManifestByName re-discovers the runtime manifest set and returns the one
// matching name, so lifecycle actions always work off the current disk state.
func ManifestByName(name string) (Manifest, bool) {
	name = strings.TrimSpace(name)
	manifests, err := DiscoverRuntimeManifests()
	if err != nil {
		return Manifest{}, false
	}
	for _, manifest := range manifests {
		if strings.TrimSpace(manifest.Name) == name {
			return manifest, true
		}
	}
	return Manifest{}, false
}

// DependentsOf returns the names of manifests that declare name as a
// dependency, restricted to installedNames, so an uninstall can refuse to
// remove a module something else currently installed still requires.
func DependentsOf(name string, manifests []Manifest, installedNames map[string]struct{}) []string {
	name = strings.TrimSpace(name)
	dependents := make([]string, 0)
	for _, manifest := range manifests {
		if strings.TrimSpace(manifest.Name) == name {
			continue
		}
		if _, installed := installedNames[strings.TrimSpace(manifest.Name)]; !installed {
			continue
		}
		for _, dependency := range manifest.Depends {
			if strings.TrimSpace(dependency) == name {
				dependents = append(dependents, manifest.Name)
				break
			}
		}
	}
	return dependents
}

// ImportRootsForModule resolves only the data/security/views roots owned by
// one module, so install/reinit can replay a single module's seed data
// without touching every other module's import roots.
func ImportRootsForModule(name string) ([]string, error) {
	roots, err := DiscoverRuntimeImportRoots()
	if err != nil {
		return nil, err
	}
	return FilterImportRootsByModuleNames(roots, []string{name}), nil
}

// InstallModule imports the seed data for name and every not-yet-installed
// module in its dependency closure, and returns the names that were freshly
// installed (state should be flipped to "installed" for each by the caller).
func InstallModule(ctx context.Context, name string) ([]string, error) {
	name = strings.TrimSpace(name)
	if _, ok := ManifestByName(name); !ok {
		// Say which manifest tree was searched: a catalog row can exist for a
		// module the running binary (or a stale disk tree) does not ship.
		manifests, source, err := discoverRuntimeManifestsWithSource()
		if err != nil {
			return nil, fmt.Errorf("modules: unknown module %q (manifest discovery failed: %v)", name, err)
		}
		return nil, fmt.Errorf("modules: unknown module %q (searched %s, %d modules)", name, source, len(manifests))
	}
	runner, ok := moduleDataRunner()
	if !ok {
		return nil, fmt.Errorf("modules: no master-data runner registered")
	}

	manifests, err := DiscoverRuntimeManifests()
	if err != nil {
		return nil, err
	}
	closure, err := ResolveSelectedManifests(manifests, []string{name})
	if err != nil {
		return nil, err
	}

	pool := orm.PGXPoolFromContext(ctx)
	if pool == nil {
		pool = orm.GetPGXPool()
	}
	installed, err := queries.InstalledModuleNames(ctx, pool)
	if err != nil {
		return nil, err
	}

	if err := ensureInstallSchema(ctx, closure, installed); err != nil {
		return nil, err
	}

	installedNow := make([]string, 0, len(closure))
	for _, manifest := range closure {
		moduleName := strings.TrimSpace(manifest.Name)
		if _, already := installed[moduleName]; already {
			continue
		}
		roots, err := ImportRootsForModule(moduleName)
		if err != nil {
			return installedNow, err
		}
		if _, _, _, err := runner.ImportModuleData(ctx, roots, false); err != nil {
			return installedNow, fmt.Errorf("modules: install %q: %w", moduleName, err)
		}
		if err := runModuleSeededHooks(ctx, moduleName); err != nil {
			return installedNow, fmt.Errorf("modules: install %q: post-seed hook: %w", moduleName, err)
		}
		reloadModuleTranslations(manifest.FolderName)
		installedNow = append(installedNow, moduleName)
	}
	for _, moduleName := range installedNow {
		InvalidateMCPCache(ctx, moduleName)
	}
	return installedNow, nil
}

// ensureInstallSchema brings the request's database schema up to the compiled
// model registry before any seed file is imported. Boot only migrates the
// configured databases (plus discovered service DBs when the database module
// is present), so a dbFilter-matched database can serve requests with a schema
// older than the binary — its first install then failed mid-import with
// `relation "tbl_<model>" does not exist`, leaving earlier files' rows behind.
// The migration is the same idempotent pass boot runs; it is skipped when
// every module in the closure is already installed (nothing will be seeded).
func ensureInstallSchema(ctx context.Context, closure []Manifest, installed map[string]struct{}) error {
	pending := false
	for _, manifest := range closure {
		if _, already := installed[strings.TrimSpace(manifest.Name)]; !already {
			pending = true
			break
		}
	}
	if !pending {
		return nil
	}
	db := orm.BunDBFromContext(ctx)
	if db == nil {
		return fmt.Errorf("modules: install: database is not initialized")
	}
	if err := orm.AutoMigrateRegisteredModelsToDB(ctx, db, orm.DefaultModelRegistry()); err != nil {
		return fmt.Errorf("modules: install: migrate schema: %w", err)
	}
	return nil
}

// CascadeAutoInstall runs the ExpandAutoInstall pass against the live catalog
// so a manual install (sys.module.module action_install) unlocks auto_install
// dependents immediately — e.g. installing project then helpdesk cascades
// project-helpdesk in — instead of waiting for the next boot/Scan
// SyncCatalog, which was previously the only caller of ExpandAutoInstall.
// Callers must flip the manually installed modules to "installed" first,
// since ExpandAutoInstall reads the installed set from the DB.
func CascadeAutoInstall(ctx context.Context) ([]string, error) {
	pool := orm.PGXPoolFromContext(ctx)
	if pool == nil {
		pool = orm.GetPGXPool()
	}
	if pool == nil {
		return nil, fmt.Errorf("modules: runtime pgx pool is not initialized")
	}
	manifests, err := DiscoverRuntimeManifests()
	if err != nil {
		return nil, err
	}
	return ExpandAutoInstall(ctx, pool, manifests)
}

// IsCoreModuleName reports whether name is "base" or "base-"-prefixed — the
// kernel module and every first-party base-* module. These can never be
// uninstalled through any path (row action, direct RPC, or service-database
// module-selection deselection). internal/models/sys_module.go's RPC-layer
// guard calls this too so the two layers agree instead of drifting.
func IsCoreModuleName(name string) bool {
	name = strings.TrimSpace(name)
	return name == "base" || strings.HasPrefix(name, "base-")
}

// UninstallModule removes name and, transitively, every currently-installed
// auto_install dependent that requires it — an auto_install module (e.g.
// project-helpdesk, cascaded in the moment project+helpdesk were both
// installed) exists only because its dependencies are satisfied, so it must
// be cascaded back out the moment one of them is removed, the mirror image
// of ExpandAutoInstall's install-side cascade. A plain (non-auto_install)
// dependent still blocks the whole uninstall via the pre-existing "required
// by" refusal — that one is something an admin chose to install for its own
// sake, so removing it out from under that choice would be silent, unwanted
// data loss. Returns per-module deleted row counts for name plus every
// dependent it took down with it, keyed by module name, so a caller can flip
// install-state for the whole cascade rather than only the module it asked
// to remove.
func UninstallModule(ctx context.Context, name string) (map[string]map[string]int, error) {
	name = strings.TrimSpace(name)
	if IsCoreModuleName(name) {
		return nil, fmt.Errorf("modules: %q is a core module and cannot be uninstalled", name)
	}
	runner, ok := moduleDataRunner()
	if !ok {
		return nil, fmt.Errorf("modules: no master-data runner registered")
	}

	manifests, err := DiscoverRuntimeManifests()
	if err != nil {
		return nil, err
	}
	pool := orm.PGXPoolFromContext(ctx)
	if pool == nil {
		pool = orm.GetPGXPool()
	}
	installed, err := queries.InstalledModuleNames(ctx, pool)
	if err != nil {
		return nil, err
	}

	order, err := uninstallCascadeOrder(name, manifests, installed)
	if err != nil {
		return nil, err
	}

	deleted := make(map[string]map[string]int, len(order))
	for _, moduleName := range order {
		rows, err := runner.DeleteModuleData(ctx, moduleName)
		if err != nil {
			return deleted, fmt.Errorf("modules: uninstall %q: %w", moduleName, err)
		}
		deleted[moduleName] = rows
		InvalidateMCPCache(ctx, moduleName)
	}
	return deleted, nil
}

// uninstallCascadeOrder returns name plus every currently-installed
// auto_install module that transitively depends on it, ordered
// dependents-first (a dependent's seeded rows must be gone before the
// dependency it relies on is removed, mirroring InstallModule's own
// dependency-closure ordering). Walking DependentsOf again from each
// cascaded-in dependent (not just from name) is what makes a multi-level
// chain (A auto-depends-on B auto-depends-on name) cascade all the way
// through, not just one level. Refuses the whole operation — no partial
// delete — the moment it meets a dependent that must never be removed this
// way: a core module (IsCoreModuleName, which can't be uninstalled through
// any path) or a plain non-auto_install dependent (an admin's own install
// choice).
func uninstallCascadeOrder(name string, manifests []Manifest, installed map[string]struct{}) ([]string, error) {
	manifestByName := make(map[string]Manifest, len(manifests))
	for _, manifest := range manifests {
		manifestByName[strings.TrimSpace(manifest.Name)] = manifest
	}

	visited := make(map[string]struct{})
	order := make([]string, 0, len(manifests))

	var visit func(target string) error
	visit = func(target string) error {
		if _, done := visited[target]; done {
			return nil
		}
		visited[target] = struct{}{}

		for _, dependent := range DependentsOf(target, manifests, installed) {
			if IsCoreModuleName(dependent) {
				return fmt.Errorf("modules: %q is required by core module %q, which can never be uninstalled", target, dependent)
			}
			if manifest, ok := manifestByName[dependent]; !ok || !manifest.AutoInstall {
				return fmt.Errorf("modules: module %q is required by %s", target, dependent)
			}
			if err := visit(dependent); err != nil {
				return err
			}
		}
		order = append(order, target)
		return nil
	}

	if err := visit(name); err != nil {
		return nil, err
	}
	return order, nil
}

// ReinitModule force-replays a single module's data/security/views roots
// (master data, menu records, and view arches all live under those three
// directories), overwriting existing seed rows back to their file-defined
// values, then reloads the module's i18n translations so a Reinit is a
// complete "reload everything this module ships" operation rather than only
// touching database-backed rows.
func ReinitModule(ctx context.Context, name string) (created, updated, skipped int, err error) {
	name = strings.TrimSpace(name)
	runner, ok := moduleDataRunner()
	if !ok {
		return 0, 0, 0, fmt.Errorf("modules: no master-data runner registered")
	}
	roots, err := ImportRootsForModule(name)
	if err != nil {
		return 0, 0, 0, err
	}
	created, updated, skipped, err = runner.ImportModuleData(ctx, roots, true)
	if err != nil {
		return created, updated, skipped, err
	}
	if err := runModuleSeededHooks(ctx, name); err != nil {
		return created, updated, skipped, fmt.Errorf("modules: reinit %q: post-seed hook: %w", name, err)
	}

	if manifest, ok := ManifestByName(name); ok {
		reloadModuleTranslations(manifest.FolderName)
	}
	InvalidateMCPCache(ctx, name)
	return created, updated, skipped, nil
}

// reloadModuleTranslations re-parses one module's i18n/*.po and *.pot files
// into the global translation registry (internal/i18n.LoadFromFS walks any
// fs.FS looking for "*/i18n/*.po(t)", so scoping it to the module's own
// subtree via fs.Sub reloads just that module instead of every module's
// translations). Translation files aren't sys_model_data-cataloged rows —
// they're a process-wide in-memory registry loaded once at boot
// (modules/base/base.go's "base.runtime.i18n" bootstrap step) — so install
// and reinit must refresh it explicitly or edited/newly-installed
// translations would never take effect without a full process restart.
// Errors are swallowed exactly like the boot-time loader: a missing or
// malformed i18n file must never fail install/reinit.
func reloadModuleTranslations(folderName string) {
	folderName = strings.TrimSpace(folderName)
	if folderName == "" {
		return
	}
	fsys := ModuleTreeFS()
	if fsys == nil {
		return
	}
	sub, err := fs.Sub(fsys, folderName)
	if err != nil {
		return
	}
	_ = i18n.LoadFromFS(sub)
}
