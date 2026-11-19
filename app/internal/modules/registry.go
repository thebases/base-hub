package modules

// File overview:
// - Responsibility: owns the module registry, manifest discovery, dependency ordering, and bootstrap lifecycle for backend modules.
// - Key symbols: Module, BootstrapContext, Register, BootstrapAll.
// - Pattern: Centralizes module ordering and teardown so runtime registration remains deterministic and partial startup can unwind cleanly on failure.

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"git.thebase.vn/base_hub/internal/services/log"
	"gopkg.in/yaml.v3"
)

const (
	DefaultRoot         = "./modules"
	defaultManifestName = "manifest.md"
)

var importableDirNames = []string{"security", "data", "views"}

// Manifest mirrors the module contract stored on disk so runtime registration,
// discovery, and catalog sync all interpret the same metadata shape.
type Manifest struct {
	Name     string `yaml:"name"`
	Version  string `yaml:"version"`
	Category string `yaml:"category"`
	// Installable controls whether this module can be installed on a tenant DB at all.
	Installable bool     `yaml:"installable"`
	Depends     []string `yaml:"depends"`
	// AutoInstall means: once every module named in Depends is installed, install
	// this module automatically too (see modules.ExpandAutoInstall).
	AutoInstall     bool                      `yaml:"auto_install"`
	FrontendWidgets map[string]FrontendWidget `yaml:"frontend_widgets"`
	// FolderName is the module's directory name (e.g. "base-workflow"),
	// stamped by the Discover* functions after parsing — never read from the
	// manifest front matter. A module's name is always its folder name: the
	// only rule for defining a module name is `app/modules/<folder>`
	// -> module name `<folder>` (e.g. "base-workflow", "base-mcp").
	FolderName string `yaml:"-"`
}

// FrontendWidget keeps widget asset metadata normalized close to manifest
// parsing so the rest of the backend can trust these paths.
type FrontendWidget struct {
	JS   string   `yaml:"js"`
	CSS  []string `yaml:"css"`
	HTML string   `yaml:"html"`
}

// Module is the runtime contract every backend module implements so bootstrap
// can order modules and hand them one shared startup context.
type Module interface {
	Manifest() Manifest
	Bootstrap(ctx context.Context, boot *BootstrapContext) error
}

// BootstrapContext coordinates one-time runtime registration and teardown so
// modules cannot accidentally race each other during process startup.
type BootstrapContext struct {
	mu         sync.Mutex
	registered map[string]struct{}
	shutdowns  []func()
}

type moduleOrderer struct {
	modulesByName map[string]Module
	visiting      map[string]bool
	visited       map[string]bool
	ordered       []Module
}

// NewBootstrapContext starts with empty ownership state so bootstrap code can
// safely register runtime hooks as each module comes online.
func NewBootstrapContext() *BootstrapContext {
	return &BootstrapContext{
		registered: make(map[string]struct{}),
		shutdowns:  make([]func(), 0),
	}
}

// RegisterRuntime runs one runtime initializer once per name and records the
// matching shutdown callback in reverse-teardown order.
func (b *BootstrapContext) RegisterRuntime(name string, initFn func() func()) error {
	if b == nil {
		log.Errorf("modules: bootstrap context is nil")
		return fmt.Errorf("modules: bootstrap context is nil")
	}

	name = strings.TrimSpace(name)
	if name == "" {
		log.Errorf("modules: runtime bootstrap name is required")
		return fmt.Errorf("modules: runtime bootstrap name is required")
	}
	if initFn == nil {
		log.Errorf("modules: runtime bootstrap init function is required for %s", name)
		return fmt.Errorf("modules: runtime bootstrap init function is required for %s", name)
	}

	b.mu.Lock()
	if _, exists := b.registered[name]; exists {
		b.mu.Unlock()
		log.Errorf("modules: runtime bootstrap %s already registered", name)
		return fmt.Errorf("modules: runtime bootstrap %s already registered", name)
	}
	b.mu.Unlock()

	// Run setup without the mutex held so a slow initializer cannot block other
	// registration work or deadlock by re-entering bootstrap code.
	shutdown := initFn()
	if shutdown == nil {
		shutdown = func() {}
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	if _, exists := b.registered[name]; exists {
		// Tear the late duplicate back down so the first successful registration
		// stays authoritative and resources do not leak.
		shutdown()
		log.Errorf("modules: runtime bootstrap %s already registered", name)
		return fmt.Errorf("modules: runtime bootstrap %s already registered", name)
	}

	b.registered[name] = struct{}{}
	b.shutdowns = append(b.shutdowns, shutdown)
	return nil
}

// Shutdown tears modules down in reverse registration order so dependencies
// outlive the modules that were built on top of them.
func (b *BootstrapContext) Shutdown() {
	if b == nil {
		return
	}

	b.mu.Lock()
	shutdowns := append([]func(){}, b.shutdowns...)
	b.mu.Unlock()

	for i := len(shutdowns) - 1; i >= 0; i-- {
		shutdowns[i]()
	}
}

var (
	registryMu sync.RWMutex
	registry   = make(map[string]Module)
)

// Register publishes a module in the process-global registry so bootstrap can
// discover it later without hard-wiring package imports into the runtime.
func Register(module Module) {
	if module == nil {
		panic("modules: cannot register nil module")
	}

	name := strings.TrimSpace(module.Manifest().Name)
	if name == "" {
		panic("modules: module manifest name is required")
	}

	registryMu.Lock()
	defer registryMu.Unlock()
	if _, exists := registry[name]; exists {
		panic("modules: duplicate module registration for " + name)
	}
	registry[name] = module
}

// Registered returns modules in name order so callers see deterministic
// bootstrap and test behavior regardless of map iteration order.
func Registered() []Module {
	registryMu.RLock()
	defer registryMu.RUnlock()

	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	sort.Strings(names)

	modules := make([]Module, 0, len(names))
	for _, name := range names {
		modules = append(modules, registry[name])
	}
	return modules
}

// BootstrapAll resolves dependency order once, boots each module, and returns a
// shared shutdown callback so startup either completes cleanly or unwinds fully.
func BootstrapAll(ctx context.Context) (func(), []Manifest, error) {
	ordered, err := orderModules(Registered())
	if err != nil {
		return nil, nil, err
	}

	boot := NewBootstrapContext()
	manifests := make([]Manifest, 0, len(ordered))
	for _, module := range ordered {
		manifest := module.Manifest()
		if err := module.Bootstrap(ctx, boot); err != nil {
			boot.Shutdown()
			log.Errorf("bootstrap module %s: %v", manifest.Name, err)
			return nil, nil, fmt.Errorf("bootstrap module %s: %w", manifest.Name, err)
		}
		manifests = append(manifests, manifest)
	}

	return boot.Shutdown, manifests, nil
}

// orderModules uses a depth-first traversal so dependencies always appear
// before the modules that require them and cycles fail fast with context.
func orderModules(registered []Module) ([]Module, error) {
	orderer, names, err := newModuleOrderer(registered)
	if err != nil {
		return nil, err
	}

	for _, name := range names {
		if err := orderer.visit(name); err != nil {
			return nil, err
		}
	}

	return orderer.ordered, nil
}

// newModuleOrderer prepares one deterministic traversal state so module
// ordering stays flat, testable, and free of nested DFS helpers.
func newModuleOrderer(registered []Module) (*moduleOrderer, []string, error) {
	modulesByName := make(map[string]Module, len(registered))
	for _, module := range registered {
		name := strings.TrimSpace(module.Manifest().Name)
		if name == "" {
			log.Errorf("modules: registered module without a name")
			return nil, nil, fmt.Errorf("modules: registered module without a name")
		}
		modulesByName[name] = module
	}

	names := make([]string, 0, len(modulesByName))
	for name := range modulesByName {
		names = append(names, name)
	}
	sort.Strings(names)

	return &moduleOrderer{
		modulesByName: modulesByName,
		visiting:      make(map[string]bool, len(modulesByName)),
		visited:       make(map[string]bool, len(modulesByName)),
		ordered:       make([]Module, 0, len(modulesByName)),
	}, names, nil
}

// visit resolves one module and its dependencies with explicit state maps so
// cycle detection stays readable and deterministic during bootstrap.
func (o *moduleOrderer) visit(name string) error {
	if o.visited[name] {
		return nil
	}
	if o.visiting[name] {
		log.Errorf("modules: dependency cycle detected at %s", name)
		return fmt.Errorf("modules: dependency cycle detected at %s", name)
	}

	module, ok := o.modulesByName[name]
	if !ok {
		log.Errorf("modules: unknown module dependency %s", name)
		return fmt.Errorf("modules: unknown module dependency %s", name)
	}

	o.visiting[name] = true
	if err := o.visitDependencies(name, module.Manifest().Depends); err != nil {
		return err
	}

	o.visiting[name] = false
	o.visited[name] = true
	o.ordered = append(o.ordered, module)
	return nil
}

// visitDependencies keeps dependency normalization in one place so the DFS can
// preserve existing blank-skip and missing-module error behavior.
func (o *moduleOrderer) visitDependencies(moduleName string, dependencies []string) error {
	for _, dependency := range dependencies {
		dependency = strings.TrimSpace(dependency)
		if dependency == "" {
			continue
		}
		if _, exists := o.modulesByName[dependency]; !exists {
			log.Errorf("modules: module %s depends on unregistered module %s", moduleName, dependency)
			return fmt.Errorf("modules: module %s depends on unregistered module %s", moduleName, dependency)
		}
		if err := o.visit(dependency); err != nil {
			return err
		}
	}
	return nil
}

// MustManifestFromMarkdown keeps package-level registration code terse while
// still reusing the strict parser used by disk and embedded discovery.
func MustManifestFromMarkdown(raw string) Manifest {
	manifest, err := ParseManifestFromMarkdown(raw)
	if err != nil {
		panic(err)
	}
	return manifest
}

// ParseManifestFromMarkdown enforces the manifest shape once so every caller
// gets the same trimming, validation, and widget normalization behavior.
func ParseManifestFromMarkdown(raw string) (Manifest, error) {
	frontMatter, err := extractFrontMatter(raw)
	if err != nil {
		return Manifest{}, err
	}

	var manifest Manifest
	if err := yaml.Unmarshal([]byte(frontMatter), &manifest); err != nil {
		log.Errorf("modules: parse manifest front matter: %v", err)
		return Manifest{}, fmt.Errorf("modules: parse manifest front matter: %w", err)
	}

	manifest.Name = strings.TrimSpace(manifest.Name)
	if manifest.Name == "" {
		log.Errorf("modules: manifest name is required")
		return Manifest{}, fmt.Errorf("modules: manifest name is required")
	}
	manifest.Category = strings.TrimSpace(manifest.Category)
	manifest.Version = strings.TrimSpace(manifest.Version)
	manifest.Depends = normalizeStringSlice(manifest.Depends)
	manifest.FrontendWidgets = normalizeFrontendWidgets(manifest.FrontendWidgets)
	return manifest, nil
}

// DiscoverManifestsFromDisk walks a module root on disk and ignores folders
// without manifests so partially built local trees do not break startup.
func DiscoverManifestsFromDisk(root string) ([]Manifest, error) {
	moduleNames, err := listDiskModuleNames(root)
	if err != nil {
		return nil, err
	}

	manifests := make([]Manifest, 0, len(moduleNames))
	for _, moduleName := range moduleNames {
		raw, err := os.ReadFile(filepath.Join(root, moduleName, defaultManifestName))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}

		manifest, err := ParseManifestFromMarkdown(string(raw))
		if err != nil {
			return nil, err
		}
		manifest.FolderName = moduleName
		manifests = append(manifests, manifest)
	}

	return manifests, nil
}

// DiscoverManifestsFromFS mirrors disk discovery for embedded module bundles so
// startup behavior stays consistent between local and packaged binaries.
func DiscoverManifestsFromFS(fsys fs.FS, root string) ([]Manifest, error) {
	root = cleanFSRoot(root)
	moduleNames, err := listFSModuleNames(fsys, root)
	if err != nil {
		return nil, err
	}

	manifests := make([]Manifest, 0, len(moduleNames))
	for _, moduleName := range moduleNames {
		raw, err := fs.ReadFile(fsys, joinFSPath(joinFSPath(root, moduleName), defaultManifestName))
		if err != nil {
			if isNotExist(err) {
				continue
			}
			return nil, err
		}

		manifest, err := ParseManifestFromMarkdown(string(raw))
		if err != nil {
			return nil, err
		}
		manifest.FolderName = moduleName
		manifests = append(manifests, manifest)
	}

	return manifests, nil
}

// normalizeStringSlice drops empty values early so dependency and asset loops do
// not need to repeat whitespace handling.
func normalizeStringSlice(values []string) []string {
	if len(values) == 0 {
		return nil
	}

	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		out = append(out, value)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// normalizeFrontendWidgets removes empty widget declarations so manifest data
// stays compact and later asset extraction can trust each entry.
func normalizeFrontendWidgets(raw map[string]FrontendWidget) map[string]FrontendWidget {
	if len(raw) == 0 {
		return nil
	}

	out := make(map[string]FrontendWidget, len(raw))
	for name, widget := range raw {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}

		normalized := FrontendWidget{
			JS:   strings.TrimSpace(widget.JS),
			CSS:  normalizeStringSlice(widget.CSS),
			HTML: strings.TrimSpace(widget.HTML),
		}
		if normalized.JS == "" && normalized.HTML == "" && len(normalized.CSS) == 0 {
			continue
		}
		out[name] = normalized
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// extractFrontMatter keeps manifest parsing strict so random markdown body text
// cannot silently influence runtime module metadata.
func extractFrontMatter(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		log.Errorf("modules: manifest markdown is empty")
		return "", fmt.Errorf("modules: manifest markdown is empty")
	}
	if !strings.HasPrefix(trimmed, "---") {
		log.Errorf("modules: manifest front matter must start with ---")
		return "", fmt.Errorf("modules: manifest front matter must start with ---")
	}

	lines := strings.Split(trimmed, "\n")
	if len(lines) < 3 {
		log.Errorf("modules: manifest front matter is incomplete")
		return "", fmt.Errorf("modules: manifest front matter is incomplete")
	}

	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			end = i
			break
		}
	}
	if end == -1 {
		log.Errorf("modules: manifest front matter end marker not found")
		return "", fmt.Errorf("modules: manifest front matter end marker not found")
	}

	return strings.Join(lines[1:end], "\n"), nil
}

// DiscoverImportRootsFromDisk preserves the module import stage order so
// seed loading remains stable across environments and deployments.
func DiscoverImportRootsFromDisk(root string) ([]string, error) {
	moduleNames, err := listDiskModuleNames(root)
	if err != nil {
		return nil, err
	}

	roots := make([]string, 0)
	for _, moduleName := range moduleNames {
		moduleRoot := filepath.Join(root, moduleName)
		if _, err := os.Stat(filepath.Join(moduleRoot, defaultManifestName)); err != nil {
			continue
		}
		for _, dirName := range importableDirNames {
			candidate := filepath.Join(moduleRoot, dirName)
			info, err := os.Stat(candidate)
			if err == nil && info.IsDir() {
				roots = append(roots, candidate)
			}
		}
	}

	return roots, nil
}

// DiscoverImportRootsFromFS mirrors disk import-root discovery so embedded
// seeds load in the same stage order as local module directories.
func DiscoverImportRootsFromFS(fsys fs.FS, root string) ([]string, error) {
	root = cleanFSRoot(root)
	moduleNames, err := listFSModuleNames(fsys, root)
	if err != nil {
		return nil, err
	}

	roots := make([]string, 0)
	for _, moduleName := range moduleNames {
		moduleRoot := joinFSPath(root, moduleName)
		if _, err := fs.Stat(fsys, joinFSPath(moduleRoot, defaultManifestName)); err != nil {
			continue
		}
		for _, dirName := range importableDirNames {
			candidate := joinFSPath(moduleRoot, dirName)
			info, err := fs.Stat(fsys, candidate)
			if err == nil && info.IsDir() {
				roots = append(roots, candidate)
			}
		}
	}

	return roots, nil
}

// cleanFSRoot normalizes relative embedded paths because `io/fs` expects slash
// separators and treats `.` as the canonical root.
func cleanFSRoot(root string) string {
	root = filepath.ToSlash(strings.TrimSpace(root))
	root = strings.TrimPrefix(root, "./")
	root = strings.Trim(root, "/")
	if root == "" {
		return "."
	}
	return root
}

// joinFSPath avoids `filepath.Join` so embedded paths stay slash-based on every
// OS and therefore keep working with `io/fs`.
func joinFSPath(root, name string) string {
	root = cleanFSRoot(root)
	name = filepath.ToSlash(strings.TrimSpace(name))
	name = strings.Trim(name, "/")
	if root == "." || root == "" {
		return name
	}
	if name == "" {
		return root
	}
	return root + "/" + name
}

// listDiskModuleNames validates the root once and returns sorted module names so
// disk-backed discovery helpers can share the same deterministic traversal.
func listDiskModuleNames(root string) ([]string, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		log.Errorf("modules: %s is not a directory", root)
		return nil, fmt.Errorf("modules: %s is not a directory", root)
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

// listFSModuleNames mirrors disk directory listing so embedded discovery logic
// can reuse the same ordering and filtering rules.
func listFSModuleNames(fsys fs.FS, root string) ([]string, error) {
	entries, err := fs.ReadDir(fsys, root)
	if err != nil {
		return nil, err
	}

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}
