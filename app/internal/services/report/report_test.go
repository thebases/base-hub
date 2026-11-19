package report

// File overview:
// - Responsibility: verifies the SRS-F-020 render service — template resolution and
//   func-map behavior, the Gotenberg client contract, the read-only render guard, and
//   end-to-end HTML rendering against the shared PostgreSQL test fixture.

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"git.thebase.vn/base_hub/internal/modules"
	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/test"
)

// withTestTemplates swaps the embedded module FS for the test's template tree
// and restores the previous FS afterwards so parallel suites stay isolated.
func withTestTemplates(t *testing.T, files map[string]string) {
	t.Helper()
	mapFS := fstest.MapFS{}
	for name, content := range files {
		mapFS[name] = &fstest.MapFile{Data: []byte(content)}
	}
	previous := modules.EmbeddedFS()
	modules.SetEmbeddedFS(mapFS)
	t.Cleanup(func() { modules.SetEmbeddedFS(previous) })
}

func TestValidateTemplateRefRejectsBadPathsAndParseErrors(t *testing.T) {
	withTestTemplates(t, map[string]string{
		"demo/reports/ok.html":     `<p>{{t "Hello"}}</p>`,
		"demo/reports/broken.html": `<p>{{range .docs}}</p>`,
		"demo/data/leak.html":      `<p>not a report source</p>`,
	})

	if err := ValidateTemplateRef("demo/reports/ok.html"); err != nil {
		t.Fatalf("expected valid template to pass: %v", err)
	}
	if err := ValidateTemplateRef("demo/reports/broken.html"); err == nil {
		t.Fatalf("expected parse error for unterminated range")
	}
	// Only files modules publish under reports/ are reachable — a definition
	// must not read arbitrary module files.
	if err := ValidateTemplateRef("demo/data/leak.html"); err == nil {
		t.Fatalf("expected non-reports path to be rejected")
	}
	if err := ValidateTemplateRef("demo/reports/missing.html"); err == nil {
		t.Fatalf("expected missing template to be rejected")
	}
}

func TestConvertHTMLToPDFSpeaksGotenberg(t *testing.T) {
	var gotPath, gotPaperWidth, gotFile string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatalf("parse engine payload: %v", err)
		}
		gotPaperWidth = r.FormValue("paperWidth")
		if file, header, err := r.FormFile("files"); err == nil {
			gotFile = header.Filename
			_ = file.Close()
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("%PDF-fake"))
	}))
	defer server.Close()

	pdf, err := ConvertHTMLToPDF(context.Background(), server.URL, []byte("<p>doc</p>"), "A4")
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if string(pdf) != "%PDF-fake" {
		t.Fatalf("unexpected pdf payload %q", pdf)
	}
	if gotPath != "/forms/chromium/convert/html" {
		t.Fatalf("unexpected engine path %q", gotPath)
	}
	if gotFile != "index.html" {
		t.Fatalf("engine entry document must be index.html, got %q", gotFile)
	}
	if gotPaperWidth != "8.27" {
		t.Fatalf("expected A4 paperWidth 8.27, got %q", gotPaperWidth)
	}
}

func TestConvertHTMLToPDFMapsEngineFailures(t *testing.T) {
	// Unconfigured engine: HTML has no dependency, PDF reports unavailability.
	if _, err := ConvertHTMLToPDF(context.Background(), "", []byte("<p/>"), "A4"); err == nil || !strings.Contains(err.Error(), "pdf engine unavailable") {
		t.Fatalf("expected unavailable error for empty engine URL, got %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("engine exploded"))
	}))
	defer server.Close()
	if _, err := ConvertHTMLToPDF(context.Background(), server.URL, []byte("<p/>"), "A4"); err == nil || !strings.Contains(err.Error(), "engine status 500") {
		t.Fatalf("expected engine status error, got %v", err)
	}
}

type reportRenderDoc struct {
	orm.BaseModel
	Name   string  `bun:"name" json:"name"`
	Amount float64 `bun:"amount" json:"amount"`
}

const reportRenderModel = "test.report.render.doc"

func TestRenderHTMLLifecycleAgainstPostgres(t *testing.T) {
	bunDB, err := orm.NewBunDB(test.EnvPostgresConfig())
	if err != nil {
		t.Skipf("postgres fixture unavailable: %v", err)
	}
	t.Cleanup(func() { _ = bunDB.Close() })

	previous := orm.GetBunDB()
	orm.SetBunDB(bunDB)
	t.Cleanup(func() {
		if previous == nil {
			orm.SetBunDB(nil)
			return
		}
		orm.SetBunDB(&orm.BunDB{DB: previous})
	})

	orm.NewHandlerWithModel[reportRenderDoc](orm.ModelSpec{
		Name:       reportRenderModel,
		Label:      "Report Render Doc",
		Definition: orm.ModelDefinition(reportRenderModel, (*reportRenderDoc)(nil)),
	}.WithStorageName("test_report_render_doc")).InitDefault()

	ctx := context.Background()
	if _, err := bunDB.DB.ExecContext(ctx, `DROP TABLE IF EXISTS test_report_render_doc`); err != nil {
		t.Fatalf("drop fixture table: %v", err)
	}
	if _, err := bunDB.DB.NewCreateTable().Model((*reportRenderDoc)(nil)).ModelTableExpr("test_report_render_doc").Exec(ctx); err != nil {
		t.Fatalf("create fixture table: %v", err)
	}

	withTestTemplates(t, map[string]string{
		"demo/reports/doc.html": `<h1>{{t "Docs"}}</h1>{{range .docs}}<p class="row">{{.name}}: {{formatFloat .amount 2}}</p>{{end}}`,
	})

	env := orm.NewEnvironment(bunDB.DB, 1, nil, true)
	envCtx := orm.ContextWithEnvironment(ctx, env)

	created, err := orm.ExecuteRuntimeMethod(envCtx, reportRenderModel, "create", nil, []any{map[string]any{"name": "Invoice A", "amount": 12.5}}, nil)
	if err != nil {
		t.Fatalf("create doc: %v", err)
	}
	record, _ := created.(orm.RecordMap)
	idText := strings.TrimSpace(fmt.Sprint(record["id"]))
	if idText == "" || idText == "<nil>" {
		t.Fatalf("create did not return an id: %#v", record)
	}

	def := Definition{
		Name:        "Doc Report",
		ReportName:  "test.doc_report",
		Model:       reportRenderModel,
		TemplateRef: "demo/reports/doc.html",
		PaperFormat: "A4",
	}
	html, records, err := RenderHTML(envCtx, def, []string{idText})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected one loaded record, got %d", len(records))
	}
	output := string(html)
	if !strings.Contains(output, "Invoice A: 12.50") {
		t.Fatalf("expected rendered values in output, got %q", output)
	}

	// The render context is read-only: any mutation attempted with it must be
	// rejected by the runtime (SRS-F-020 §4).
	readOnly := ReadOnlyContext(envCtx)
	if _, err := orm.ExecuteRuntimeMethod(readOnly, reportRenderModel, "write", []string{idText}, []any{map[string]any{"name": "Mutated"}}, nil); err == nil {
		t.Fatalf("expected read-only environment to reject writes")
	}

	// Unknown ids must fail the whole request, not render partially.
	if _, _, err := RenderHTML(envCtx, def, []string{idText, "999999"}); err == nil {
		t.Fatalf("expected unreadable record to fail the render")
	}
}

// TestReportDatesUseCallerTimeZone covers K-17: instants render in the
// environment "tz" (UTC without one), never the server's zone; naive dates
// stay as written.
func TestReportDatesUseCallerTimeZone(t *testing.T) {
	instant := time.Date(2026, 1, 1, 23, 30, 0, 0, time.UTC)
	ctx := orm.ContextWithEnvironment(context.Background(), orm.NewEnvironment(nil, 0, map[string]any{"tz": "Asia/Ho_Chi_Minh"}, false))
	funcs := renderFuncMap("en", renderLocation(ctx))
	formatDate := funcs["formatDate"].(func(any) string)
	formatDateTime := funcs["formatDateTime"].(func(any) string)
	if got := formatDateTime(instant); got != "2026-01-02 06:30:00" {
		t.Fatalf("formatDateTime(time) = %q, want the Asia/Ho_Chi_Minh wall time", got)
	}
	if got := formatDate("2026-01-01T23:30:00Z"); got != "2026-01-02" {
		t.Fatalf("formatDate(RFC3339) = %q, want the local date", got)
	}
	if got := formatDate("2026-01-01"); got != "2026-01-01" {
		t.Fatalf("formatDate(naive date) = %q, must not shift", got)
	}
	if loc := renderLocation(context.Background()); loc != time.UTC {
		t.Fatalf("no environment: location = %v, want UTC", loc)
	}
}
