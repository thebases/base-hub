package routes

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"git.thebase.vn/base_hub/internal/config"
	"git.thebase.vn/base_hub/internal/orm"
	"github.com/gofiber/fiber/v3"
)

func TestResolveWebsiteRuntimeDBNamePrefersBoundRequestEnvironment(t *testing.T) {
	app := fiber.New()
	app.Get("/", func(c fiber.Ctx) error {
		env := orm.NewEnvironmentWithDBName(nil, "tenant_site_db", 0, nil, false)
		c.SetContext(orm.ContextWithEnvironment(context.Background(), env))
		got, err := resolveWebsiteRuntimeDBName(c)
		if err != nil {
			t.Fatalf("resolveWebsiteRuntimeDBName() error = %v", err)
		}
		if got != "tenant_site_db" {
			t.Fatalf("resolveWebsiteRuntimeDBName() = %q, want tenant_site_db", got)
		}
		return nil
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test() error = %v", err)
	}
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}

func TestRuntimeInt64AcceptsMany2OneRelationShapes(t *testing.T) {
	tests := []struct {
		name string
		raw  any
		want int64
	}{
		{name: "tuple relation", raw: []any{int64(42), "Deployment"}, want: 42},
		{name: "object relation", raw: map[string]any{"id": float64(77), "name": "Deployment"}, want: 77},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := runtimeInt64(tt.raw)
			if !ok || got != tt.want {
				t.Fatalf("runtimeInt64(%#v) = (%d, %v), want (%d, true)", tt.raw, got, ok, tt.want)
			}
		})
	}
}

func TestNormalizeRuntimePathCollapsesSlashesAndTrailing(t *testing.T) {
	tests := map[string]string{
		"":        "/",
		"/":       "/",
		"about":   "/about",
		"/about/": "/about",
		"//a//b":  "/a/b",
	}
	for input, want := range tests {
		if got := normalizeRuntimePath(input); got != want {
			t.Fatalf("normalizeRuntimePath(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestNormalizeRuntimeHostStripsPortAndLowercases(t *testing.T) {
	tests := map[string]string{
		"":                        "",
		"  ":                      "",
		"Example.com":             "example.com",
		"Tenant.Example.com:8080": "tenant.example.com",
		"localhost:3000":          "localhost",
		" a.b.com ":               "a.b.com",
	}
	for input, want := range tests {
		if got := normalizeRuntimeHost(input); got != want {
			t.Fatalf("normalizeRuntimeHost(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestClassifyWebsiteRuntimeRequest(t *testing.T) {
	kindHeader := func(kind websiteRuntimeRequestKind) string {
		switch kind {
		case websiteRuntimeRequestPageDocument:
			return "page"
		case websiteRuntimeRequestPageAsset:
			return "asset"
		case websiteRuntimeRequestVendorAsset:
			return "vendor"
		default:
			return "none"
		}
	}

	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		return c.SendString(kindHeader(classifyWebsiteRuntimeRequest(c)))
	})

	tests := []struct {
		name       string
		method     string
		path       string
		accept     string
		wantStatus string
	}{
		{name: "root browser page", method: http.MethodGet, path: "/", accept: fiber.MIMETextHTML, wantStatus: "page"},
		{name: "nested browser page", method: http.MethodGet, path: "/about", accept: fiber.MIMETextHTML, wantStatus: "page"},
		{name: "site main script under prefix", method: http.MethodGet, path: "/_site/main.js", accept: "*/*", wantStatus: "asset"},
		{name: "site page component under prefix", method: http.MethodGet, path: "/_site/pages/Home/Home.xml", accept: "*/*", wantStatus: "asset"},
		{name: "site vendor under prefix", method: http.MethodGet, path: "/_site/vendor/baseui.min.js", accept: "*/*", wantStatus: "asset"},
		{name: "core SPA main script is not claimed", method: http.MethodGet, path: "/main.js", accept: "*/*", wantStatus: "none"},
		{name: "core SPA service worker is not claimed", method: http.MethodGet, path: "/sw.js", accept: "*/*", wantStatus: "none"},
		{name: "core SPA vendor is not claimed", method: http.MethodGet, path: "/vendor/marked.min.js", accept: "*/*", wantStatus: "none"},
		{name: "sitemap", method: http.MethodGet, path: "/sitemap.xml", accept: "*/*", wantStatus: "asset"},
		{name: "site manifest", method: http.MethodGet, path: "/manifest.json", accept: "*/*", wantStatus: "asset"},
		{name: "app login page", method: http.MethodGet, path: "/login", accept: fiber.MIMETextHTML, wantStatus: "none"},
		{name: "app markdown viewer", method: http.MethodGet, path: "/view-md/docs/readme.md", accept: fiber.MIMETextHTML, wantStatus: "none"},
		{name: "runtime json endpoint", method: http.MethodGet, path: "/website/runtime/resolve", accept: fiber.MIMEApplicationJSON, wantStatus: "none"},
		{name: "web content asset", method: http.MethodGet, path: "/web/content/1", accept: "*/*", wantStatus: "none"},
		{name: "unrelated extension", method: http.MethodGet, path: "/favicon.ico", accept: "*/*", wantStatus: "none"},
		{name: "post request", method: http.MethodPost, path: "/", accept: fiber.MIMETextHTML, wantStatus: "none"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			req.Header.Set(fiber.HeaderAccept, tt.accept)
			resp, err := app.Test(req)
			if err != nil {
				t.Fatalf("app.Test() error = %v", err)
			}
			body := make([]byte, 16)
			n, _ := resp.Body.Read(body)
			got := string(body[:n])
			if got != tt.wantStatus {
				t.Fatalf("classifyWebsiteRuntimeRequest() = %q, want %q", got, tt.wantStatus)
			}
		})
	}
}

// fiber:context-methods migrated

func TestRegisterReservedRuntimePrefixClaimsBareRootPath(t *testing.T) {
	if isWebsiteRuntimeReservedPath("/blog/news") {
		t.Fatal("/blog must not be reserved before a module claims it")
	}
	RegisterReservedRuntimePrefix("blog/")
	RegisterReservedRuntimePrefix("/blog")
	if !isWebsiteRuntimeReservedPath("/blog") || !isWebsiteRuntimeReservedPath("/blog/news/post") {
		t.Fatal("/blog and its children must be reserved after registration")
	}
	if isWebsiteRuntimeReservedPath("/blogging") {
		t.Fatal("prefix match must respect path segments")
	}
	if len(extraReservedRuntimePrefixes) != 1 {
		t.Fatalf("duplicate registration kept %d prefixes", len(extraReservedRuntimePrefixes))
	}
	extraReservedRuntimePrefixes = nil
}

func TestInjectWebsiteRuntimeBase(t *testing.T) {
	got := string(injectWebsiteRuntimeBase([]byte("<!doctype html>\n<html>\n<HEAD>\n<base href=\"/x/\">\n<title>t</title>\n</HEAD><body></body></html>")))
	want := "<!doctype html>\n<html>\n<HEAD>\n<base href=\"/_site/\">\n\n<title>t</title>\n</HEAD><body></body></html>"
	if got != want {
		t.Fatalf("injectWebsiteRuntimeBase() = %q, want %q", got, want)
	}
	if got := string(injectWebsiteRuntimeBase([]byte("<p>x</p>"))); got != "<base href=\"/_site/\">\n<p>x</p>" {
		t.Fatalf("headless document = %q", got)
	}
}

func TestWebsiteProtectedTemplatePath(t *testing.T) {
	root := t.TempDir()
	mk := func(rel string) {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mk("protected/Members.xml")
	mk("pages/Members/Members.js")
	mk("protected/Ghost.xml") // unpublished: no component module
	mk("pages/Home/Home.xml")

	if ok, _ := websiteProtectedTemplatePath(root, "pages/Members/Members.xml"); !ok {
		t.Fatal("deployed protected page must be gated")
	}
	for _, rel := range []string{"pages/Ghost/Ghost.xml", "pages/Home/Home.xml", "pages/Members/Members.js", "main.js"} {
		if ok, _ := websiteProtectedTemplatePath(root, rel); ok {
			t.Fatalf("%s must not be gated", rel)
		}
	}
}

// serveSiteAssetForTest runs serveWebsiteSiteStaticAsset for one request with the tenant env bound,
// the way the shared middleware binds it in production.
func serveSiteAssetForTest(t *testing.T, siteRow orm.RecordMap, path string) int {
	t.Helper()
	app := fiber.New()
	app.Get("/*", func(c fiber.Ctx) error {
		env := orm.NewEnvironmentWithDBName(nil, "tenant_db", 0, nil, false)
		c.SetContext(orm.ContextWithEnvironment(context.Background(), env))
		return serveWebsiteSiteStaticAsset(c, siteRow, c.Path())
	})
	resp, err := app.Test(httptest.NewRequest(http.MethodGet, path, nil))
	if err != nil {
		t.Fatalf("app.Test() error = %v", err)
	}
	return resp.StatusCode
}

func TestServeWebsiteSiteStaticAssetRejectsDeployPathOutsidePublicRoot(t *testing.T) {
	previous := *config.Get()
	t.Cleanup(func() { config.Set(previous) })
	sharedRoot := t.TempDir()
	config.Update(func(c *config.AppConfig) { c.Server.SharedFolder = sharedRoot })

	inside := filepath.Join(sharedRoot, "tenant_db", "public", "deployments", "site-abcdefgh")
	outside := filepath.Join(sharedRoot, "secrets")
	for _, dir := range []string{inside, outside} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "main.js"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if got := serveSiteAssetForTest(t, orm.RecordMap{"deploy_path": inside}, "/_site/main.js"); got != fiber.StatusOK {
		t.Fatalf("confined deploy path: status = %d, want 200", got)
	}
	// A tampered row pointing outside <shared>/<db>/public must never be served.
	if got := serveSiteAssetForTest(t, orm.RecordMap{"deploy_path": outside}, "/_site/main.js"); got != fiber.StatusNotFound {
		t.Fatalf("tampered deploy path: status = %d, want 404", got)
	}
}

func TestWebsiteRuntimeResolvePayloadOmitsDeployPath(t *testing.T) {
	payload := websiteRuntimeResolvePayload(
		orm.RecordMap{"id": int64(1), "name": "Site", "slug": "site", "deploy_path": "/srv/shared/db/public/deployments/x"},
		orm.RecordMap{"id": int64(2), "name": "Home", "route_path": "/", "deploy_path": "/srv/shared/db/public/deployments/x/pages/Home"},
	)
	page := payload["page"].(fiber.Map)
	if page["published"] != true {
		t.Fatalf("published = %v, want true", page["published"])
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "deploy_path") || strings.Contains(string(encoded), "/srv/") {
		t.Fatalf("resolve payload leaks the filesystem path: %s", encoded)
	}
}
