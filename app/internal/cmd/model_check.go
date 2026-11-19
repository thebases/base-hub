package cmd

// File overview:
// - Responsibility: `model check [module]` (plan T86) cross-checks a module's XML and ACL files against the
//   model registry: record and arch `<field name>` names exist on their model, menu `route_path="/web/<model>"`
//   targets a registered model, ACL rows name registered models and are not dead baseline grants.
// - Key symbols: `modelCheckCmd`, `checkModuleFiles`, `checkViewFile`, `checkACLFile`.
// - Pattern: registers every module's models the way the server does (BootstrapAll), then reads files only;
//   it never touches a database. Exit status is non-zero when anything is found.

import (
	"encoding/csv"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"git.thebase.vn/base_hub/internal/orm"
	"github.com/spf13/cobra"
)

var modelCheckCmd = &cobra.Command{
	Use:   "check [module]",
	Short: "Check module views, menus and ACL rows against the registered models",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runModelCheckCommand,
}

func init() {
	// `model` is the T89 scaffold parent; cobra resolves `check` before its positional args.
	modelCmd.AddCommand(modelCheckCmd)
}

func runModelCheckCommand(cmd *cobra.Command, args []string) error {
	shutdown := initRuntimeModels()
	defer shutdown()

	modules, err := modelCheckModules(DefaultModuleRoot(), args)
	if err != nil {
		return err
	}
	var findings []string
	for _, module := range modules {
		moduleFindings, err := checkModuleFiles(filepath.Join(DefaultModuleRoot(), module))
		if err != nil {
			return err
		}
		findings = append(findings, moduleFindings...)
	}
	for _, finding := range findings {
		fmt.Fprintln(cmd.OutOrStdout(), finding)
	}
	if len(findings) > 0 {
		return fmt.Errorf("model check: %d finding(s)", len(findings))
	}
	fmt.Fprintf(cmd.OutOrStdout(), "model check: %d module(s) clean\n", len(modules))
	return nil
}

// modelCheckModules returns the requested module, or every module directory under root.
func modelCheckModules(root string, args []string) ([]string, error) {
	if len(args) == 1 {
		module := strings.TrimSpace(args[0])
		if info, err := os.Stat(filepath.Join(root, module)); err != nil || !info.IsDir() {
			return nil, fmt.Errorf("model check: module %q not found under %s", module, root)
		}
		return []string{module}, nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var modules []string
	for _, entry := range entries {
		if entry.IsDir() && entry.Name() != "all" {
			modules = append(modules, entry.Name())
		}
	}
	return modules, nil
}

// checkModuleFiles checks views/*.xml and security/*.csv of one module directory.
func checkModuleFiles(dir string) ([]string, error) {
	var findings []string
	views, _ := filepath.Glob(filepath.Join(dir, "views", "*.xml"))
	sort.Strings(views)
	for _, path := range views {
		fileFindings, err := checkViewFile(path)
		if err != nil {
			return nil, err
		}
		findings = append(findings, fileFindings...)
	}
	acls, _ := filepath.Glob(filepath.Join(dir, "security", "*.csv"))
	sort.Strings(acls)
	for _, path := range acls {
		fileFindings, err := checkACLFile(path)
		if err != nil {
			return nil, err
		}
		findings = append(findings, fileFindings...)
	}
	return findings, nil
}

// viewChecker walks one XML file. Each open element pushes the model its `<field name>` children
// resolve against: a record's model, the ui.view `model` value inside an arch, or the relation
// target inside a relational field's inline sub-view.
type viewChecker struct {
	path      string
	decoder   *xml.Decoder
	stack     []string
	findings  []string
	viewModel string
}

func checkViewFile(path string) ([]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	checker := &viewChecker{path: path, decoder: xml.NewDecoder(file)}
	for {
		token, err := checker.decoder.Token()
		if errors.Is(err, io.EOF) {
			return checker.findings, nil
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		switch element := token.(type) {
		case xml.StartElement:
			if err := checker.start(element); err != nil {
				return nil, err
			}
		case xml.EndElement:
			checker.stack = checker.stack[:len(checker.stack)-1]
		}
	}
}

func (c *viewChecker) top() string {
	if len(c.stack) == 0 {
		return ""
	}
	return c.stack[len(c.stack)-1]
}

func (c *viewChecker) start(element xml.StartElement) error {
	model := c.top()
	switch element.Name.Local {
	case "record":
		model = xmlAttr(element, "model")
		c.viewModel = ""
	case "menuitem":
		c.checkMenu(element)
	case "field":
		name := xmlAttr(element, "name")
		// The record's own `model` and `arch` values carry the view target and its layout.
		if model == "ui.view" && name == "model" {
			value, err := c.text(element)
			c.viewModel = value
			return err
		}
		if model == "ui.view" && name == "arch" {
			c.stack = append(c.stack, c.viewModel)
			return nil
		}
		model = c.checkField(model, name)
	}
	c.stack = append(c.stack, model)
	return nil
}

// text reads a simple element's character data (it consumes the end element, so nothing is pushed).
func (c *viewChecker) text(element xml.StartElement) (string, error) {
	var value string
	if err := c.decoder.DecodeElement(&value, &element); err != nil {
		return "", fmt.Errorf("%s: %w", c.path, err)
	}
	return strings.TrimSpace(value), nil
}

// checkField reports a field missing on model and returns the model nested children resolve against.
func (c *viewChecker) checkField(model, name string) string {
	meta, ok := orm.LookupSharedModel(model)
	if !ok || name == "" {
		return model
	}
	for _, field := range meta.Fields {
		if field.Name == name {
			if field.Relation != "" {
				return field.Relation
			}
			return model
		}
	}
	c.findings = append(c.findings, fmt.Sprintf("%s: field %q does not exist on %s", c.path, name, model))
	return model
}

func (c *viewChecker) checkMenu(element xml.StartElement) {
	route := xmlAttr(element, "route_path")
	target, ok := strings.CutPrefix(route, "/web/")
	if !ok {
		return
	}
	target, _, _ = strings.Cut(target, "/")
	// Only model routes carry a dot; other /web/ pages (dashboards, apps) are not models.
	if !strings.Contains(target, ".") {
		return
	}
	if _, ok := orm.LookupSharedModel(target); !ok {
		c.findings = append(c.findings, fmt.Sprintf("%s: menu %q routes to unregistered model %s", c.path, xmlAttr(element, "id"), target))
	}
}

func xmlAttr(element xml.StartElement, name string) string {
	for _, attr := range element.Attr {
		if attr.Name.Local == name {
			return strings.TrimSpace(attr.Value)
		}
	}
	return ""
}

// checkACLFile reports ACL rows on unregistered models, and base.group_user rows on tbl./base.
// models, which the prefix baseline already grants in full (the row is dead, see backend context).
func checkACLFile(path string) ([]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	rows, err := csv.NewReader(file).ReadAll()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	modelColumn, groupColumn := csvColumn(rows[0], "model_name"), csvColumn(rows[0], "group_id")
	if modelColumn < 0 {
		return nil, nil
	}
	var findings []string
	for line, row := range rows[1:] {
		model := strings.TrimSpace(row[modelColumn])
		if _, ok := orm.LookupSharedModel(model); !ok {
			findings = append(findings, fmt.Sprintf("%s:%d: ACL row names unregistered model %s", path, line+2, model))
			continue
		}
		if groupColumn >= 0 && strings.TrimSpace(row[groupColumn]) == "base.group_user" &&
			(strings.HasPrefix(model, "tbl.") || strings.HasPrefix(model, "base.")) {
			findings = append(findings, fmt.Sprintf("%s:%d: base.group_user row on %s is dead (prefix baseline grants full CRUD)", path, line+2, model))
		}
	}
	return findings, nil
}

func csvColumn(header []string, name string) int {
	for index, column := range header {
		if strings.TrimSpace(column) == name {
			return index
		}
	}
	return -1
}
