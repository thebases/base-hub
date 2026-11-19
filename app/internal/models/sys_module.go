package models

// File overview:
// - Responsibility: defines built-in runtime model metadata and server-side behavior for sys module inside the kernel model catalog.
// - Key symbols: `init`, `createSysModule`, `SysModuleRecord`.
// - Pattern: Leans on metadata-driven registration so storage mapping, runtime methods, and behavior hooks stay centralized instead of being scattered across handlers.
// - Pattern: Threads `context.Context` through the main execution paths so cancellation, auth scope, and transaction state can follow the work end to end.

import (
	"context"
	"fmt"
	"strings"

	kernelmodules "git.thebase.vn/base_hub/internal/modules"
	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/queries"
	"git.thebase.vn/base_hub/internal/services/log"
	"github.com/uptrace/bun"
)

// SysModuleRecord stores one module manifest snapshot so admin tooling can
// browse, relate, and eventually manage module lifecycle metadata.
type SysModuleRecord struct {
	bun.BaseModel `bun:"table:sys_module_module,alias:sys_module_module"`
	orm.MetadataBaseModel
	Name             string         `bun:"name" json:"name" meta:"index,unique,label:Name,help:Stores the technical module name for this record."`
	Version          string         `bun:"version" json:"version" meta:"label:Version,help:Stores the module version value for this record."`
	Category         string         `bun:"category" json:"category" meta:"index,label:Category,help:Stores the module category label for this record."`
	CategoryID       *int64         `bun:"category_id" json:"category_id" meta:"kind:many2one,model:sys.module.category,ondelete:SET NULL,index,label:Category ID,help:Links this record to the related sys.module.category record for category id."`
	Installable      bool           `bun:"installable" json:"installable" meta:"label:Installable,help:Controls whether this module can be installed on a tenant database."`
	AutoInstall      bool           `bun:"auto_install" json:"auto_install" meta:"label:Auto Install,help:When true, this module installs itself automatically once every module in its depends list is installed."`
	ManualUninstall  bool           `bun:"manual_uninstall,notnull,default:false" json:"manual_uninstall" meta:"label:Manual Uninstall,help:Set when an admin explicitly uninstalled this module via the row action; blocks automatic reconciliation (e.g. optional enable-only modules) from forcing it back to installed."`
	ManualInstall    bool           `bun:"manual_install,notnull,default:false" json:"manual_install" meta:"label:Manual Install,help:Set when an admin explicitly installed this module via the row action; blocks the service-database module-selection reconciliation from auto-uninstalling it on the next boot just because it is absent from the default/base module selection."`
	State            string         `bun:"state" json:"state" meta:"index,label:State,selection:installed|Installed;not_installed|Not Installed;to_install|To Install;to_upgrade|To Upgrade;to_remove|To Remove,help:Tracks the module install lifecycle state for the active database."`
	FrontendWidgets  map[string]any `bun:"frontend_widgets,type:jsonb,nullzero" json:"frontend_widgets" meta:"kind:json,label:Frontend Widgets,help:Stores the frontend widget asset metadata declared by the module manifest."`
	StaticJSAssets   []string       `bun:"static_js_assets,type:jsonb,nullzero" json:"static_js_assets" meta:"kind:json,label:Static JS Assets,help:Lists JavaScript assets exposed by the module static bundle."`
	StaticCSSAssets  []string       `bun:"static_css_assets,type:jsonb,nullzero" json:"static_css_assets" meta:"kind:json,label:Static CSS Assets,help:Lists stylesheet assets exposed by the module static bundle."`
	StaticHTMLAssets []string       `bun:"static_html_assets,type:jsonb,nullzero" json:"static_html_assets" meta:"kind:json,label:Static HTML Assets,help:Lists HTML template assets exposed by the module static bundle."`
}

const SysModuleModelName = "sys.module.module"

// rejectIfProtectedModuleName returns an error when name is "base" or any
// "base-"-prefixed module so unlink/disable/write callers get one consistent
// message. Mirrors kernelmodules.IsCoreModuleName, which enforces the same
// rule at the UninstallModule layer itself so no call path (row action,
// direct RPC, or service-database module-selection deselection) can bypass it.
func rejectIfProtectedModuleName(name string) error {
	if kernelmodules.IsCoreModuleName(name) {
		return fmt.Errorf("module %q cannot be disabled or uninstalled", name)
	}
	return nil
}

// SysModuleModel registers no frontend bid constraint: sys.module.module has no bid field (T86 lint), so it never fired.
var SysModuleModel = orm.Define[SysModuleRecord](&Models, 13, orm.ModelSpec{
	Name:        SysModuleModelName,
	Label:       "Modules",
	Description: "Installed module metadata and manifest attributes exposed through the runtime catalog",
	RuntimeMethods: []orm.RuntimeMethod{
		{Name: "create", Public: true, ModelOnly: true, Call: createSysModule},
		{Name: "write", Public: true, UsesPathID: true, Call: writeSysModule},
		{Name: "unlink", Public: true, UsesPathID: true, Call: unlinkSysModule},
		{Name: "action_scan", Public: true, ModelOnly: true, Call: scanSysModules},
		{Name: "action_install", Public: true, UsesPathID: true, Call: installSysModule},
		{Name: "action_uninstall", Public: true, UsesPathID: true, Call: uninstallSysModule},
		{Name: "action_reinit", Public: true, UsesPathID: true, Call: reinitSysModule},
		{Name: "action_reinit_status", Public: true, ModelOnly: true, Call: reinitStatusSysModule},
	},
})

// createSysModule creates the sys module handled by this package.
func createSysModule(ctx context.Context, _ *orm.ModelMetadata, _ []string, args []any, kwargs map[string]any) (any, error) {
	return createLoggedMetadataRecords(ctx, SysModuleModelName, args, kwargs)
}

// A write that would move a record out of "installed" is rejected up front
// for protected modules (SysModule.State), the same as unlink below.
func writeSysModule(ctx context.Context, model *orm.ModelMetadata, ids []string, args []any, kwargs map[string]any) (any, error) {
	if writeTargetsModuleUninstall(args, kwargs) {
		if err := rejectProtectedModuleIDs(ctx, model, ids); err != nil {
			return nil, err
		}
	}
	return writeLoggedMetadataRecords(ctx, SysModuleModelName, ids, args, kwargs)
}

func unlinkSysModule(ctx context.Context, model *orm.ModelMetadata, ids []string, _ []any, _ map[string]any) (any, error) {
	if err := rejectProtectedModuleIDs(ctx, model, ids); err != nil {
		return nil, err
	}
	return unlinkLoggedMetadataRecords(ctx, SysModuleModelName, ids)
}

// scanSysModules rediscovers module manifests from disk/embedded assets and
// syncs the catalog on demand, reporting which manifest names were not
// already known so an admin can see what's newly available to install.
func scanSysModules(ctx context.Context, _ *orm.ModelMetadata, _ []string, _ []any, _ map[string]any) (any, error) {
	newNames, total, err := kernelmodules.ScanAndSyncCatalog(ctx)
	if err != nil {
		return nil, err
	}
	return map[string]any{"scanned": total, "new_modules": newNames}, nil
}

// installSysModule imports the seed data for each targeted module (and any
// not-yet-installed dependency) and flips state/enabled for everything it
// actually installed.
func installSysModule(ctx context.Context, model *orm.ModelMetadata, ids []string, _ []any, _ map[string]any) (any, error) {
	names, err := sysModuleNamesForIDs(ctx, model, ids)
	if err != nil {
		return nil, err
	}

	installedAll := make([]string, 0, len(names))
	for _, name := range names {
		installedNow, err := kernelmodules.InstallModule(ctx, name)
		if err != nil {
			return nil, err
		}
		installedAll = append(installedAll, installedNow...)
	}
	if len(installedAll) > 0 {
		if err := sysModuleSetState(ctx, installedAll, "installed"); err != nil {
			return nil, err
		}
		// Now that the requested modules are persisted as installed, cascade
		// any auto_install dependent whose Depends just became satisfied (e.g.
		// project-helpdesk once both project and helpdesk are installed).
		// Best-effort, same as SyncCatalog: the requested install already
		// committed, so a broken cascade is logged rather than reported as a
		// failed install; the next Scan/boot retries it.
		cascaded, err := kernelmodules.CascadeAutoInstall(ctx)
		if err != nil {
			log.Errorf("sys.module.module: auto_install cascade after install: %v", err)
		}
		installedAll = append(installedAll, cascaded...)
		// Installed modules can seed menus/views/data the frontend renders —
		// bump version.json and push the live event so an already-open tab
		// refreshes instead of serving stale cached data until its next load.
		kernelmodules.BumpFrontendVersion(ctx)
		// Also push the lighter-weight menu-only event so an already-open
		// sidebar can soft-refresh immediately, without waiting on the
		// version-bump path's own (heavier) reload. Best-effort: a stale
		// sidebar for a few seconds until the version bump lands is not worth
		// failing the install over.
		if err := publishUIMenuUpdated(ctx); err != nil {
			log.Warnf("sys.module.module: publish ui.menu.updated after install: %v", err)
		}
	}
	return map[string]any{"installed_modules": installedAll}, nil
}

// uninstallSysModule deletes every row a module seeded and marks it
// not_installed. Protected modules and modules still required by another
// installed module are rejected before anything is deleted. UninstallModule
// can cascade into auto_install dependents (e.g. project-helpdesk when
// project is uninstalled), so the affected set flipped to not_installed
// below is whatever it actually touched, not just the requested ids.
func uninstallSysModule(ctx context.Context, model *orm.ModelMetadata, ids []string, _ []any, _ map[string]any) (any, error) {
	if err := rejectProtectedModuleIDs(ctx, model, ids); err != nil {
		return nil, err
	}
	names, err := sysModuleNamesForIDs(ctx, model, ids)
	if err != nil {
		return nil, err
	}

	deletedByModule := make(map[string]map[string]int, len(names))
	affected := make([]string, 0, len(names))
	for _, name := range names {
		deleted, err := kernelmodules.UninstallModule(ctx, name)
		if err != nil {
			return nil, err
		}
		for moduleName, counts := range deleted {
			if _, already := deletedByModule[moduleName]; !already {
				affected = append(affected, moduleName)
			}
			deletedByModule[moduleName] = counts
		}
	}
	if len(affected) > 0 {
		if err := sysModuleSetState(ctx, affected, "not_installed"); err != nil {
			return nil, err
		}
		// Uninstalling deletes menus/views/data the frontend renders — bump
		// version.json and push the live event so an already-open tab
		// refreshes instead of continuing to serve now-deleted data from its
		// service worker's offline cache.
		kernelmodules.BumpFrontendVersion(ctx)
		// See installSysModule: also push the menu-only event for an
		// immediate sidebar soft-refresh ahead of the version-bump reload.
		if err := publishUIMenuUpdated(ctx); err != nil {
			log.Warnf("sys.module.module: publish ui.menu.updated after uninstall: %v", err)
		}
	}
	return map[string]any{"deleted": deletedByModule}, nil
}

// reinitSysModule force-replays each targeted module's data/security/views
// roots, overwriting existing seed rows back to their file-defined values.
// No state change: the module stays installed throughout.
//
// Optional kwarg request_id: the outcome is also published as
// sys.module.reinit_succeeded/failed and exposed via action_reinit_status, so a
// client that lost its connection mid-reinit can still learn when it finished.
func reinitSysModule(ctx context.Context, model *orm.ModelMetadata, ids []string, _ []any, kwargs map[string]any) (result any, err error) {
	requestID, _ := kwargs["request_id"].(string)
	requestID = strings.TrimSpace(requestID)
	setReinitStatus(requestID, "running", "")
	var names []string
	defer func() {
		if requestID != "" {
			publishModuleReinitEvent(ctx, requestID, names, err)
		}
	}()

	names, err = sysModuleNamesForIDs(ctx, model, ids)
	if err != nil {
		return nil, err
	}

	reports := make(map[string]map[string]int, len(names))
	for _, name := range names {
		created, updated, skipped, err := kernelmodules.ReinitModule(ctx, name)
		if err != nil {
			return nil, err
		}
		reports[name] = map[string]int{"created": created, "updated": updated, "skipped": skipped}
	}
	if len(names) > 0 {
		// Reinit force-replays a module's data root, which can rewrite its
		// seeded ui.menu rows back to file-defined values — push the same
		// menu-only refresh event as install/uninstall so an open sidebar
		// picks up the replayed menus without a manual reload.
		if err := publishUIMenuUpdated(ctx); err != nil {
			log.Warnf("sys.module.module: publish ui.menu.updated after reinit: %v", err)
		}
	}
	return map[string]any{"reinit": reports}, nil
}

// sysModuleNamesForIDs reads the technical name of each targeted row, the
// same lookup rejectProtectedModuleIDs already performs, so lifecycle actions
// can work off module names instead of row ids.
func sysModuleNamesForIDs(ctx context.Context, model *orm.ModelMetadata, ids []string) ([]string, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(SysModuleModelName, model), ids, []string{"name"})
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(rows))
	for _, row := range rows {
		name := strings.TrimSpace(fmt.Sprint(row["name"]))
		if name != "" {
			names = append(names, name)
		}
	}
	return names, nil
}

// sysModuleSetState updates the lifecycle state for the given module names in
// one statement, reusing the pgx pool the catalog sync path already resolves
// the same way (context-scoped, falling back to the process default pool).
func sysModuleSetState(ctx context.Context, names []string, state string) error {
	pool := orm.PGXPoolFromContext(ctx)
	if pool == nil {
		pool = orm.GetPGXPool()
	}
	// manualUninstall mirrors the target state: true only when this call is
	// moving the module to not_installed, so an explicit admin action sticks
	// through the next automatic reconciliation pass (see SetModuleState).
	return queries.SetModuleState(ctx, pool, names, state, state != "installed")
}

// writeTargetsModuleUninstall reports whether a write payload would move a
// module away from the installed state, so a benign field edit (e.g.
// category) on a protected module is not blocked.
func writeTargetsModuleUninstall(args []any, kwargs map[string]any) bool {
	values, _ := sysModuleWriteValues(args, kwargs)
	if values == nil {
		return false
	}
	if state, ok := values["state"].(string); ok && strings.TrimSpace(state) != "installed" {
		return true
	}
	return false
}

// sysModuleWriteValues mirrors the positional/kwargs value extraction the
// generic metadata write path already performs, scoped to this guard so it
// never needs to depend on that path's internals.
func sysModuleWriteValues(args []any, kwargs map[string]any) (map[string]any, bool) {
	if len(args) > 0 {
		if values, ok := args[0].(map[string]any); ok {
			return values, true
		}
	}
	if values, ok := kwargs["values"].(map[string]any); ok {
		return values, true
	}
	return nil, false
}

// rejectProtectedModuleIDs reads the target rows' technical names and fails
// the call if any of them is protected, so base and base-automation can
// never be disabled or uninstalled through the runtime RPC surface.
func rejectProtectedModuleIDs(ctx context.Context, model *orm.ModelMetadata, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	rows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(SysModuleModelName, model), ids, []string{"name"})
	if err != nil {
		return err
	}
	for _, row := range rows {
		name, _ := row["name"].(string)
		if err := rejectIfProtectedModuleName(name); err != nil {
			return err
		}
	}
	return nil
}
