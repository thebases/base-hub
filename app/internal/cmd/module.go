package cmd

// File overview:
// - Responsibility: implements command-line workflow for module so operational tasks reuse the same config and bootstrap rules as the main service.
// - Key symbols: `init`, `DefaultModuleRoot`, `module`, `runtimeBootstrapEntry`.
// - Pattern: Concentrates startup ordering here so dependency initialization, embedded assets, and shutdown hooks stay predictable during deploys and local runs.

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"git.thebase.vn/base_hub/internal/modulesgen"
	"git.thebase.vn/base_hub/internal/services/log"
	"github.com/spf13/cobra"
)

const scaffoldAccessCSVHeader = "id,name,group_id,model_name,perm_read,perm_write,perm_create,perm_unlink,active\n"

var moduleNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

var moduleCmd = &cobra.Command{
	Use:   "module <module name>",
	Short: "Create a backend module scaffold under ./modules",
	Args:  cobra.ExactArgs(1),
	RunE:  runModuleCommand,
}

func runModuleCommand(cmd *cobra.Command, args []string) error {
	moduleName, err := normalizeModuleName(args[0])
	if err != nil {
		return err
	}

	moduleRoot, err := scaffoldModule(DefaultModuleRoot(), moduleName)
	if err != nil {
		return err
	}

	log.Infof("created module scaffold at %s", moduleRoot)
	return nil
}

func init() {
	rootCmd.AddCommand(moduleCmd)
}

// DefaultModuleRoot selects the module root handled by this package.
func DefaultModuleRoot() string {
	return filepath.Clean("./modules")
}

func normalizeModuleName(raw string) (string, error) {
	name := strings.TrimSpace(strings.ToLower(raw))
	if name == "" {
		log.Errorf("module name is required")
		return "", fmt.Errorf("module name is required")
	}
	if !moduleNamePattern.MatchString(name) {
		log.Errorf("invalid module name %q: use lowercase letters, numbers, and underscores, starting with a letter", raw)
		return "", fmt.Errorf("invalid module name %q: use lowercase letters, numbers, and underscores, starting with a letter", raw)
	}
	return name, nil
}

func scaffoldModule(modulesRoot string, moduleName string) (string, error) {
	moduleName, err := normalizeModuleName(moduleName)
	if err != nil {
		return "", err
	}

	root := filepath.Clean(modulesRoot)
	moduleRoot := filepath.Join(root, moduleName)

	if info, err := os.Stat(moduleRoot); err == nil {
		if info.IsDir() {
			log.Errorf("module %q already exists at %s", moduleName, moduleRoot)
			return "", fmt.Errorf("module %q already exists at %s", moduleName, moduleRoot)
		}
		log.Errorf("module path %s already exists and is not a directory", moduleRoot)
		return "", fmt.Errorf("module path %s already exists and is not a directory", moduleRoot)
	} else if !os.IsNotExist(err) {
		log.Errorf("stat module path %s: %v", moduleRoot, err)
		return "", fmt.Errorf("stat module path %s: %w", moduleRoot, err)
	}

	if err := os.MkdirAll(root, 0o755); err != nil {
		log.Errorf("create modules root %s: %v", root, err)
		return "", fmt.Errorf("create modules root %s: %w", root, err)
	}

	files := []struct {
		path    string
		content string
	}{
		{path: filepath.Join(moduleRoot, moduleName+".go"), content: moduleBootstrapTemplate(moduleName)},
		{path: filepath.Join(moduleRoot, "manifest.md"), content: moduleManifestTemplate(moduleName)},
		{path: filepath.Join(moduleRoot, "data", ".gitkeep"), content: ""},
		{path: filepath.Join(moduleRoot, "i18n", ".gitkeep"), content: ""},
		{path: filepath.Join(moduleRoot, "models", "runtime_bootstrap.go"), content: moduleRuntimeBootstrapTemplate(moduleName)},
		{path: filepath.Join(moduleRoot, "security", "sys_model_access.csv"), content: scaffoldAccessCSVHeader},
		{path: filepath.Join(moduleRoot, "static", ".gitkeep"), content: ""},
		{path: filepath.Join(moduleRoot, "views", "dashboard-"+moduleName+".xml"), content: moduleDashboardViewTemplate(moduleName)},
	}

	if err := writeModuleScaffoldFiles(files); err != nil {
		_ = os.RemoveAll(moduleRoot)
		return "", err
	}

	if err := modulesgen.GenerateModuleImports(modulesgen.GenerateOptions{
		ModulesRoot: root,
		ImportBase:  "git.thebase.vn/base_hub/modules",
		OutputPath:  filepath.Join(root, "all", "modules_gen.go"),
	}); err != nil {
		log.Errorf("refresh module import registry: %v", err)
		return "", fmt.Errorf("refresh module import registry: %w", err)
	}

	return moduleRoot, nil
}

func writeModuleScaffoldFiles(files []struct {
	path    string
	content string
}) error {
	for _, file := range files {
		path := file.path
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			log.Errorf("create scaffold directory for %s: %v", path, err)
			return fmt.Errorf("create scaffold directory for %s: %w", path, err)
		}
		if err := os.WriteFile(path, []byte(file.content), 0o644); err != nil {
			log.Errorf("write scaffold file %s: %v", path, err)
			return fmt.Errorf("write scaffold file %s: %w", path, err)
		}
	}
	return nil
}

func moduleBootstrapTemplate(moduleName string) string {
	return fmt.Sprintf(`package %s

import (
	"context"
	_ "embed"

	kernelmodules "git.thebase.vn/base_hub/internal/modules"
	modulemodels "git.thebase.vn/base_hub/modules/%s/models"
)

type module struct{}

//go:embed manifest.md
var manifestDescription string

// init registers this package runtime during bootstrap so its models, hooks, or commands are available before requests arrive.
func init() {
	kernelmodules.Register(module{})
}

// Manifest handles the manifest handled by this package.
func (module) Manifest() kernelmodules.Manifest {
	return kernelmodules.MustManifestFromMarkdown(manifestDescription)
}

// Bootstrap handles the bootstrap handled by this package.
func (module) Bootstrap(_ context.Context, boot *kernelmodules.BootstrapContext) error {
	return boot.RegisterRuntime("%s.runtime.models", modulemodels.InitRuntimeModels)
}
`, moduleName, moduleName, moduleName)
}

func moduleRuntimeBootstrapTemplate(moduleName string) string {
	return fmt.Sprintf(`package models

// File overview:
// - Responsibility: owns the %s module's orm.ModelSet; every model file registers itself
//   with orm.Define(&Models, order, spec) so no central model list is needed.
// - Key symbols: Models, InitRuntimeModels.

import "git.thebase.vn/base_hub/internal/orm"

// Models collects this module's runtime models in registration order.
var Models orm.ModelSet

// InitRuntimeModels initializes the module's models and returns their shutdown.
func InitRuntimeModels() func() { return Models.Init() }
`, moduleName)
}

// Shared by moduleManifestTemplate and moduleDashboardViewTemplate so the
// scaffolded manifest title and the seeded dashboard's display name derive
// from the module name the same way.
func titleCaseModuleName(moduleName string) string {
	parts := strings.Split(moduleName, "_")
	for i, part := range parts {
		if part == "" {
			continue
		}
		parts[i] = strings.ToUpper(part[:1]) + part[1:]
	}
	return strings.Join(parts, " ")
}

// Seeds one ui.view row of type="dashboard" for the new module so `views/` is
// never an empty placeholder — the module author fills in real <card>/<link>
// entries once they add their own models. Dashboards are ui.view rows that
// are not model-bound (they describe a whole module's dashboard tab, not one
// runtime model): `model` is left blank and `module`/`sequence` carry the
// manifest module name and tab ordering instead. noupdate="1" matches this
// repo's existing convention: every other views/*.xml file across every
// module (base, base-workflow, helpdesk, website, ...) uses noupdate="1", so
// a freshly scaffolded module's seed file follows the same
// reimport-is-a-noop rule rather than inventing a new noupdate="0"
// convention for this one file. File name follows the repo-wide
// `<ui type>-<ui name>.xml` views/ convention (e.g. `dashboard-helpdesk.xml`,
// `menu-helpdesk.xml`, `view-helpdesk-ticket.xml`).
func moduleDashboardViewTemplate(moduleName string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="utf-8"?>
<data noupdate="1">
    <record id="dashboard_%s" model="ui.view">
      <field name="name">%s</field>
      <field name="type">dashboard</field>
      <field name="module">%s</field>
      <field name="sequence">10</field>
      <field name="arch" type="xml">
        <dashboard>
          <cards/>
          <quicklinks/>
        </dashboard>
      </field>
    </record>
</data>
`, moduleName, titleCaseModuleName(moduleName), moduleName)
}

func moduleManifestTemplate(moduleName string) string {
	title := titleCaseModuleName(moduleName)
	return fmt.Sprintf(`---
name: %s
version: 0.1.0
category: Hidden
installable: true
depends: [base]
auto_install: false
frontend_widgets: {}
---

# %s

Scaffold module for %s.

Use this module to add runtime models, seed data, access rules, static assets,
translations, and XML views.
`, moduleName, title, title)
}
