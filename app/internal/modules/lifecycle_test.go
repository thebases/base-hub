package modules

// File overview:
// - Responsibility: locks down UninstallModule's dependent-cascade ordering
//   and refusal rules, independent of any live Postgres/master-data runner.
// - Key symbols: TestUninstallCascadeOrder*.

import "testing"

// TestUninstallCascadeOrderCascadesAutoInstallDependent mirrors the
// project-helpdesk bridge-module pattern: uninstalling one of its Depends
// must cascade project-helpdesk out too, dependent-before-dependency.
func TestUninstallCascadeOrderCascadesAutoInstallDependent(t *testing.T) {
	manifests := []Manifest{
		{Name: "project"},
		{Name: "helpdesk"},
		{Name: "project-helpdesk", AutoInstall: true, Depends: []string{"project", "helpdesk"}},
	}
	installed := map[string]struct{}{"project": {}, "helpdesk": {}, "project-helpdesk": {}}

	order, err := uninstallCascadeOrder("project", manifests, installed)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(order) != 2 || order[0] != "project-helpdesk" || order[1] != "project" {
		t.Fatalf("expected [project-helpdesk project], got %v", order)
	}
}

// TestUninstallCascadeOrderRefusesPlainDependent ensures a dependent that is
// NOT auto_install (something an admin chose to install for its own sake)
// still blocks the uninstall instead of being silently removed.
func TestUninstallCascadeOrderRefusesPlainDependent(t *testing.T) {
	manifests := []Manifest{
		{Name: "project"},
		{Name: "custom-reports", AutoInstall: false, Depends: []string{"project"}},
	}
	installed := map[string]struct{}{"project": {}, "custom-reports": {}}

	if _, err := uninstallCascadeOrder("project", manifests, installed); err == nil {
		t.Fatal("expected an error refusing to uninstall project while custom-reports depends on it")
	}
}

// TestUninstallCascadeOrderRefusesCoreDependent ensures a core (base/base-*)
// dependent can never be swept up in a cascade, even if it declared
// auto_install — core modules can never be uninstalled through any path.
func TestUninstallCascadeOrderRefusesCoreDependent(t *testing.T) {
	manifests := []Manifest{
		{Name: "widget"},
		{Name: "base-widget-ext", AutoInstall: true, Depends: []string{"widget"}},
	}
	installed := map[string]struct{}{"widget": {}, "base-widget-ext": {}}

	if _, err := uninstallCascadeOrder("widget", manifests, installed); err == nil {
		t.Fatal("expected an error refusing to cascade into a core-named dependent")
	}
}

// TestUninstallCascadeOrderMultiLevelChain checks a transitive chain
// (grandchild auto-depends on child auto-depends on the removed module) is
// fully swept, ordered deepest-dependent-first.
func TestUninstallCascadeOrderMultiLevelChain(t *testing.T) {
	manifests := []Manifest{
		{Name: "core-feature"},
		{Name: "bridge-a", AutoInstall: true, Depends: []string{"core-feature"}},
		{Name: "bridge-b", AutoInstall: true, Depends: []string{"bridge-a"}},
	}
	installed := map[string]struct{}{"core-feature": {}, "bridge-a": {}, "bridge-b": {}}

	order, err := uninstallCascadeOrder("core-feature", manifests, installed)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(order) != 3 || order[0] != "bridge-b" || order[1] != "bridge-a" || order[2] != "core-feature" {
		t.Fatalf("expected [bridge-b bridge-a core-feature], got %v", order)
	}
}

// TestUninstallCascadeOrderNoDependents is the base case: uninstalling a
// module nothing depends on is just itself.
func TestUninstallCascadeOrderNoDependents(t *testing.T) {
	manifests := []Manifest{{Name: "standalone"}}
	installed := map[string]struct{}{"standalone": {}}

	order, err := uninstallCascadeOrder("standalone", manifests, installed)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(order) != 1 || order[0] != "standalone" {
		t.Fatalf("expected [standalone], got %v", order)
	}
}
