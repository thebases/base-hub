package modules

// File overview:
// - Responsibility: resolves module-owned resource files across disk and embedded assets so bootstrap, tests, and packaged binaries share one lookup path.
// - Key symbols: SetEmbeddedFS, ReadModuleResource, cleanModuleResourcePath.
// - Pattern: Prefers developer-local disk files first, then falls back to embedded assets so overrides stay easy in development without breaking production bundles.

import (
	"fmt"
	"git.thebase.vn/base_hub/internal/services/log"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

var (
	embeddedModulesFSMu sync.RWMutex
	embeddedModulesFS   fs.FS
)

// SetEmbeddedFS swaps the embedded module filesystem used as the fallback when
// local disk assets are unavailable, which keeps tests and production boot
// paths using the same lookup API.
func SetEmbeddedFS(fsys fs.FS) {
	embeddedModulesFSMu.Lock()
	defer embeddedModulesFSMu.Unlock()
	embeddedModulesFS = fsys
}

// EmbeddedFS returns the current embedded module filesystem, or nil when none
// has been set yet. Callers that need to walk all module assets (e.g. the
// i18n translation loader) use this to access the bundled file tree.
func EmbeddedFS() fs.FS {
	embeddedModulesFSMu.RLock()
	defer embeddedModulesFSMu.RUnlock()
	return embeddedModulesFS
}

// ModuleTreeFS returns the module tree for loaders that walk every module
// (i18n). It is the embedded bundle when one is registered, else the module
// directory on disk: `go run ./main.go` skips main_embed.go, so dev runs have
// no embedded bundle and would otherwise load nothing. Nil when neither exists.
func ModuleTreeFS() fs.FS {
	if fsys := EmbeddedFS(); fsys != nil {
		return fsys
	}
	for _, dir := range moduleDiskCandidates(".") {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return os.DirFS(dir)
		}
	}
	return nil
}

// ReadModuleResource prefers disk files so local development can override
// bundled assets, then falls back to embedded files for built binaries.
func ReadModuleResource(resourcePath string) ([]byte, error) {
	cleaned, err := cleanModuleResourcePath(resourcePath)
	if err != nil {
		return nil, err
	}

	for _, candidate := range moduleDiskCandidates(cleaned) {
		raw, readErr := os.ReadFile(candidate)
		if readErr == nil {
			return raw, nil
		}
		if !os.IsNotExist(readErr) {
			return nil, readErr
		}
	}

	embeddedModulesFSMu.RLock()
	fsys := embeddedModulesFS
	embeddedModulesFSMu.RUnlock()
	if fsys == nil {
		return nil, fs.ErrNotExist
	}

	raw, readErr := fs.ReadFile(fsys, filepath.ToSlash(cleaned))
	if readErr == nil {
		return raw, nil
	}
	if !isNotExist(readErr) {
		return nil, readErr
	}

	return nil, fs.ErrNotExist
}

// ListModuleResourceDir returns the file names (directories excluded, not
// recursive) inside a module-owned resource directory, checked on disk first
// then in the embedded module tree -- so callers can auto-discover files a
// module ships (e.g. static assets to attach to generated output) without
// hardcoding names that would go stale as files are added or removed.
func ListModuleResourceDir(resourcePath string) ([]string, error) {
	cleaned, err := cleanModuleResourcePath(resourcePath)
	if err != nil {
		return nil, err
	}

	for _, candidate := range moduleDiskCandidates(cleaned) {
		entries, readErr := os.ReadDir(candidate)
		if readErr == nil {
			return fileNamesFromDirEntries(entries), nil
		}
		if !os.IsNotExist(readErr) {
			return nil, readErr
		}
	}

	embeddedModulesFSMu.RLock()
	fsys := embeddedModulesFS
	embeddedModulesFSMu.RUnlock()
	if fsys == nil {
		return nil, fs.ErrNotExist
	}

	entries, readErr := fs.ReadDir(fsys, filepath.ToSlash(cleaned))
	if readErr == nil {
		return fileNamesFromDirEntries(entries), nil
	}
	if !isNotExist(readErr) {
		return nil, readErr
	}

	return nil, fs.ErrNotExist
}

func fileNamesFromDirEntries(entries []fs.DirEntry) []string {
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	return names
}

// cleanModuleResourcePath rejects empty and traversal-style paths so callers
// can only resolve files inside a module-owned subtree.
func cleanModuleResourcePath(resourcePath string) (string, error) {
	cleaned := filepath.ToSlash(strings.TrimSpace(resourcePath))
	cleaned = strings.TrimPrefix(cleaned, "/")
	cleaned = strings.TrimPrefix(cleaned, "./")
	if cleaned == "" {
		log.Errorf("modules: resource path is required")
		return "", fmt.Errorf("modules: resource path is required")
	}

	parts := strings.Split(cleaned, "/")
	if len(parts) < 2 {
		log.Errorf("modules: resource path %q must include the module name", resourcePath)
		return "", fmt.Errorf("modules: resource path %q must include the module name", resourcePath)
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			log.Errorf("modules: invalid resource path %q", resourcePath)
			return "", fmt.Errorf("modules: invalid resource path %q", resourcePath)
		}
	}

	return cleaned, nil
}

// moduleDiskCandidates keeps the search order explicit so the primary module
// roots remain predictable across the backend entrypoints we support today.
func moduleDiskCandidates(cleaned string) []string {
	candidates := []string{
		filepath.Join(DefaultRoot, filepath.Clean(cleaned)),
		filepath.Join("app", "backend", "modules", filepath.Clean(cleaned)),
		filepath.Join("..", "modules", filepath.Clean(cleaned)),
	}

	seen := make(map[string]struct{}, len(candidates))
	out := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		candidate = filepath.Clean(candidate)
		if _, exists := seen[candidate]; exists {
			continue
		}
		seen[candidate] = struct{}{}
		out = append(out, candidate)
	}

	return out
}

// isNotExist smooths over `os` and `io/fs` error variants so resource lookup
// can treat both disk and embedded misses the same way.
func isNotExist(err error) bool {
	return err != nil && (os.IsNotExist(err) || err == fs.ErrNotExist)
}
