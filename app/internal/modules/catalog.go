package modules

// File overview:
// - Responsibility: translates discovered module manifests into durable catalog rows and persists them through the query layer.
// - Key symbols: CatalogRecord, BuildCatalogRecords, SyncCatalog.
// - Pattern: Normalizes manifest-derived widget assets once so later startup and UI discovery paths consume one stable catalog representation.

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/queries"
	"git.thebase.vn/base_hub/internal/services/log"
	"github.com/jackc/pgx/v5/pgxpool"
)

// CatalogRecord keeps the parsed manifest data in a shape that matches the
// catalog sync path while still being easy for bootstrap code to assemble.
type CatalogRecord struct {
	Name             string
	Version          string
	Category         string
	Installable      bool
	AutoInstall      bool
	State            string
	FrontendWidgets  map[string]FrontendWidget
	StaticJSAssets   []string
	StaticCSSAssets  []string
	StaticHTMLAssets []string
}

// BuildCatalogRecords turns manifests into catalog rows once so sync code does
// not have to repeat widget asset extraction. A module discovered for the
// first time always lands as "not_installed" here — auto_install modules are
// picked up afterward by ExpandAutoInstall, which knows the real installed
// set and can honor "install once every dependency is installed" instead of
// installing unconditionally. The SQL upsert (queries.SyncModuleCatalog)
// COALESCEs state against the existing row, so an already installed module is
// never reset by a later scan or boot.
func BuildCatalogRecords(manifests []Manifest) []CatalogRecord {
	records := make([]CatalogRecord, 0, len(manifests))
	for _, manifest := range manifests {
		if manifest.Name == "" {
			continue
		}

		jsAssets, cssAssets, htmlAssets := collectWidgetAssets(manifest.FrontendWidgets)
		records = append(records, CatalogRecord{
			Name:             manifest.Name,
			Version:          manifest.Version,
			Category:         manifest.Category,
			Installable:      manifest.Installable,
			AutoInstall:      manifest.AutoInstall,
			State:            "not_installed",
			FrontendWidgets:  manifest.FrontendWidgets,
			StaticJSAssets:   jsAssets,
			StaticCSSAssets:  cssAssets,
			StaticHTMLAssets: htmlAssets,
		})
	}
	return records
}

// collectWidgetAssets deduplicates manifest-declared assets so later sync work
// stays stable even when multiple widgets reuse the same files.
func collectWidgetAssets(widgets map[string]FrontendWidget) ([]string, []string, []string) {
	if len(widgets) == 0 {
		return nil, nil, nil
	}

	js := make([]string, 0, len(widgets))
	css := make([]string, 0, len(widgets))
	html := make([]string, 0, len(widgets))
	seenJS := make(map[string]struct{}, len(widgets))
	seenCSS := make(map[string]struct{}, len(widgets))
	seenHTML := make(map[string]struct{}, len(widgets))

	for _, widget := range widgets {
		if widget.JS != "" {
			if _, ok := seenJS[widget.JS]; !ok {
				seenJS[widget.JS] = struct{}{}
				js = append(js, widget.JS)
			}
		}

		for _, item := range widget.CSS {
			if _, ok := seenCSS[item]; ok {
				continue
			}
			seenCSS[item] = struct{}{}
			css = append(css, item)
		}

		if widget.HTML != "" {
			if _, ok := seenHTML[widget.HTML]; !ok {
				seenHTML[widget.HTML] = struct{}{}
				html = append(html, widget.HTML)
			}
		}
	}

	return js, css, html
}

// SyncCatalog persists the discovered module catalog in one batch so runtime
// startup and later lookups see the same manifest-backed module metadata.
func SyncCatalog(ctx context.Context, manifests []Manifest) error {
	pool := orm.PGXPoolFromContext(ctx)
	if pool == nil {
		pool = orm.GetPGXPool()
	}
	if pool == nil {
		log.Errorf("modules: runtime pgx pool is not initialized")
		return fmt.Errorf("modules: runtime pgx pool is not initialized")
	}

	records := BuildCatalogRecords(manifests)
	if len(records) == 0 {
		return nil
	}

	rows := make([]queries.ModuleCatalogRow, 0, len(records))
	for _, record := range records {
		row, err := buildModuleCatalogRow(record)
		if err != nil {
			return err
		}
		rows = append(rows, row)
	}

	if err := queries.SyncModuleCatalog(ctx, pool, rows); err != nil {
		return err
	}

	// Every catalog row now exists; expand auto_install modules whose full
	// dependency list is already installed. Errors here are logged, not
	// fatal — a broken auto-install chain must not block boot/scan.
	if _, err := ExpandAutoInstall(ctx, pool, manifests); err != nil {
		log.Errorf("modules: expand auto_install after catalog sync: %v", err)
	}
	return nil
}

// ExpandAutoInstall installs every not-yet-installed manifest whose
// AutoInstall is set once all of its Depends are already installed,
// repeating until no further module qualifies (a module unlocked by one pass
// can itself unlock the next). It returns the names it actually installed.
func ExpandAutoInstall(ctx context.Context, pool *pgxpool.Pool, manifests []Manifest) ([]string, error) {
	// InstallModule -> InvalidateMCPCache -> SyncModuleCatalog calls back into this pass while the
	// module just installed is still not_installed in the DB (the caller persists state afterwards),
	// so a nested pass would pick the same candidate again and recurse without end. The outer pass
	// already tracks what it installed, so a nested one is a no-op.
	if ctx.Value(expandAutoInstallActiveKey{}) != nil {
		return nil, nil
	}
	ctx = context.WithValue(ctx, expandAutoInstallActiveKey{}, true)
	manifestByName := make(map[string]Manifest, len(manifests))
	for _, manifest := range manifests {
		if manifest.Name != "" {
			manifestByName[manifest.Name] = manifest
		}
	}

	installed, err := queries.InstalledModuleNames(ctx, pool)
	if err != nil {
		return nil, err
	}

	installedAll := make([]string, 0)
	for {
		unlocked := nextAutoInstallCandidate(manifestByName, installed)
		if unlocked == "" {
			break
		}
		installedNow, err := InstallModule(ctx, unlocked)
		if err != nil {
			return installedAll, fmt.Errorf("modules: auto_install %q: %w", unlocked, err)
		}
		for _, name := range installedNow {
			installed[name] = struct{}{}
		}
		installed[unlocked] = struct{}{}
		installedAll = append(installedAll, installedNow...)
	}

	if len(installedAll) > 0 {
		if err := queries.SetModuleState(ctx, pool, installedAll, "installed", false); err != nil {
			return installedAll, err
		}
	}
	return installedAll, nil
}

// expandAutoInstallActiveKey marks a context already inside an ExpandAutoInstall pass.
type expandAutoInstallActiveKey struct{}

// nextAutoInstallCandidate returns one not-yet-installed, auto_install
// manifest whose Depends are all satisfied by installed, or "" once none
// qualify. Deterministic name order keeps repeated boots/scans stable.
//
// This intentionally does NOT restrict to "base"/"base-" prefixed modules:
// IsCoreModuleName gates a different concern (which modules are pre-selected
// at single-tenant home-DB bootstrap, and which can never be uninstalled —
// see singleTenantHomeModuleSelection and UninstallModule). A module can only
// declare auto_install/Depends in its own manifest.md, which is checked into
// the repo and code-reviewed like any other module code, so cascading any
// manifest's declared auto_install here carries no more risk than installing
// it by hand — e.g. project-helpdesk (depends: [project, helpdesk],
// auto_install: true) relies on this cascade to activate itself the moment
// both of its dependencies are installed, without being a base-* module.
func nextAutoInstallCandidate(manifestByName map[string]Manifest, installed map[string]struct{}) string {
	names := make([]string, 0, len(manifestByName))
	for name := range manifestByName {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		if _, already := installed[name]; already {
			continue
		}
		manifest := manifestByName[name]
		if !manifest.AutoInstall {
			continue
		}
		if allDependsInstalled(manifest.Depends, installed) {
			return name
		}
	}
	return ""
}

// allDependsInstalled reports whether every dependency name is present in
// installed, so nextAutoInstallCandidate can gate on the full closure.
func allDependsInstalled(depends []string, installed map[string]struct{}) bool {
	for _, dependency := range depends {
		if _, ok := installed[dependency]; !ok {
			return false
		}
	}
	return true
}

// ScanAndSyncCatalog re-discovers every module manifest (disk first, embedded
// fallback) and syncs the catalog, the same primitive boot uses, but reports
// which manifest names did not already have a sys_module_module row so a
// manual "Scan" action can tell an admin what's actually new.
func ScanAndSyncCatalog(ctx context.Context) (newNames []string, total int, err error) {
	manifests, err := DiscoverRuntimeManifests()
	if err != nil {
		return nil, 0, err
	}

	pool := orm.PGXPoolFromContext(ctx)
	if pool == nil {
		pool = orm.GetPGXPool()
	}

	candidates := make([]string, 0, len(manifests))
	for _, manifest := range manifests {
		if manifest.Name != "" {
			candidates = append(candidates, manifest.Name)
		}
	}

	existing, err := queries.ExistingModuleNames(ctx, pool, candidates)
	if err != nil {
		return nil, 0, err
	}

	if err := SyncCatalog(ctx, manifests); err != nil {
		return nil, 0, err
	}

	for _, name := range candidates {
		if _, found := existing[name]; !found {
			newNames = append(newNames, name)
		}
	}
	return newNames, len(manifests), nil
}

// InstalledModule delegates to the query layer so callers can ask whether a
// module is installed without needing to know where the catalog is stored.
func InstalledModule(ctx context.Context, name string) (bool, error) {
	return queries.ModuleInstalled(ctx, orm.PGXPoolFromContext(ctx), name)
}

// buildModuleCatalogRow centralizes JSON encoding so each field keeps a precise
// error message instead of making SyncCatalog repeat the same boilerplate.
func buildModuleCatalogRow(record CatalogRecord) (queries.ModuleCatalogRow, error) {
	frontendWidgets, err := marshalCatalogField(record.Name, "frontend widgets", record.FrontendWidgets)
	if err != nil {
		return queries.ModuleCatalogRow{}, err
	}
	staticJS, err := marshalCatalogField(record.Name, "static js assets", record.StaticJSAssets)
	if err != nil {
		return queries.ModuleCatalogRow{}, err
	}
	staticCSS, err := marshalCatalogField(record.Name, "static css assets", record.StaticCSSAssets)
	if err != nil {
		return queries.ModuleCatalogRow{}, err
	}
	staticHTML, err := marshalCatalogField(record.Name, "static html assets", record.StaticHTMLAssets)
	if err != nil {
		return queries.ModuleCatalogRow{}, err
	}

	return queries.ModuleCatalogRow{
		Name:             record.Name,
		Version:          record.Version,
		Category:         record.Category,
		Installable:      record.Installable,
		AutoInstall:      record.AutoInstall,
		State:            record.State,
		FrontendWidgets:  frontendWidgets,
		StaticJSAssets:   staticJS,
		StaticCSSAssets:  staticCSS,
		StaticHTMLAssets: staticHTML,
	}, nil
}

// marshalCatalogField keeps catalog-sync failures tied to the owning module and
// field name so broken manifest data is quick to diagnose.
func marshalCatalogField(moduleName, fieldName string, value any) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		log.Errorf("modules: marshal %s for %s: %v", fieldName, moduleName, err)
		return nil, fmt.Errorf("modules: marshal %s for %s: %w", fieldName, moduleName, err)
	}
	return raw, nil
}
