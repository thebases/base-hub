package cmd

// File overview:
// - Responsibility: verifies the `model` scaffolder (fields DSL, generated model/menu/ACL/i18n/test text) and `i18n extract` idempotency.
// - Key symbols: focused Test cases over temp module trees.
// - Pattern: asserts on generated text and gofmt-parses it; the generated Go is not compiled here because it targets the module's own package.

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const scaffoldTestFields = "name:char!,note:text,qty:int,price:float,done:bool,due:date,at:datetime,state:selection(draft|done)!,partner_id:m2o(tbl.partner),tag_ids:m2m(tbl.tag),line_ids:o2m(tbl.demo_line,order_id)"

func TestParseScaffoldFields(t *testing.T) {
	t.Parallel()

	fields, err := parseScaffoldFields(scaffoldTestFields)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(fields) != 11 {
		t.Fatalf("want 11 fields, got %d", len(fields))
	}
	if !fields[0].Required || fields[0].Kind != "char" {
		t.Fatalf("name field wrong: %+v", fields[0])
	}
	if got := strings.Join(fields[7].Args, "|"); got != "draft|done" || !fields[7].Required {
		t.Fatalf("selection field wrong: %+v", fields[7])
	}
	if got := fields[10].Args; len(got) != 2 || got[0] != "tbl.demo_line" || got[1] != "order_id" {
		t.Fatalf("o2m args wrong: %v", got)
	}

	for _, bad := range []string{"name", "id:int", "x:weird", "x:m2o", "x:o2m(tbl.a)", "x:char(1)", "a:int,a:int", "x:selection(Bad Value)"} {
		if _, err := parseScaffoldFields(bad); err == nil {
			t.Fatalf("expected %q to fail", bad)
		}
	}
}

func TestScaffoldModelWritesModelMenuACLAndI18n(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "modules")
	moduleRoot, err := scaffoldModule(root, "demo")
	if err != nil {
		t.Fatalf("scaffold module: %v", err)
	}
	fields, err := parseScaffoldFields(scaffoldTestFields)
	if err != nil {
		t.Fatalf("fields: %v", err)
	}
	modelPath, err := scaffoldModel(root, "demo", "sale_order", fields)
	if err != nil {
		t.Fatalf("scaffold model: %v", err)
	}

	model := readTestFile(t, modelPath)
	if _, err := parser.ParseFile(token.NewFileSet(), modelPath, model, parser.AllErrors); err != nil {
		t.Fatalf("generated model does not parse: %v\n%s", err, model)
	}
	for _, want := range []string{
		"var DemoSaleOrderModel = orm.Define[DemoSaleOrderRecord](&Models, 100, orm.ModelSpec{",
		`const DemoSaleOrderModelName = "tbl.demo_sale_order"`,
		`Label: "Demo Sale Orders"`,
		`bun:"name,notnull" json:"name" meta:"required,label:Name,help:Stores the name of this record."`,
		`selection:"(draft,Draft),(done,Done)"`,
		`bun:"state,notnull,default:'draft'"`,
		`meta:"kind:many2one,model:tbl.partner,ondelete:SET NULL,index,label:Partner,help:`,
		`relation_table:tbl_demo_sale_order_tag_rel,relation_column:sale_order_id,relation_key:tag_id,label:Tags`,
		`kind:one2many,model:tbl.demo_line,inverse:order_id,stored:false,label:Lines,`,
		`PartnerID `,
		`TagIDs `,
		`"time"`,
	} {
		if !strings.Contains(model, want) {
			t.Fatalf("model missing %q:\n%s", want, model)
		}
	}
	// label and help are always the last two meta keys (help swallows the rest of the tag).
	if strings.Contains(model, "help:Stores the name of this record.,") {
		t.Fatalf("help is not last in meta:\n%s", model)
	}

	testSrc := readTestFile(t, strings.TrimSuffix(modelPath, ".go")+"_test.go")
	if !strings.Contains(testSrc, "func TestDemoSaleOrderModelRegistered") || !strings.Contains(testSrc, `{"state", orm.FieldKindSelection, true}`) {
		t.Fatalf("registration test wrong:\n%s", testSrc)
	}

	menu := readTestFile(t, filepath.Join(moduleRoot, "views", "menu-demo.xml"))
	if !strings.Contains(menu, `<menuitem id="menu.demo_sale_order" name="Sale Orders" parent="menu.main" sequence="10" route_path="/web/tbl.demo_sale_order"`) {
		t.Fatalf("menu line missing:\n%s", menu)
	}
	access := readTestFile(t, filepath.Join(moduleRoot, "security", "sys_model_access.csv"))
	if !strings.Contains(access, "demo.access_demo_sale_order_user,Demo Sale Orders user,base.group_user,tbl.demo_sale_order,true,true,true,true,true\n") {
		t.Fatalf("ACL row missing:\n%s", access)
	}
	pot := readTestFile(t, filepath.Join(moduleRoot, "i18n", "demo.pot"))
	for _, want := range []string{"#: label\nmsgid \"Demo Sale Orders\"", "#: menu\nmsgid \"Sale Orders\"", "#: selection:tbl.demo_sale_order,state:draft\nmsgid \"Draft\"", "#: field, selection:tbl.demo_sale_order,state:done\nmsgid \"Done\""} {
		if !strings.Contains(pot, want) {
			t.Fatalf("pot missing %q:\n%s", want, pot)
		}
	}
	if !strings.Contains(readTestFile(t, filepath.Join(moduleRoot, "i18n", "vi_vn.po")), `msgid "Partner"`+"\nmsgstr \"\"") {
		t.Fatal("vi_vn.po missing new msgid")
	}

	if _, err := scaffoldModel(root, "demo", "sale_order", fields); err == nil {
		t.Fatal("second scaffold of the same model must fail")
	}
	if _, err := scaffoldModel(root, "missing", "x", nil); err == nil {
		t.Fatal("scaffold into a missing module must fail")
	}
}

// TestI18nExtractIsIdempotentAndKeepsTranslations: extracting the scaffolded
// module again changes nothing, and a hand-added translation plus an obsolete
// msgid survive a rebuild.
func TestI18nExtractIsIdempotentAndKeepsTranslations(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "modules")
	moduleRoot, err := scaffoldModule(root, "demo")
	if err != nil {
		t.Fatalf("scaffold module: %v", err)
	}
	fields, _ := parseScaffoldFields("name:char!,state:selection(draft|done)")
	if _, err := scaffoldModel(root, "demo", "task", fields); err != nil {
		t.Fatalf("scaffold model: %v", err)
	}

	changed, err := extractModuleI18n(moduleRoot, "demo")
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(changed) != 0 {
		t.Fatalf("extract right after scaffold should be a no-op, rewrote %v", changed)
	}

	poPath := filepath.Join(moduleRoot, "i18n", "vi_vn.po")
	po := strings.Replace(readTestFile(t, poPath), "msgid \"Name\"\nmsgstr \"\"", "msgid \"Name\"\nmsgstr \"Ten\"", 1)
	po += "\n#. module: demo\n#: field\nmsgid \"Gone\"\nmsgstr \"Da xoa\"\n"
	writeTestFile(t, poPath, po)
	// A new field label appears in source: it must reach pot and po, nothing else may move.
	modelPath := filepath.Join(moduleRoot, "models", "tbl_demo_task.go")
	writeTestFile(t, modelPath, strings.Replace(readTestFile(t, modelPath), "label:Name,", "label:Title,", 1))

	if _, err := extractModuleI18n(moduleRoot, "demo"); err != nil {
		t.Fatalf("extract: %v", err)
	}
	gotPo := readTestFile(t, poPath)
	for _, want := range []string{"msgstr \"Ten\"", "msgstr \"Da xoa\"", "msgid \"Title\"\nmsgstr \"\""} {
		if !strings.Contains(gotPo, want) {
			t.Fatalf("po lost %q:\n%s", want, gotPo)
		}
	}
	pot := readTestFile(t, filepath.Join(moduleRoot, "i18n", "demo.pot"))
	if !strings.Contains(pot, `msgid "Title"`) || strings.Contains(pot, `msgid "Name"`) {
		t.Fatalf("pot not rebuilt from source:\n%s", pot)
	}
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
