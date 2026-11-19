package modules

// File overview:
// - Responsibility: resolves per-database module selections against discovered manifests and keeps dependency closure deterministic.
// - Key symbols: `ResolveSelectedManifests`, `FilterImportRootsByModuleNames`.
// - Pattern: Expands explicit selections through manifest dependencies once so provisioning code can reuse one stable module set for catalog sync and seed import.

import (
	"fmt"
	"path/filepath"
	"strings"

	"git.thebase.vn/base_hub/internal/services/log"
)

var runtimeManifestDiskCandidates = []string{
	DefaultRoot,
	filepath.Join("app", "backend", "modules"),
	filepath.Join("..", "modules"),
	filepath.Join("..", "..", "modules"),
	filepath.Join("..", "..", "..", "modules"),
}

// DiscoverRuntimeManifests resolves module manifests the same way boot-time
// catalog sync does: ./modules when present, otherwise the embedded bundle, and
// only then the parent-directory fallbacks (used by tests/dev runs that start
// outside the app root). A production binary with no ./modules therefore never
// picks up a stale sibling ../modules tree that lacks newer modules.
func DiscoverRuntimeManifests() ([]Manifest, error) {
	manifests, _, err := discoverRuntimeManifestsWithSource()
	return manifests, err
}

// discoverRuntimeManifestsWithSource is DiscoverRuntimeManifests plus a label
// naming the tree the manifests came from, so "unknown module" errors can say
// which tree (disk path or embedded) was actually consulted.
func discoverRuntimeManifestsWithSource() ([]Manifest, string, error) {
	if manifests, err := DiscoverManifestsFromDisk(DefaultRoot); err == nil {
		return manifests, "disk:" + DefaultRoot, nil
	}
	if fsys := EmbeddedFS(); fsys != nil {
		manifests, err := DiscoverManifestsFromFS(fsys, ".")
		return manifests, "embedded", err
	}
	for _, candidate := range runtimeManifestDiskCandidates[1:] {
		manifests, err := DiscoverManifestsFromDisk(candidate)
		if err == nil {
			return manifests, "disk:" + candidate, nil
		}
	}
	return nil, "", fmt.Errorf("modules: no runtime manifests are available")
}

// DiscoverRuntimeImportRoots mirrors DiscoverRuntimeManifests (same source
// order) but for the data/security/views import roots, so module-scoped
// operations (install, reinit) read seed data from the same tree whose
// manifests they resolved.
func DiscoverRuntimeImportRoots() ([]string, error) {
	if roots, err := DiscoverImportRootsFromDisk(DefaultRoot); err == nil {
		return roots, nil
	}
	if fsys := EmbeddedFS(); fsys != nil {
		return DiscoverImportRootsFromFS(fsys, ".")
	}
	for _, candidate := range runtimeManifestDiskCandidates[1:] {
		roots, err := DiscoverImportRootsFromDisk(candidate)
		if err == nil {
			return roots, nil
		}
	}
	return nil, fmt.Errorf("modules: no runtime import roots are available")
}

// ResolveSelectedManifests expands the requested module names through manifest
// dependencies and returns the matching manifests in their original discovery order.
func ResolveSelectedManifests(manifests []Manifest, selectedNames []string) ([]Manifest, error) {
	if len(selectedNames) == 0 {
		return append([]Manifest(nil), manifests...), nil
	}

	manifestByName := make(map[string]Manifest, len(manifests))
	for _, manifest := range manifests {
		name := strings.TrimSpace(manifest.Name)
		if name == "" {
			continue
		}
		manifestByName[name] = manifest
	}

	resolved := make(map[string]struct{}, len(selectedNames))
	for _, selectedName := range selectedNames {
		if err := collectSelectedManifestNames(manifestByName, resolved, strings.TrimSpace(selectedName)); err != nil {
			return nil, err
		}
	}

	selected := make([]Manifest, 0, len(resolved))
	for _, manifest := range manifests {
		if _, ok := resolved[strings.TrimSpace(manifest.Name)]; ok {
			selected = append(selected, manifest)
		}
	}
	return selected, nil
}

// FilterImportRootsByModuleNames keeps only import roots whose owning module is
// in the selected set so service-database seed replay matches the catalog
// state. A module's name is always its on-disk folder name (see
// Manifest.FolderName), so the root's folder segment can be compared to
// moduleNames directly with no manifest lookup involved.
func FilterImportRootsByModuleNames(roots []string, moduleNames []string) []string {
	if len(moduleNames) == 0 {
		return append([]string(nil), roots...)
	}

	allowed := make(map[string]struct{}, len(moduleNames))
	for _, moduleName := range moduleNames {
		moduleName = strings.TrimSpace(moduleName)
		if moduleName == "" {
			continue
		}
		allowed[moduleName] = struct{}{}
	}

	filtered := make([]string, 0, len(roots))
	for _, root := range roots {
		if _, ok := allowed[moduleNameFromImportRoot(root)]; ok {
			filtered = append(filtered, root)
		}
	}
	return filtered
}

func collectSelectedManifestNames(manifestByName map[string]Manifest, resolved map[string]struct{}, name string) error {
	if name == "" {
		return nil
	}
	if _, ok := resolved[name]; ok {
		return nil
	}

	manifest, ok := manifestByName[name]
	if !ok {
		log.Errorf("modules: unknown selected module %s", name)
		return fmt.Errorf("modules: unknown selected module %s", name)
	}
	resolved[name] = struct{}{}

	// Pull declared dependencies into the effective selection so provisioning
	// never creates a tenant whose chosen feature set is missing required seeds.
	for _, dependency := range manifest.Depends {
		if err := collectSelectedManifestNames(manifestByName, resolved, strings.TrimSpace(dependency)); err != nil {
			return err
		}
	}
	return nil
}

func moduleNameFromImportRoot(root string) string {
	cleaned := filepath.ToSlash(filepath.Clean(strings.TrimSpace(root)))
	parts := strings.Split(cleaned, "/")
	if len(parts) < 2 {
		return ""
	}
	return strings.TrimSpace(parts[len(parts)-2])
}
