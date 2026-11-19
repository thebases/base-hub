package all

// File overview:
// - Responsibility: CI checks over the full module set (plan T86/T88): a golden snapshot of the model
//   registry, the model lint with its legacy allowlist, and the list of direct RuntimeRepositoryCreate/
//   Write callers that skip the guarded helpers.
// - Key symbols: TestRegisteredModelsGolden, TestRegisteredModelsLint, TestDirectRepositoryWriteCallers.
// - Regenerate the golden file with `go test ./modules/all -run TestRegisteredModelsGolden -update`.

import (
	"context"
	"flag"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	kernelmodules "git.thebase.vn/base_hub/internal/modules"
	"git.thebase.vn/base_hub/internal/orm"
)

var updateGolden = flag.Bool("update", false, "rewrite testdata golden files")

var bootstrapOnce sync.Once

// bootstrapAllModules registers every module's models exactly as server start-up does.
func bootstrapAllModules(t *testing.T) {
	t.Helper()
	var err error
	bootstrapOnce.Do(func() {
		_, _, err = kernelmodules.BootstrapAll(context.Background())
	})
	if err != nil {
		t.Fatalf("bootstrap modules: %v", err)
	}
}

// registrySnapshot renders the registry in registration order: model, storage name, field names.
// Converting a module to orm.Define/ModelSet must leave it byte-identical.
func registrySnapshot() string {
	var out strings.Builder
	for _, model := range orm.RegisteredModels() {
		fields := make([]string, 0, len(model.Fields))
		for _, field := range model.Fields {
			fields = append(fields, field.Name)
		}
		out.WriteString(model.Name + " " + model.StorageName + " " + strings.Join(fields, ",") + "\n")
	}
	return out.String()
}

func TestRegisteredModelsGolden(t *testing.T) {
	bootstrapAllModules(t)
	const path = "testdata/registered_models.golden"
	got := registrySnapshot()
	if *updateGolden {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden (run with -update to create it): %v", err)
	}
	if got != string(want) {
		t.Fatalf("registered models differ from %s (order, storage names or fields changed).\n%s", path, firstDiff(string(want), got))
	}
}

// firstDiff reports the first differing line so a failing conversion points at the model it broke.
func firstDiff(want, got string) string {
	wantLines, gotLines := strings.Split(want, "\n"), strings.Split(got, "\n")
	for i := 0; i < len(wantLines) || i < len(gotLines); i++ {
		var w, g string
		if i < len(wantLines) {
			w = wantLines[i]
		}
		if i < len(gotLines) {
			g = gotLines[i]
		}
		if w != g {
			return "line " + strconv.Itoa(i+1) + ":\n  want: " + w + "\n  got:  " + g
		}
	}
	return ""
}

// lintAllowlist holds legacy findings, keyed by "kind model[.field]", each with the reason it is
// tolerated. Shrink it as modules are fixed; a stale entry fails the test so it cannot linger.
var lintAllowlist = map[string]string{
	"json_bun_mismatch base.users.lang_id":               "legacy column `lang` kept for existing databases; wire key is lang_id",
	"unknown_relation sys.attachment.group_id":           "points at res.group (should be tbl.groups); fixing it lets migrate add an FK on existing rows, needs a data check first",
	"unknown_meta_key sys.module.module.state":           selectionInMeta,
	"unknown_meta_key tbl.website_asset.kind":            selectionInMeta,
	"unknown_meta_key tbl.website_blog_post.state":       selectionInMeta,
	"unknown_meta_key tbl.website_contact_request.state": selectionInMeta,
	"unknown_meta_key tbl.website_form_submission.state": selectionInMeta,
}

const selectionInMeta = "selection: inside meta is ignored (field stays char); moving it to a `selection:\"...\"` tag changes the field kind, owner check needed"

// redundantStorageAllowlist lists models whose WithStorageName equals the default; the orm.Define
// conversions (T88) drop the call and the entry with it.
var redundantStorageAllowlist = []string{}

func TestRegisteredModelsLint(t *testing.T) {
	bootstrapAllModules(t)
	allowed := make(map[string]string, len(lintAllowlist)+len(redundantStorageAllowlist))
	for key, reason := range lintAllowlist {
		allowed[key] = reason
	}
	for _, name := range redundantStorageAllowlist {
		allowed[orm.LintRedundantStorageTag+" "+name] = "converted by T88"
	}
	findings := orm.RegisteredModelFindings()
	if err := orm.ValidateRegisteredModels(); (err != nil) != (len(findings) > 0) {
		t.Fatalf("ValidateRegisteredModels() = %v with %d findings", err, len(findings))
	}
	seen := map[string]bool{}
	for _, finding := range findings {
		key := finding.Kind + " " + finding.Model
		if finding.Field != "" {
			key += "." + finding.Field
		}
		seen[key] = true
		if _, ok := allowed[key]; !ok {
			t.Errorf("new model lint finding: %s", finding)
		}
	}
	for key := range allowed {
		if !seen[key] {
			t.Errorf("allowlist entry %q no longer matches a finding: remove it", key)
		}
	}
}

const directWriteAllowlistPath = "testdata/direct_write_allowlist.txt"

// TestDirectRepositoryWriteCallers lists non-test functions in modules/** and internal/models/** that
// call orm.RuntimeRepositoryCreate/Write directly (skipping the guards of orm.CreateRecord/WriteRecords).
// Each must be in the allowlist with a reason (D9); `-update` rewrites the file keeping known reasons.
func TestDirectRepositoryWriteCallers(t *testing.T) {
	callers, err := directRepositoryWriteCallers("../..", "modules", "internal/models")
	if err != nil {
		t.Fatal(err)
	}
	allowed, err := readFuncAllowlist(directWriteAllowlistPath)
	if err != nil && !*updateGolden {
		t.Fatal(err)
	}
	if *updateGolden {
		writeDirectWriteAllowlist(t, callers, allowed)
		return
	}
	for _, caller := range callers {
		if _, ok := allowed[caller]; !ok {
			t.Errorf("%s calls orm.RuntimeRepositoryCreate/Write directly: use orm.CreateRecord/WriteRecords, or add it to %s with a reason", caller, directWriteAllowlistPath)
		}
		delete(allowed, caller)
	}
	for caller := range allowed {
		t.Errorf("allowlist entry %s no longer calls the repository directly: remove it", caller)
	}
}

// directRepositoryWriteCallers parses every non-test Go file under root/dirs and returns sorted
// "path:Func" (methods as "path:Type.Func") for each function calling orm.RuntimeRepositoryCreate/Write.
func directRepositoryWriteCallers(root string, dirs ...string) ([]string, error) {
	return funcsMatching(root, dirs, isDirectRepositoryWrite)
}

// funcsMatching parses every non-test Go file under root/dirs and returns sorted "path:Func" for each
// function whose body contains a node accepted by match.
func funcsMatching(root string, dirs []string, match func(ast.Node) bool) ([]string, error) {
	found := map[string]struct{}{}
	for _, dir := range dirs {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, entry fs.DirEntry, err error) error {
			if err != nil || entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return err
			}
			rel, _ := filepath.Rel(root, path)
			return collectMatchingFuncs(path, filepath.ToSlash(rel), match, found)
		})
		if err != nil {
			return nil, err
		}
	}
	out := make([]string, 0, len(found))
	for caller := range found {
		out = append(out, caller)
	}
	sort.Strings(out)
	return out, nil
}

func collectMatchingFuncs(path, rel string, match func(ast.Node) bool, found map[string]struct{}) error {
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		return err
	}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		if bodyContains(fn.Body, match) {
			found[rel+":"+funcDeclName(fn)] = struct{}{}
		}
	}
	return nil
}

func bodyContains(body *ast.BlockStmt, match func(ast.Node) bool) bool {
	hit := false
	ast.Inspect(body, func(node ast.Node) bool {
		if node != nil && match(node) {
			hit = true
		}
		return !hit
	})
	return hit
}

func isDirectRepositoryWrite(node ast.Node) bool {
	return isPackageSelector(node, "orm", "RuntimeRepositoryCreate") || isPackageSelector(node, "orm", "RuntimeRepositoryWrite")
}

// isPackageSelector reports whether node is the selector pkg.name (e.g. orm.RuntimeRepositoryWrite).
func isPackageSelector(node ast.Node, pkg, name string) bool {
	selector, ok := node.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != name {
		return false
	}
	ident, ok := selector.X.(*ast.Ident)
	return ok && ident.Name == pkg
}

func funcDeclName(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return fn.Name.Name
	}
	recv := fn.Recv.List[0].Type
	if star, ok := recv.(*ast.StarExpr); ok {
		recv = star.X
	}
	if index, ok := recv.(*ast.IndexExpr); ok {
		recv = index.X
	}
	if ident, ok := recv.(*ast.Ident); ok {
		return ident.Name + "." + fn.Name.Name
	}
	return fn.Name.Name
}

// readFuncAllowlist reads "path:Func  reason" lines; blank lines and # comments are skipped.
func readFuncAllowlist(path string) (map[string]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return map[string]string{}, err
	}
	out := map[string]string{}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		caller, reason, _ := strings.Cut(line, " ")
		out[caller] = strings.TrimSpace(reason)
	}
	return out, nil
}

const directWriteAllowlistHeader = `# Direct orm.RuntimeRepositoryCreate/Write callers (plan T15/T87, decision D9).
# Format: <path>:<Func>  <reason>. Switch RPC-facing ones to orm.CreateRecord/WriteRecords and delete the line;
# keep server-trusted paths (syncs, engine-owned writes) with a real reason.
`

func writeDirectWriteAllowlist(t *testing.T, callers []string, reasons map[string]string) {
	writeFuncAllowlist(t, directWriteAllowlistPath, directWriteAllowlistHeader, "TODO(T87): not yet reviewed", callers, reasons)
}

// writeFuncAllowlist rewrites a "path:Func  reason" allowlist, keeping known reasons.
func writeFuncAllowlist(t *testing.T, path, header, todo string, callers []string, reasons map[string]string) {
	t.Helper()
	var out strings.Builder
	out.WriteString(header)
	for _, caller := range callers {
		reason := reasons[caller]
		if reason == "" {
			reason = todo
		}
		out.WriteString(caller + "  " + reason + "\n")
	}
	if err := os.WriteFile(path, []byte(out.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}
