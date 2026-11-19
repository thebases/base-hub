package cmd

// File overview:
// - Responsibility: `i18n extract <module>` rebuilds a module's .pot from its model sources and view XML, then merges new msgids into every .po without touching existing translations.
// - Key symbols: `i18nCmd`, `extractModuleI18n`, `extractModuleEntries`, `poCatalog`, `addScaffoldI18n`.
// - Pattern: msgids follow the existing catalogs (field `label`, model `Label`, selection option labels, view `string/placeholder/title` attrs, menu names); extraction reads Go source statically so it needs no database, no running registry, and does not depend on how models are registered.

import (
	"encoding/xml"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"git.thebase.vn/base_hub/internal/i18n"
	"git.thebase.vn/base_hub/internal/services/log"
	"github.com/spf13/cobra"
)

var i18nModulesRootFlag string

var i18nCmd = &cobra.Command{
	Use:   "i18n",
	Short: "Translation catalog tooling",
}

var i18nExtractCmd = &cobra.Command{
	Use:   "extract <module>",
	Short: "Rebuild <module>/i18n/<module>.pot and merge new msgids into each .po",
	Args:  cobra.ExactArgs(1),
	RunE:  runI18nExtractCommand,
}

func init() {
	i18nExtractCmd.Flags().StringVar(&i18nModulesRootFlag, "modules-root", "", "modules directory (default ./modules)")
	i18nCmd.AddCommand(i18nExtractCmd)
	rootCmd.AddCommand(i18nCmd)
}

func runI18nExtractCommand(_ *cobra.Command, args []string) error {
	root := i18nModulesRootFlag
	if root == "" {
		root = DefaultModuleRoot()
	}
	module, err := normalizeModuleName(args[0])
	if err != nil {
		return err
	}
	changed, err := extractModuleI18n(filepath.Join(filepath.Clean(root), module), module)
	if err != nil {
		return err
	}
	if len(changed) == 0 {
		log.Infof("i18n catalogs for %s already up to date", module)
	}
	for _, path := range changed {
		log.Infof("updated %s", path)
	}
	return nil
}

// i18nEntry is one msgid with the gettext "#:" kinds that reference it.
type i18nEntry struct {
	MsgID string
	Kinds []string
}

// extractableKinds are the kinds a rebuild may remove. selection is deliberately
// excluded: selections can also be declared outside struct tags (specs, seeds)
// where static extraction cannot see them, so existing selection kinds only grow.
// Any other kind (frontend, ui:button, report, ...) is hand-maintained too.
var extractableKinds = map[string]bool{"field": true, "label": true, "view": true, "menu": true}

func kindFamily(kind string) string {
	family, _, _ := strings.Cut(kind, ":")
	return family
}

// extractModuleI18n rebuilds the .pot and merges new msgids into each .po. It
// returns the files it rewrote; an untouched module returns none (idempotent).
func extractModuleI18n(moduleRoot, module string) ([]string, error) {
	if info, err := os.Stat(moduleRoot); err != nil || !info.IsDir() {
		return nil, fmt.Errorf("module directory %s not found", moduleRoot)
	}
	extracted, err := extractModuleEntries(moduleRoot)
	if err != nil {
		return nil, err
	}
	potPath := filepath.Join(moduleRoot, "i18n", module+".pot")
	pot, err := loadCatalog(potPath, module, "Translation of Base Hub Server.")
	if err != nil {
		return nil, err
	}
	pot.rebuild(module, extracted)
	var changed []string
	if wrote, err := pot.save(potPath); err != nil {
		return nil, err
	} else if wrote {
		changed = append(changed, potPath)
	}
	wrote, err := mergeIntoPoFiles(moduleRoot, module, pot.entries(), false)
	return append(changed, wrote...), err
}

// addScaffoldI18n adds scaffolded msgids to the .pot and every .po without
// rebuilding, so unrelated catalog content is never reshuffled by `model`.
func addScaffoldI18n(moduleRoot, module string, entries []i18nEntry) error {
	potPath := filepath.Join(moduleRoot, "i18n", module+".pot")
	pot, err := loadCatalog(potPath, module, "Translation of Base Hub Server.")
	if err != nil {
		return err
	}
	pot.add(module, entries, true)
	if _, err := pot.save(potPath); err != nil {
		return err
	}
	_, err = mergeIntoPoFiles(moduleRoot, module, entries, true)
	return err
}

// mergeIntoPoFiles appends missing msgids (empty msgstr) to each .po. With
// createDefault, a module without any .po gets vi_vn.po so the scaffold ships
// a translation target.
func mergeIntoPoFiles(moduleRoot, module string, entries []i18nEntry, createDefault bool) ([]string, error) {
	dir := filepath.Join(moduleRoot, "i18n")
	paths, _ := filepath.Glob(filepath.Join(dir, "*.po"))
	if len(paths) == 0 && createDefault {
		paths = []string{filepath.Join(dir, "vi_vn.po")}
	}
	var changed []string
	for _, path := range paths {
		po, err := loadCatalog(path, module, "Vietnamese translation of Base Hub Server.")
		if err != nil {
			return changed, err
		}
		po.add(module, entries, false)
		wrote, err := po.save(path)
		if err != nil {
			return changed, err
		}
		if wrote {
			changed = append(changed, path)
		}
	}
	return changed, nil
}

// ---- catalog (gettext block-level model; blocks keep their raw text) ----

type poBlock struct {
	comments []string
	body     []string
	msgid    string
}

func (b *poBlock) kinds() []string {
	for _, c := range b.comments {
		if rest, ok := strings.CutPrefix(c, "#: "); ok {
			return strings.Split(rest, ", ")
		}
	}
	return nil
}

// setKinds rewrites (or inserts) the "#:" line, leaving other comments alone.
func (b *poBlock) setKinds(module string, kinds []string) {
	line := "#: " + strings.Join(kinds, ", ")
	for i, c := range b.comments {
		if strings.HasPrefix(c, "#: ") {
			b.comments[i] = line
			return
		}
	}
	if len(b.comments) == 0 {
		b.comments = append(b.comments, "#. module: "+module)
	}
	b.comments = append(b.comments, line)
}

type poCatalog struct {
	header string // raw header block text; empty means "not yet written"
	blocks []*poBlock
	loaded string // text read from disk, to skip no-op writes
}

func newBlock(module string, entry i18nEntry) *poBlock {
	b := &poBlock{
		comments: []string{"#. module: " + module},
		body:     []string{"msgid " + quotePO(entry.MsgID), `msgstr ""`},
		msgid:    entry.MsgID,
	}
	b.setKinds(module, sortedKinds(entry.Kinds))
	return b
}

func quotePO(s string) string {
	return strconv.Quote(s)
}

func sortedKinds(kinds []string) []string {
	set := map[string]bool{}
	for _, k := range kinds {
		set[k] = true
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func loadCatalog(path, module, title string) (*poCatalog, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &poCatalog{header: defaultPoHeader(module, title)}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	cat := &poCatalog{loaded: string(data)}
	for _, raw := range splitPoBlocks(string(data)) {
		entries, err := i18n.ParsePO(strings.NewReader(raw))
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		block := toBlock(raw)
		if len(entries) == 0 {
			// The block without a real msgid is the file header (or a stray comment block); keep its text verbatim.
			if cat.header == "" {
				cat.header = raw
			}
			continue
		}
		block.msgid = entries[0].MsgID
		cat.blocks = append(cat.blocks, block)
	}
	return cat, nil
}

func splitPoBlocks(text string) []string {
	var blocks []string
	for _, chunk := range regexp.MustCompile(`\n[ \t]*\n`).Split(strings.TrimRight(text, "\n"), -1) {
		if strings.TrimSpace(chunk) != "" {
			blocks = append(blocks, chunk)
		}
	}
	return blocks
}

func toBlock(raw string) *poBlock {
	b := &poBlock{}
	for _, line := range strings.Split(raw, "\n") {
		if strings.HasPrefix(line, "#") {
			b.comments = append(b.comments, line)
		} else {
			b.body = append(b.body, line)
		}
	}
	return b
}

func defaultPoHeader(module, title string) string {
	return "# " + title + "\n# This file contains the translation of the following modules:\n# \t* " + module + "\n#\n" +
		"msgid \"\"\nmsgstr \"\"\n" +
		"\"Project-Id-Version: Base Hub 0.1.0\\n\"\n" +
		"\"MIME-Version: 1.0\\n\"\n" +
		"\"Content-Type: text/plain; charset=UTF-8\\n\"\n" +
		"\"Content-Transfer-Encoding: 8bit\\n\"\n" +
		"\"Plural-Forms: nplurals=2; plural=(n != 1);\\n\""
}

func (c *poCatalog) entries() []i18nEntry {
	out := make([]i18nEntry, 0, len(c.blocks))
	for _, b := range c.blocks {
		out = append(out, i18nEntry{MsgID: b.msgid, Kinds: b.kinds()})
	}
	return out
}

// add inserts missing msgids (merging duplicate msgids in entries into one
// block). With updateKinds (the .pot) existing blocks also gain new kinds; the
// .po passes false so translated entries stay byte-identical.
func (c *poCatalog) add(module string, entries []i18nEntry, updateKinds bool) {
	index := map[string]*poBlock{}
	for _, b := range c.blocks {
		index[b.msgid] = b
	}
	var added []*poBlock
	for _, e := range entries {
		existing := index[e.MsgID]
		if existing == nil {
			b := newBlock(module, e)
			index[e.MsgID] = b
			added = append(added, b)
			continue
		}
		if !updateKinds && !contains(added, existing) {
			continue
		}
		if merged := sortedKinds(append(existing.kinds(), e.Kinds...)); !sameKinds(merged, existing.kinds()) {
			existing.setKinds(module, merged)
		}
	}
	c.insert(added)
}

func contains(blocks []*poBlock, b *poBlock) bool {
	for _, candidate := range blocks {
		if candidate == b {
			return true
		}
	}
	return false
}

// rebuild makes the .pot match extraction: owned kinds are recomputed, entries
// that lost every kind are dropped (they stay in each .po as obsolete), and
// hand-maintained kinds are preserved.
func (c *poCatalog) rebuild(module string, extracted map[string]*i18nEntry) {
	kept := c.blocks[:0]
	seen := map[string]bool{}
	for _, b := range c.blocks {
		kinds := preservedKinds(b.kinds())
		if e := extracted[b.msgid]; e != nil {
			kinds = append(kinds, e.Kinds...)
		}
		seen[b.msgid] = true
		if len(kinds) == 0 {
			continue
		}
		if kinds = sortedKinds(kinds); !sameKinds(kinds, b.kinds()) {
			b.setKinds(module, kinds)
		}
		kept = append(kept, b)
	}
	c.blocks = kept
	var added []*poBlock
	for _, id := range sortedKeys(extracted) {
		if !seen[id] {
			added = append(added, newBlock(module, *extracted[id]))
		}
	}
	c.insert(added)
}

func preservedKinds(kinds []string) []string {
	var out []string
	for _, k := range kinds {
		if !extractableKinds[kindFamily(k)] {
			out = append(out, k)
		}
	}
	return out
}

func sameKinds(a, b []string) bool {
	return reflect.DeepEqual(sortedKinds(a), sortedKinds(b))
}

// insert places new blocks alphabetically when the catalog is already sorted;
// a hand-ordered catalog gets them appended so existing order never churns.
func (c *poCatalog) insert(added []*poBlock) {
	if len(added) == 0 {
		return
	}
	sorted := sort.SliceIsSorted(c.blocks, func(i, j int) bool { return c.blocks[i].msgid < c.blocks[j].msgid })
	c.blocks = append(c.blocks, added...)
	if sorted {
		sort.SliceStable(c.blocks, func(i, j int) bool { return c.blocks[i].msgid < c.blocks[j].msgid })
	}
}

func (c *poCatalog) render() string {
	var parts []string
	if c.header != "" {
		parts = append(parts, c.header)
	}
	for _, b := range c.blocks {
		parts = append(parts, strings.Join(append(append([]string{}, b.comments...), b.body...), "\n"))
	}
	return strings.Join(parts, "\n\n") + "\n"
}

// save writes only when the rendered text differs from what was loaded.
func (c *poCatalog) save(path string) (bool, error) {
	out := c.render()
	if out == c.loaded {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(out), 0o644); err != nil {
		return false, fmt.Errorf("write %s: %w", path, err)
	}
	c.loaded = out
	return true, nil
}

// ---- extraction ----

// extractModuleEntries collects msgids from models/*.go and views/*.xml.
func extractModuleEntries(moduleRoot string) (map[string]*i18nEntry, error) {
	out := map[string]*i18nEntry{}
	if err := extractGoEntries(filepath.Join(moduleRoot, "models"), out); err != nil {
		return nil, err
	}
	if err := extractViewEntries(filepath.Join(moduleRoot, "views"), out); err != nil {
		return nil, err
	}
	return out, nil
}

func addExtracted(out map[string]*i18nEntry, msgid, kind string) {
	msgid = strings.TrimSpace(msgid)
	if msgid == "" {
		return
	}
	if out[msgid] == nil {
		out[msgid] = &i18nEntry{MsgID: msgid}
	}
	out[msgid].Kinds = sortedKinds(append(out[msgid].Kinds, kind))
}

var selectionOptionPattern = regexp.MustCompile(`\(([^,()]*),([^()]*)\)`)

func extractGoEntries(dir string, out map[string]*i18nEntry) error {
	paths, _ := filepath.Glob(filepath.Join(dir, "*.go"))
	fset := token.NewFileSet()
	var files []*ast.File
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
		files = append(files, file)
	}
	consts := collectStringConsts(files)
	for _, file := range files {
		fileModel := firstModelName(file, consts)
		ast.Inspect(file, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.TypeSpec:
				if st, ok := node.Type.(*ast.StructType); ok {
					extractStructEntries(node.Name.Name, fileModel, st, consts, out)
				}
			case *ast.CompositeLit:
				extractSpecLabel(node, out)
			}
			return true
		})
	}
	return nil
}

// firstModelName is the fallback model for a struct whose <Struct>ModelName
// constant does not exist (record and constant names are not always paired).
func firstModelName(file *ast.File, consts map[string]string) string {
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, spec := range gen.Specs {
			if vs, ok := spec.(*ast.ValueSpec); ok {
				for _, name := range vs.Names {
					if strings.HasSuffix(name.Name, "ModelName") && consts[name.Name] != "" {
						return consts[name.Name]
					}
				}
			}
		}
	}
	return ""
}

// collectStringConsts maps package-level string const/var names to their
// literal values (model names are declared as either).
func collectStringConsts(files []*ast.File) map[string]string {
	consts := map[string]string{}
	for _, file := range files {
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || (gen.Tok != token.CONST && gen.Tok != token.VAR) {
				continue
			}
			for _, spec := range gen.Specs {
				vs := spec.(*ast.ValueSpec)
				for i, name := range vs.Names {
					if i < len(vs.Values) {
						if lit, ok := vs.Values[i].(*ast.BasicLit); ok && lit.Kind == token.STRING {
							consts[name.Name], _ = strconv.Unquote(lit.Value)
						}
					}
				}
			}
		}
	}
	return consts
}

// extractSpecLabel reads Label from orm.ModelSpec{...} literals (the model
// label, kind "label").
func extractSpecLabel(lit *ast.CompositeLit, out map[string]*i18nEntry) {
	if !isModelSpecType(lit.Type) {
		return
	}
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		if key, ok := kv.Key.(*ast.Ident); ok && key.Name == "Label" {
			if val, ok := kv.Value.(*ast.BasicLit); ok && val.Kind == token.STRING {
				label, _ := strconv.Unquote(val.Value)
				addExtracted(out, label, "label")
			}
		}
	}
}

func isModelSpecType(expr ast.Expr) bool {
	switch t := expr.(type) {
	case *ast.SelectorExpr:
		return t.Sel.Name == "ModelSpec"
	case *ast.Ident:
		return t.Name == "ModelSpec"
	}
	return false
}

// extractStructEntries reads each field's meta `label:` and selection options.
// The selection kind names the owning model, derived from <Struct minus
// "Record">ModelName.
func extractStructEntries(structName, fileModel string, st *ast.StructType, consts map[string]string, out map[string]*i18nEntry) {
	modelName := consts[strings.TrimSuffix(structName, "Record")+"ModelName"]
	if modelName == "" {
		modelName = fileModel
	}
	for _, field := range st.Fields.List {
		if field.Tag == nil {
			continue
		}
		tagText, err := strconv.Unquote(field.Tag.Value)
		if err != nil {
			continue
		}
		tag := reflect.StructTag(tagText)
		meta := tag.Get("meta")
		if meta == "" {
			meta = tag.Get("opt")
		}
		addExtracted(out, metaLabel(meta), "field")
		jsonName, _, _ := strings.Cut(tag.Get("json"), ",")
		for _, m := range selectionOptionPattern.FindAllStringSubmatch(tag.Get("selection"), -1) {
			addExtracted(out, m[2], fmt.Sprintf("selection:%s,%s:%s", modelName, jsonName, strings.TrimSpace(m[1])))
		}
	}
}

// metaLabel returns the label: value. `help:` is free prose that ends the tag,
// so scanning stops there; labels themselves never contain commas.
func metaLabel(meta string) string {
	for _, part := range strings.Split(meta, ",") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, "help:") {
			return ""
		}
		if label, ok := strings.CutPrefix(part, "label:"); ok {
			return label
		}
	}
	return ""
}

var viewTextAttrs = map[string]bool{"string": true, "placeholder": true, "title": true}

func extractViewEntries(dir string, out map[string]*i18nEntry) error {
	paths, _ := filepath.Glob(filepath.Join(dir, "*.xml"))
	for _, path := range paths {
		if err := extractViewFile(path, out); err != nil {
			return err
		}
	}
	return nil
}

func extractViewFile(path string, out map[string]*i18nEntry) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()
	dec := xml.NewDecoder(f)
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
		start, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		for _, attr := range start.Attr {
			switch {
			case start.Name.Local == "menuitem" && attr.Name.Local == "name":
				addExtracted(out, attr.Value, "menu")
			case viewTextAttrs[attr.Name.Local]:
				addExtracted(out, attr.Value, "view")
			case attr.Name.Local == "label" && (start.Name.Local == "card" || start.Name.Local == "link"):
				// Dashboard cards and quicklinks carry their visible text in label=.
				addExtracted(out, attr.Value, "view")
			}
		}
	}
}
