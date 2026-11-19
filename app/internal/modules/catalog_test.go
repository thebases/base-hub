package modules

// File overview:
// - Responsibility: verifies module discovery, catalog sync, and bootstrap registration for catalog.
// - Key symbols: focused Test cases plus file-local fixtures and assertion helpers.
// - Pattern: Locks down module bootstrap behavior so discovery order, manifest parsing, and catalog persistence stay deterministic.

import (
	"testing"

	"git.thebase.vn/base_hub/internal/orm"
	testutil "git.thebase.vn/base_hub/internal/test"
)

// TestNextAutoInstallCandidateAllowsNonBaseBridgeModule locks in the
// project-helpdesk bridge-module pattern: a non-"base"/"base-" manifest that
// declares auto_install: true must still cascade in once every module in its
// own Depends is installed. Regression guard for the Sep 2026 hardening
// commit that (over-broadly) restricted this cascade to IsCoreModuleName.
func TestNextAutoInstallCandidateAllowsNonBaseBridgeModule(t *testing.T) {
	manifestByName := map[string]Manifest{
		"project": {Name: "project", AutoInstall: false},
		"helpdesk": {Name: "helpdesk", AutoInstall: false},
		"project-helpdesk": {
			Name:        "project-helpdesk",
			AutoInstall: true,
			Depends:     []string{"project", "helpdesk"},
		},
	}

	notYetSatisfied := map[string]struct{}{"project": {}}
	if got := nextAutoInstallCandidate(manifestByName, notYetSatisfied); got != "" {
		t.Fatalf("expected no candidate while helpdesk is not installed, got %q", got)
	}

	satisfied := map[string]struct{}{"project": {}, "helpdesk": {}}
	if got := nextAutoInstallCandidate(manifestByName, satisfied); got != "project-helpdesk" {
		t.Fatalf("expected project-helpdesk to be the auto-install candidate once its deps are installed, got %q", got)
	}
}

func TestSyncCatalogRepairsMissingNameConflictTarget(t *testing.T) {
	bunDB, err := orm.NewBunDB(testutil.EnvPostgresConfig())
	if err != nil {
		t.Skipf("open postgres fixture: %v", err)
	}
	defer bunDB.Close()
	testutil.SwapBunDB(t, bunDB)

	db := bunDB.DB
	ctx := t.Context()

	statements := []string{
		`DROP TABLE IF EXISTS sys_module_module`,
		`CREATE TABLE sys_module_module (
			id BIGSERIAL,
			name TEXT,
			version TEXT,
			category TEXT,
			installable BOOLEAN,
			auto_install BOOLEAN,
			state TEXT,
			frontend_widgets JSONB,
			static_js_assets JSONB,
			static_css_assets JSONB,
			static_html_assets JSONB
		)`,
		`INSERT INTO sys_module_module
			(name, version, category, installable, auto_install, state, frontend_widgets, static_js_assets, static_css_assets, static_html_assets)
		 VALUES
		 	('base', '0.9.0', 'Legacy', true, false, 'installed', '{}'::jsonb, '[]'::jsonb, '[]'::jsonb, '[]'::jsonb)`,
	}
	for _, statement := range statements {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("prepare sys_module_module fixture: %v", err)
		}
	}
	t.Cleanup(func() {
		_, _ = db.ExecContext(ctx, `DROP TABLE IF EXISTS sys_module_module`)
	})

	manifests := []Manifest{{
		Name:        "base",
		Version:     "1.0.0",
		Category:    "Core",
		Installable: true,
		AutoInstall: false,
		FrontendWidgets: map[string]FrontendWidget{
			"status": {JS: "/modules/base/status.js"},
		},
	}}
	if err := SyncCatalog(ctx, manifests); err != nil {
		t.Fatalf("sync catalog: %v", err)
	}

	var (
		version    string
		category   string
		state      string
		rowCount   int
		hasNameKey bool
	)
	if err := db.QueryRowContext(ctx, `
		SELECT version, category, state
		FROM sys_module_module
		WHERE name = 'base'
	`).Scan(&version, &category, &state); err != nil {
		t.Fatalf("read synced module row: %v", err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sys_module_module WHERE name = 'base'`).Scan(&rowCount); err != nil {
		t.Fatalf("count synced module rows: %v", err)
	}
	if err := db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM pg_class tbl
			JOIN pg_namespace ns ON ns.oid = tbl.relnamespace
			JOIN pg_index idx ON idx.indrelid = tbl.oid
			JOIN unnest(idx.indkey) WITH ORDINALITY AS keycols(attnum, ordinality) ON keycols.attnum > 0
			JOIN pg_attribute attr ON attr.attrelid = tbl.oid AND attr.attnum = keycols.attnum
			WHERE ns.nspname = current_schema()
			  AND tbl.relname = 'sys_module_module'
			  AND idx.indisunique
			GROUP BY idx.indexrelid
			HAVING string_agg(attr.attname, ',' ORDER BY keycols.ordinality) = 'name'
		)
	`).Scan(&hasNameKey); err != nil {
		t.Fatalf("check sys_module_module name key: %v", err)
	}

	if version != "1.0.0" {
		t.Fatalf("expected version to be updated, got %q", version)
	}
	if category != "Core" {
		t.Fatalf("expected category to be updated, got %q", category)
	}
	if state != "installed" {
		t.Fatalf("expected state=installed to be preserved during sync, got %q", state)
	}
	if rowCount != 1 {
		t.Fatalf("expected exactly one base module row after sync, got %d", rowCount)
	}
	if !hasNameKey {
		t.Fatalf("expected sync to create a unique name key for sys_module_module")
	}
}
