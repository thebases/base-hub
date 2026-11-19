package cmd

// File overview:
// - Responsibility: `model <module> <entity> --fields ...` scaffolds one tbl.<module>_<entity> model (record struct, orm.Define registration, menu line, ACL row, i18n msgids and a registration test) inside an existing module.
// - Key symbols: `modelCmd`, `parseScaffoldFields`, `scaffoldModel`, `renderModelFile`.
// - Pattern: `modelCmd` is the parent for model tooling; the scaffold is its default action and sibling tools (e.g. `model check`) attach as subcommands, which cobra resolves before the positional module argument.

import (
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"git.thebase.vn/base_hub/internal/services/log"
	"github.com/spf13/cobra"
)

var (
	scaffoldFieldsFlag      string
	scaffoldModulesRootFlag string

	scaffoldFieldNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	scaffoldOrderPattern     = regexp.MustCompile(`(?:orm\.Define\[[^\]]+\]\(&Models,|RegisterRuntimeBootstrap\([^,]+,)\s*(\d+)`)
	scaffoldSequencePattern  = regexp.MustCompile(`sequence="(\d+)"`)
)

var modelCmd = &cobra.Command{
	Use:   "model <module> <entity>",
	Short: "Scaffold a tbl.<module>_<entity> model inside an existing module",
	Long: `Scaffold a model: record struct, orm.Define registration, menu item, ACL row, i18n msgids and a registration test.

--fields is a comma-separated list of name:kind[!] entries ("!" = required):
  char, text, int, float, bool, date, datetime,
  selection(a|b), m2o(model), m2m(model), o2m(model,inverse_field)
Example: --fields "name:char!,state:selection(draft|done),partner_id:m2o(tbl.partner)"`,
	Args: cobra.ExactArgs(2),
	RunE: runModelScaffoldCommand,
}

func init() {
	modelCmd.Flags().StringVar(&scaffoldFieldsFlag, "fields", "", "field list, e.g. name:char!,state:selection(draft|done)")
	modelCmd.Flags().StringVar(&scaffoldModulesRootFlag, "modules-root", "", "modules directory (default ./modules)")
	rootCmd.AddCommand(modelCmd)
}

func runModelScaffoldCommand(_ *cobra.Command, args []string) error {
	root := scaffoldModulesRootFlag
	if root == "" {
		root = DefaultModuleRoot()
	}
	fields, err := parseScaffoldFields(scaffoldFieldsFlag)
	if err != nil {
		return err
	}
	modelFile, err := scaffoldModel(root, args[0], args[1], fields)
	if err != nil {
		return err
	}
	log.Infof("created model scaffold at %s", modelFile)
	return nil
}

// scaffoldField is one parsed entry of the --fields DSL.
type scaffoldField struct {
	Name     string
	Kind     string // canonical: char text int float bool date datetime selection m2o m2m o2m
	Required bool
	Args     []string // selection options, or relation model (+ inverse for o2m)
}

// parseScaffoldFields parses "name:kind[(args)][!]" entries. Entries are split
// on top-level commas only, because o2m(model,inverse) carries a comma inside
// its parentheses.
func parseScaffoldFields(spec string) ([]scaffoldField, error) {
	var fields []scaffoldField
	seen := map[string]bool{}
	for _, raw := range splitTopLevelCommas(spec) {
		field, err := parseScaffoldField(raw)
		if err != nil {
			return nil, err
		}
		if seen[field.Name] {
			return nil, fmt.Errorf("duplicate field %q", field.Name)
		}
		seen[field.Name] = true
		fields = append(fields, field)
	}
	return fields, nil
}

func splitTopLevelCommas(spec string) []string {
	var parts []string
	depth, start := 0, 0
	for i, r := range spec {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				parts = append(parts, spec[start:i])
				start = i + 1
			}
		}
	}
	parts = append(parts, spec[start:])
	var out []string
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

var scaffoldKindAliases = map[string]string{
	"char": "char", "text": "text", "int": "int", "float": "float", "bool": "bool",
	"date": "date", "datetime": "datetime", "selection": "selection",
	"m2o": "m2o", "m2m": "m2m", "o2m": "o2m",
}

func parseScaffoldField(raw string) (scaffoldField, error) {
	name, kindSpec, ok := strings.Cut(raw, ":")
	name = strings.TrimSpace(name)
	if !ok || !scaffoldFieldNamePattern.MatchString(name) {
		return scaffoldField{}, fmt.Errorf("invalid field %q: want name:kind", raw)
	}
	if name == "id" {
		return scaffoldField{}, fmt.Errorf("field %q is provided by orm.BaseModel", name)
	}
	kindSpec = strings.TrimSpace(kindSpec)
	field := scaffoldField{Name: name}
	if strings.HasSuffix(kindSpec, "!") {
		field.Required = true
		kindSpec = strings.TrimSpace(strings.TrimSuffix(kindSpec, "!"))
	}
	kindName, argSpec, hasArgs := strings.Cut(kindSpec, "(")
	kind, known := scaffoldKindAliases[strings.TrimSpace(kindName)]
	if !known {
		return scaffoldField{}, fmt.Errorf("field %q: unknown kind %q", name, kindName)
	}
	field.Kind = kind
	if hasArgs {
		if !strings.HasSuffix(argSpec, ")") {
			return scaffoldField{}, fmt.Errorf("field %q: missing closing parenthesis", name)
		}
		argSpec = strings.TrimSuffix(argSpec, ")")
	}
	return validateScaffoldFieldArgs(field, argSpec, hasArgs)
}

func validateScaffoldFieldArgs(field scaffoldField, argSpec string, hasArgs bool) (scaffoldField, error) {
	switch field.Kind {
	case "selection":
		for _, option := range strings.Split(argSpec, "|") {
			option = strings.TrimSpace(option)
			if !scaffoldFieldNamePattern.MatchString(option) {
				return field, fmt.Errorf("field %q: selection option %q must match %s", field.Name, option, scaffoldFieldNamePattern)
			}
			field.Args = append(field.Args, option)
		}
	case "m2o", "m2m", "o2m":
		for _, arg := range strings.Split(argSpec, ",") {
			field.Args = append(field.Args, strings.TrimSpace(arg))
		}
		want := 1
		if field.Kind == "o2m" {
			want = 2
		}
		if !hasArgs || len(field.Args) != want || field.Args[0] == "" || field.Args[want-1] == "" {
			return field, fmt.Errorf("field %q: %s needs %d argument(s), e.g. m2o(tbl.partner) or o2m(tbl.line,order_id)", field.Name, field.Kind, want)
		}
	default:
		if hasArgs {
			return field, fmt.Errorf("field %q: kind %s takes no arguments", field.Name, field.Kind)
		}
	}
	return field, nil
}

// scaffoldModel validates everything first and only then writes, so a bad
// request (unknown module, duplicate model, duplicate menu/ACL id) leaves the
// module untouched.
func scaffoldModel(modulesRoot, moduleRaw, entityRaw string, fields []scaffoldField) (string, error) {
	module, err := normalizeModuleName(moduleRaw)
	if err != nil {
		return "", err
	}
	entity := strings.TrimSpace(strings.ToLower(entityRaw))
	if !moduleNamePattern.MatchString(entity) {
		return "", fmt.Errorf("invalid entity name %q: use lowercase letters, numbers, and underscores, starting with a letter", entityRaw)
	}
	moduleRoot := filepath.Join(filepath.Clean(modulesRoot), module)
	if info, err := os.Stat(filepath.Join(moduleRoot, "models")); err != nil || !info.IsDir() {
		return "", fmt.Errorf("module %q has no models directory at %s (run `module %s` first)", module, moduleRoot, module)
	}

	plan := newModelScaffoldPlan(module, entity, fields)
	plan.order = nextScaffoldOrder(filepath.Join(moduleRoot, "models"))

	modelPath := filepath.Join(moduleRoot, "models", "tbl_"+module+"_"+entity+".go")
	if _, err := os.Stat(modelPath); err == nil {
		return "", fmt.Errorf("model file %s already exists", modelPath)
	}
	source, err := renderModelFile(plan)
	if err != nil {
		return "", err
	}
	testSource, err := renderModelTestFile(plan)
	if err != nil {
		return "", err
	}
	menuPath := filepath.Join(moduleRoot, "views", "menu-"+module+".xml")
	menuXML, err := appendMenuItem(menuPath, plan)
	if err != nil {
		return "", err
	}
	accessPath := filepath.Join(moduleRoot, "security", "sys_model_access.csv")
	accessCSV, err := appendAccessRow(accessPath, plan)
	if err != nil {
		return "", err
	}

	writes := []struct {
		path    string
		content string
	}{
		{modelPath, source},
		{strings.TrimSuffix(modelPath, ".go") + "_test.go", testSource},
		{menuPath, menuXML},
		{accessPath, accessCSV},
	}
	for _, w := range writes {
		if err := os.MkdirAll(filepath.Dir(w.path), 0o755); err != nil {
			return "", fmt.Errorf("create %s: %w", filepath.Dir(w.path), err)
		}
		if err := os.WriteFile(w.path, []byte(w.content), 0o644); err != nil {
			return "", fmt.Errorf("write %s: %w", w.path, err)
		}
	}
	if err := addScaffoldI18n(moduleRoot, module, plan.msgids()); err != nil {
		return "", err
	}
	return modelPath, nil
}

// modelScaffoldPlan carries the names derived once from <module>/<entity>.
type modelScaffoldPlan struct {
	Module, Entity string
	ModelName      string // tbl.<module>_<entity>
	GoName         string // PascalCase of <module>_<entity>
	Label          string // plural, shown in the registry and the pot
	MenuName       string
	Fields         []scaffoldField
	order          int
}

func newModelScaffoldPlan(module, entity string, fields []scaffoldField) modelScaffoldPlan {
	return modelScaffoldPlan{
		Module:    module,
		Entity:    entity,
		ModelName: "tbl." + module + "_" + entity,
		GoName:    pascalCase(module + "_" + entity),
		Label:     titleCaseModuleName(module) + " " + pluralTitle(entity),
		MenuName:  pluralTitle(entity),
		Fields:    fields,
	}
}

// pascalCase maps snake_case to a Go identifier, using Go's initialism style
// for id/ids (partner_id -> PartnerID, tag_ids -> TagIDs).
func pascalCase(snake string) string {
	parts := strings.Split(snake, "_")
	for i, part := range parts {
		switch {
		case part == "id" && i > 0:
			parts[i] = "ID"
		case part == "ids" && i > 0:
			parts[i] = "IDs"
		case part != "":
			parts[i] = strings.ToUpper(part[:1]) + part[1:]
		}
	}
	return strings.Join(parts, "")
}

func pluralTitle(snake string) string {
	title := titleCaseModuleName(snake)
	switch {
	case strings.HasSuffix(title, "y") && !strings.HasSuffix(title, "ay") && !strings.HasSuffix(title, "ey"):
		return strings.TrimSuffix(title, "y") + "ies"
	case strings.HasSuffix(title, "s"), strings.HasSuffix(title, "x"):
		return title + "es"
	}
	return title + "s"
}

// fieldLabel turns partner_id / tag_ids / due_date into Partner / Tags / Due Date.
func fieldLabel(name string) string {
	if base, ok := strings.CutSuffix(name, "_ids"); ok {
		return pluralTitle(base)
	}
	return titleCaseModuleName(strings.TrimSuffix(name, "_id"))
}

// msgids lists the translatable strings this scaffold introduces, with the
// same kinds `i18n extract` assigns (model label, field label, selection
// option, menu name).
func (p modelScaffoldPlan) msgids() []i18nEntry {
	entries := []i18nEntry{
		{MsgID: p.Label, Kinds: []string{"label"}},
		{MsgID: p.MenuName, Kinds: []string{"menu"}},
	}
	for _, f := range p.Fields {
		entries = append(entries, i18nEntry{MsgID: fieldLabel(f.Name), Kinds: []string{"field"}})
		if f.Kind != "selection" {
			continue
		}
		for _, option := range f.Args {
			kind := fmt.Sprintf("selection:%s,%s:%s", p.ModelName, f.Name, option)
			entries = append(entries, i18nEntry{MsgID: titleCaseModuleName(option), Kinds: []string{kind}})
		}
	}
	return entries
}

// nextScaffoldOrder picks max(existing registration order)+10 so the new model
// initializes after the module's current models (it may reference them).
func nextScaffoldOrder(modelsDir string) int {
	highest := 90
	files, _ := filepath.Glob(filepath.Join(modelsDir, "*.go"))
	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		for _, match := range scaffoldOrderPattern.FindAllStringSubmatch(string(data), -1) {
			if n, err := strconv.Atoi(match[1]); err == nil && n > highest {
				highest = n
			}
		}
	}
	return highest + 10
}

// goField renders one struct field line. Required scalars are values with
// notnull; optional ones are pointers so NULL round-trips.
func (p modelScaffoldPlan) goField(f scaffoldField) (line string, usesTime bool) {
	goType, bun := "string", f.Name
	meta := []string{}
	extra := ""
	pointer := !f.Required
	switch f.Kind {
	case "text":
		meta = append(meta, "kind:text")
	case "int":
		goType = "int64"
	case "float":
		goType = "float64"
	case "bool":
		goType, pointer = "bool", false
		bun += ",notnull,default:false"
	case "date", "datetime":
		goType, usesTime = "time.Time", true
		meta = append(meta, "kind:"+f.Kind)
	case "selection":
		options := make([]string, len(f.Args))
		for i, option := range f.Args {
			options[i] = "(" + option + "," + titleCaseModuleName(option) + ")"
		}
		extra = ` selection:"` + strings.Join(options, ",") + `"`
		if f.Required {
			// Required selections need a database default so inserts without the field stay valid.
			bun += ",notnull,default:'" + f.Args[0] + "'"
		}
	case "m2o":
		goType = "int64"
		ondelete := "SET NULL"
		if f.Required {
			ondelete = "RESTRICT"
		}
		meta = append(meta, "kind:many2one", "model:"+f.Args[0], "ondelete:"+ondelete, "index")
	case "m2m", "o2m":
		return p.goRelationField(f), false
	}
	if f.Required && f.Kind != "bool" && !strings.Contains(bun, "notnull") {
		bun += ",notnull"
	}
	if f.Required {
		meta = append(meta, "required")
	}
	meta = append(meta, "label:"+fieldLabel(f.Name), "help:"+p.help(f))
	if pointer {
		goType = "*" + goType
	}
	return fmt.Sprintf("%s %s `bun:%q json:%q meta:%q%s`", pascalCase(f.Name), goType, bun, f.Name, strings.Join(meta, ","), extra), usesTime
}

func (p modelScaffoldPlan) goRelationField(f scaffoldField) string {
	target := f.Args[0]
	var meta []string
	if f.Kind == "o2m" {
		meta = []string{"kind:one2many", "model:" + target, "inverse:" + f.Args[1], "stored:false"}
	} else {
		targetKey := target[strings.LastIndex(target, ".")+1:]
		meta = []string{
			"kind:many2many", "model:" + target,
			"relation_table:tbl_" + p.Module + "_" + p.Entity + "_" + strings.TrimSuffix(f.Name, "_ids") + "_rel",
			"relation_column:" + p.Entity + "_id",
			"relation_key:" + targetKey + "_id",
		}
	}
	meta = append(meta, "label:"+fieldLabel(f.Name), "help:"+p.help(f))
	return fmt.Sprintf("%s []int64 `bun:%q json:%q meta:%q`", pascalCase(f.Name), f.Name+",type:jsonb,nullzero", f.Name, strings.Join(meta, ","))
}

// help writes the field help sentence; `help:` must stay the last meta key
// because it consumes the rest of the tag.
func (p modelScaffoldPlan) help(f scaffoldField) string {
	label := strings.ToLower(fieldLabel(f.Name))
	switch f.Kind {
	case "selection":
		return "Selects the " + label + " of this record."
	case "m2o":
		return "Links this record to the related " + f.Args[0] + " record."
	case "m2m":
		return "Lists the " + f.Args[0] + " records linked to this record."
	case "o2m":
		return "Lists the related " + f.Args[0] + " records linked back through " + f.Args[1] + "."
	}
	return "Stores the " + label + " of this record."
}

func renderModelFile(p modelScaffoldPlan) (string, error) {
	var body strings.Builder
	needsTime := false
	for _, f := range p.Fields {
		line, usesTime := p.goField(f)
		needsTime = needsTime || usesTime
		body.WriteString("\t" + line + "\n")
	}
	imports := "\t\"git.thebase.vn/base_hub/internal/orm\"\n"
	if needsTime {
		imports = "\t\"time\"\n\n" + imports
	}
	src := fmt.Sprintf(`package models

// File overview:
// - Responsibility: defines the %[1]s model owned by the %[2]s module.
// - Key symbols: %[3]sRecord, %[3]sModelName, %[3]sModel.
// - Pattern: orm.Define reflects the record struct into the model definition and registers it on the module's ModelSet.

import (
%[4]s)

// %[3]sRecord stores one %[1]s row.
type %[3]sRecord struct {
	orm.BaseModel
%[5]s}

const %[3]sModelName = %[1]q

var %[3]sModel = orm.Define[%[3]sRecord](&Models, %[6]d, orm.ModelSpec{
	Name:  %[3]sModelName,
	Label: %[7]q,
})
`, p.ModelName, p.Module, p.GoName, imports, body.String(), p.order, p.Label)
	formatted, err := format.Source([]byte(src))
	if err != nil {
		return "", fmt.Errorf("format generated model: %w", err)
	}
	return string(formatted), nil
}

// renderModelTestFile emits a test that reflects the record into a definition,
// which fails on any invalid meta tag, and pins field kinds and required flags.
func renderModelTestFile(p modelScaffoldPlan) (string, error) {
	var checks strings.Builder
	kinds := map[string]string{
		"char": "FieldKindText", "text": "FieldKindText", "int": "FieldKindInteger", "float": "FieldKindFloat",
		"bool": "FieldKindBoolean", "date": "FieldKindDate", "datetime": "FieldKindDateTime",
		"selection": "FieldKindSelection", "m2o": "FieldKindManyToOne", "m2m": "FieldKindManyToMany", "o2m": "FieldKindOneToMany",
	}
	for _, f := range p.Fields {
		fmt.Fprintf(&checks, "\t\t{%q, orm.%s, %t},\n", f.Name, kinds[f.Kind], f.Required)
	}
	src := fmt.Sprintf(`package models

// File overview:
// - Responsibility: pins the %[1]s registration and field contract so a bad meta tag fails here, not at boot.
// - Key symbols: Test%[2]sModelRegistered.
// - Pattern: reflects the record struct without a database.

import (
	"testing"

	"git.thebase.vn/base_hub/internal/orm"
)

func Test%[2]sModelRegistered(t *testing.T) {
	if %[2]sModel == nil {
		t.Fatal("%[2]sModel was not registered")
	}
	definition, err := orm.ModelDefinitionFromRecord(%[2]sModelName, (*%[2]sRecord)(nil))
	if err != nil {
		t.Fatalf("definition: %%v", err)
	}
	for _, want := range []struct {
		name     string
		kind     orm.FieldKind
		required bool
	}{
%[3]s	} {
		field := definition.Fields[want.name]
		if field == nil {
			t.Fatalf("field %%s missing from definition", want.name)
		}
		if field.Kind != want.kind || field.Required != want.required {
			t.Fatalf("field %%s: kind=%%s required=%%t, want kind=%%s required=%%t", want.name, field.Kind, field.Required, want.kind, want.required)
		}
	}
}
`, p.ModelName, p.GoName, checks.String())
	formatted, err := format.Source([]byte(src))
	if err != nil {
		return "", fmt.Errorf("format generated test: %w", err)
	}
	return string(formatted), nil
}

// appendMenuItem returns the menu XML with the new menuitem inserted before
// </data>, creating the file when the module has no menu yet.
func appendMenuItem(path string, p modelScaffoldPlan) (string, error) {
	content := "<?xml version=\"1.0\" encoding=\"utf-8\"?>\n<data noupdate=\"1\">\n</data>\n"
	if data, err := os.ReadFile(path); err == nil {
		content = string(data)
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	id := "menu." + p.Module + "_" + p.Entity
	if strings.Contains(content, `id="`+id+`"`) {
		return "", fmt.Errorf("menu id %s already exists in %s", id, path)
	}
	sequence := 10
	for _, match := range scaffoldSequencePattern.FindAllStringSubmatch(content, -1) {
		if n, _ := strconv.Atoi(match[1]); n >= sequence {
			sequence = n + 1
		}
	}
	line := fmt.Sprintf("    <menuitem id=%q name=%q parent=\"menu.main\" sequence=\"%d\" route_path=\"/web/%s\" icon=\"box\"/>\n", id, p.MenuName, sequence, p.ModelName)
	end := strings.LastIndex(content, "</data>")
	if end < 0 {
		return "", fmt.Errorf("%s has no closing </data>", path)
	}
	return content[:end] + line + content[end:], nil
}

// appendAccessRow returns the ACL CSV with a base.group_user full-access row
// (matching discuss) appended, creating the file with its header if absent.
func appendAccessRow(path string, p modelScaffoldPlan) (string, error) {
	content := scaffoldAccessCSVHeader
	if data, err := os.ReadFile(path); err == nil {
		content = string(data)
		if content != "" && !strings.HasSuffix(content, "\n") {
			content += "\n"
		}
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	id := p.Module + ".access_" + p.Module + "_" + p.Entity + "_user"
	if strings.Contains(content, "\n"+id+",") || strings.HasPrefix(content, id+",") {
		return "", fmt.Errorf("access id %s already exists in %s", id, path)
	}
	return content + fmt.Sprintf("%s,%s user,base.group_user,%s,true,true,true,true,true\n", id, p.Label, p.ModelName), nil
}

// sortedKeys keeps generated output deterministic when iterating maps.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
