package cmd

// File overview:
// - Responsibility: verifies CLI bootstrap, module loading, and server startup wiring for server.
// - Key symbols: focused Test cases plus file-local fixtures and assertion helpers.
// - Pattern: Locks down startup expectations so changes to command bootstrap do not break operational flows or initialization order.

import (
	"context"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/static"

	kernelmodules "git.thebase.vn/base_hub/internal/modules"
)

func TestShouldServeWebSPAIndex(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		method string
		target string
		accept string
		want   bool
	}{
		{
			name:   "model route with dotted name falls back to spa",
			method: fiber.MethodGet,
			target: "/web/tbl.country.state",
			accept: fiber.MIMETextHTML,
			want:   true,
		},
		{
			name:   "record route falls back to spa",
			method: fiber.MethodGet,
			target: "/web/base.users/42",
			accept: fiber.MIMETextHTML,
			want:   true,
		},
		{
			name:   "image endpoint stays on backend",
			method: fiber.MethodGet,
			target: "/web/image/res.users/1/avatar_128",
			accept: fiber.MIMETextHTML,
			want:   false,
		},
		{
			name:   "static bundle asset stays on filesystem handler",
			method: fiber.MethodGet,
			target: "/web/assets/index-abcd1234.js",
			accept: "*/*",
			want:   false,
		},
		{
			name:   "json request does not fall back to spa",
			method: fiber.MethodGet,
			target: "/web/tbl.country.state",
			accept: fiber.MIMEApplicationJSON,
			want:   false,
		},
		{
			name:   "post request does not fall back to spa",
			method: fiber.MethodPost,
			target: "/web/tbl.country.state",
			accept: fiber.MIMETextHTML,
			want:   false,
		},
	}

	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		if shouldServeWebSPAIndex(c) {
			return c.SendStatus(fiber.StatusOK)
		}
		return c.SendStatus(fiber.StatusNotFound)
	})

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequest(tc.method, tc.target, nil)
			if tc.accept != "" {
				req.Header.Set(fiber.HeaderAccept, tc.accept)
			}

			resp, err := app.Test(req, fiber.TestConfig{Timeout: 0, FailOnTimeout: false})
			if err != nil {
				t.Fatalf("app.Test() error = %v", err)
			}

			got := resp.StatusCode == fiber.StatusOK
			if got != tc.want {
				t.Fatalf("shouldServeWebSPAIndex(%s %s, accept=%q) = %v, want %v", tc.method, tc.target, tc.accept, got, tc.want)
			}
		})
	}
}

func TestWebModelRefreshServesSPAIndex(t *testing.T) {
	t.Parallel()

	staticDir := t.TempDir()
	indexPath := filepath.Join(staticDir, "index.html")
	if err := os.WriteFile(indexPath, []byte("<html><body>spa index</body></html>"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(%s) error = %v", indexPath, err)
	}

	app := fiber.New()

	app.Use("/", static.New("", static.Config{
		FS:         os.DirFS(staticDir),
		Browse:     false,
		IndexNames: []string{"index.html"},
		MaxAge:     3600,
		Next: func(c fiber.Ctx) bool {
			return shouldServeWebSPAIndex(c)
		},
	}))
	app.Use("/web", func(c fiber.Ctx) error {
		if !shouldServeWebSPAIndex(c) {
			return c.Next()
		}
		return c.SendFile(indexPath)
	})

	req := httptest.NewRequest(fiber.MethodGet, "/web/tbl.country.state", nil)
	req.Header.Set(fiber.HeaderAccept, fiber.MIMETextHTML)

	resp, err := app.Test(req, fiber.TestConfig{Timeout: 0, FailOnTimeout: false})
	if err != nil {
		t.Fatalf("app.Test() error = %v", err)
	}
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, fiber.StatusOK)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadFrom(response body) error = %v", err)
	}
	if !strings.Contains(string(body), "spa index") {
		t.Fatalf("response body = %q, want SPA index", string(body))
	}
}

// TestServeModuleStaticResourceReadsFromModuleTree locks in that module
// static assets resolve from the module resource tree (<name>/static/**) and
// are served with ETag revalidation.
func TestServeModuleStaticResourceReadsFromModuleTree(t *testing.T) {
	modulesDir := t.TempDir()
	assetDir := filepath.Join(modulesDir, "knowledge", "static", "routes")
	if err := os.MkdirAll(assetDir, 0o755); err != nil {
		t.Fatalf("os.MkdirAll(%s) error = %v", assetDir, err)
	}
	assetPath := filepath.Join(assetDir, "markdown-public-route.js")
	if err := os.WriteFile(assetPath, []byte("export default function(){}"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(%s) error = %v", assetPath, err)
	}

	previous := kernelmodules.EmbeddedFS()
	kernelmodules.SetEmbeddedFS(os.DirFS(modulesDir))
	t.Cleanup(func() { kernelmodules.SetEmbeddedFS(previous) })

	app := fiber.New()
	app.Get("/:module/static/*", serveModuleStaticResource)

	req := httptest.NewRequest(fiber.MethodGet, "/knowledge/static/routes/markdown-public-route.js", nil)
	resp, err := app.Test(req, fiber.TestConfig{Timeout: 0, FailOnTimeout: false})
	if err != nil {
		t.Fatalf("app.Test() error = %v", err)
	}
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, fiber.StatusOK)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadFrom(response body) error = %v", err)
	}
	if !strings.Contains(string(body), "export default function") {
		t.Fatalf("response body = %q, want module asset contents", string(body))
	}
	// Module assets keep their URL across deploys, so they must always be revalidated by ETag.
	etag := resp.Header.Get(fiber.HeaderETag)
	if etag == "" || resp.Header.Get(fiber.HeaderCacheControl) != "no-cache" {
		t.Fatalf("cache headers = %q / %q, want no-cache with an ETag", resp.Header.Get(fiber.HeaderCacheControl), etag)
	}
	req = httptest.NewRequest(fiber.MethodGet, "/knowledge/static/routes/markdown-public-route.js", nil)
	req.Header.Set(fiber.HeaderIfNoneMatch, etag)
	if resp, err = app.Test(req, fiber.TestConfig{Timeout: 0, FailOnTimeout: false}); err != nil || resp.StatusCode != fiber.StatusNotModified {
		t.Fatalf("revalidation = %v, %v; want 304", resp, err)
	}
	// A changed file (new deploy) must miss the old ETag and come back in full.
	if err := os.WriteFile(assetPath, []byte("export default function(){ return 2 }"), 0o644); err != nil {
		t.Fatal(err)
	}
	req = httptest.NewRequest(fiber.MethodGet, "/knowledge/static/routes/markdown-public-route.js", nil)
	req.Header.Set(fiber.HeaderIfNoneMatch, etag)
	if resp, err = app.Test(req, fiber.TestConfig{Timeout: 0, FailOnTimeout: false}); err != nil || resp.StatusCode != fiber.StatusOK {
		t.Fatalf("changed asset = %v, %v; want 200", resp, err)
	}
}

// TestServeModuleStaticResourceFollowsInstallState locks in the module static-folder lifecycle:
// files are reachable while the module is installed, 404 once it is uninstalled (same files, same
// URL), reachable again after reinstall, and a module without a static folder never serves anything.
func TestServeModuleStaticResourceFollowsInstallState(t *testing.T) {
	modulesDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(modulesDir, "demo", "static", "css"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(modulesDir, "nostatic"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(modulesDir, "demo", "static", "css", "a.css"), []byte("a{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	previousFS := kernelmodules.EmbeddedFS()
	kernelmodules.SetEmbeddedFS(os.DirFS(modulesDir))
	previousCheck := moduleInstalledForStatic
	t.Cleanup(func() {
		kernelmodules.SetEmbeddedFS(previousFS)
		moduleInstalledForStatic = previousCheck
	})

	installed := map[string]bool{"demo": true, "nostatic": true}
	moduleInstalledForStatic = func(_ context.Context, name string) (bool, error) { return installed[name], nil }

	app := fiber.New()
	app.Get("/:module/static/*", serveModuleStaticResource)
	status := func(target string) int {
		t.Helper()
		resp, err := app.Test(httptest.NewRequest(fiber.MethodGet, target, nil), fiber.TestConfig{Timeout: 0, FailOnTimeout: false})
		if err != nil {
			t.Fatal(err)
		}
		return resp.StatusCode
	}

	if got := status("/demo/static/css/a.css"); got != fiber.StatusOK {
		t.Fatalf("installed module: status = %d, want 200", got)
	}
	installed["demo"] = false
	if got := status("/demo/static/css/a.css"); got != fiber.StatusNotFound {
		t.Fatalf("uninstalled module: status = %d, want 404", got)
	}
	installed["demo"] = true
	if got := status("/demo/static/css/a.css"); got != fiber.StatusOK {
		t.Fatalf("reinstalled module: status = %d, want 200", got)
	}
	if got := status("/nostatic/static/x.css"); got != fiber.StatusNotFound {
		t.Fatalf("module without static folder: status = %d, want 404", got)
	}
	if got := status("/demo/static/%2e%2e/%2e%2e/secret"); got != fiber.StatusNotFound {
		t.Fatalf("traversal: status = %d, want 404", got)
	}
}
