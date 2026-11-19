package cmd

// File overview:
// - Responsibility: centralizes embedded asset registration and runtime asset-source resolution so server bootstrap can serve one consistent filesystem API.
// - Key symbols: `EmbeddedAssets`, `SetEmbeddedAssets`, `resolveUserFEStaticAssets`.
// - Pattern: Keeps compile-time embed wiring separate from HTTP startup by resolving one static asset source up front and reusing it across middleware and command paths.

import (
	"errors"
	"fmt"
	"html"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"git.thebase.vn/base_hub/internal/routes"
	"git.thebase.vn/base_hub/internal/services/log"
	"github.com/gofiber/fiber/v3"
)

// htmlCommentPattern matches "<!-- ... -->" blocks, including the malformed
// "<!--- ... --->" variant some editors emit — both start with "<!--" and
// non-greedy matching to the first "-->" strips either form the same way.
var htmlCommentPattern = regexp.MustCompile(`(?s)<!--.*?-->`)

// EmbeddedAssets groups the package-level embedded filesystems so bootstrap can
// register one bundle instead of scattering separate setters across the entrypoint.
type EmbeddedAssets struct {
	StaticFrontend fs.FS
	Modules        fs.FS
}

// staticAssetSource captures the resolved static asset tree once at startup so
// request handlers do not need to repeat embed-versus-disk branching.
type staticAssetSource struct {
	FS     fs.FS
	HTTPFS http.FileSystem
	Source string
}

var embeddedAssets EmbeddedAssets

// SetEmbeddedAssets stores the embedded asset bundle handled by this package.
func SetEmbeddedAssets(bundle EmbeddedAssets) {
	embeddedAssets = bundle
}

// resolveUserFEStaticAssets resolves the end-user SPA filesystem served at
// both "/" and "/web" (see shouldServeWebSPAIndex). app/frontend is
// the source — a static, no-bundler SPA (plain ES modules loaded via its own
// import map), so there is no separate build output to point at and the disk
// fallback reads the source tree directly.
func resolveUserFEStaticAssets() (staticAssetSource, error) {
	if embeddedAssets.StaticFrontend != nil {
		// withModTime: embed.FS has no mtime, which made every revalidation a
		// 304 and kept stale .js modules in browsers after a deploy.
		fsys := withModTime(embeddedAssets.StaticFrontend)
		return staticAssetSource{
			FS:     fsys,
			HTTPFS: http.FS(fsys),
			Source: "embedded frontend",
		}, nil
	}

	for _, candidate := range []struct {
		dir    string
		source string
	}{
		{
			dir:    filepath.Clean("./frontend"),
			source: "disk fallback ./frontend (app/frontend)",
		},
	} {
		if _, err := os.Stat(filepath.Join(candidate.dir, "index.html")); err == nil {
			fsys := os.DirFS(candidate.dir)
			return staticAssetSource{
				FS:     fsys,
				HTTPFS: http.FS(fsys),
				Source: candidate.source,
			}, nil
		} else if err != nil && !os.IsNotExist(err) {
			log.Errorf("stat %s/index.html: %v", candidate.dir, err)
			return staticAssetSource{}, fmt.Errorf("stat %s/index.html: %w", candidate.dir, err)
		}
	}

	log.Errorf("no user-fe static assets available")
	return staticAssetSource{}, fmt.Errorf("no user-fe static assets available")
}

// sendStaticAsset streams a file from the resolved static filesystem so
// fallback pages and SPA index responses behave the same for embedded and disk assets.
func sendStaticAsset(c fiber.Ctx, root fs.FS, name string, status int) error {
	cleanName := strings.TrimLeft(pathCleanAssetName(name), "/")
	file, err := root.Open(cleanName)
	if err != nil {
		if errorsIsNotExist(err) {
			return fiber.ErrNotFound
		}
		log.Errorf("open static asset %s: %v", cleanName, err)
		return fmt.Errorf("open static asset %s: %w", cleanName, err)
	}

	stat, err := file.Stat()
	if err != nil {
		_ = file.Close()
		log.Errorf("stat static asset %s: %v", cleanName, err)
		return fmt.Errorf("stat static asset %s: %w", cleanName, err)
	}

	if ext := filepath.Ext(cleanName); ext == ".html" || ext == ".xml" {
		c.Type(strings.TrimPrefix(ext, "."), "utf-8")
		c.Status(status)
		// HTML/XML is buffered (not streamed) so source comments can be
		// stripped on render without touching the on-disk file the
		// dev-facing comments document.
		body, err := io.ReadAll(file.(io.Reader))
		_ = file.Close()
		if err != nil {
			log.Errorf("read static asset %s: %v", cleanName, err)
			return fmt.Errorf("read static asset %s: %w", cleanName, err)
		}
		body = htmlCommentPattern.ReplaceAll(body, nil)
		if cleanName == "index.html" {
			body = injectThemeColor(body, routes.PWAThemeColor(c.Context()))
		}
		c.Response().SetBody(body)
		return nil
	}

	if ext := filepath.Ext(cleanName); ext != "" {
		c.Type(ext)
	}
	c.Status(status)
	c.Response().SetBodyStream(file.(io.Reader), int(stat.Size()))
	return nil
}

var themeColorMetaPattern = regexp.MustCompile(`(<meta\s+name="theme-color"\s+content=")[^"]*(")`)

// injectThemeColor swaps the static theme-color meta in the SPA shell for the pwa.theme_color
// config value; the value is HTML-escaped because it is operator-editable data.
func injectThemeColor(body []byte, color string) []byte {
	repl := []byte("${1}" + strings.ReplaceAll(html.EscapeString(color), "$", "$$") + "${2}")
	return themeColorMetaPattern.ReplaceAll(body, repl)
}

// serveIndexAtRoot serves the SPA shell for "/" and "/index.html" through sendStaticAsset so the
// theme color is injected; static.New would otherwise stream the on-disk file verbatim.
func serveIndexAtRoot(fsys fs.FS) fiber.Handler {
	return func(c fiber.Ctx) error {
		if c.Method() != fiber.MethodGet && c.Method() != fiber.MethodHead {
			return c.Next()
		}
		if p := c.Path(); p != "/" && p != "/index.html" {
			return c.Next()
		}
		return sendStaticAsset(c, fsys, "index.html", fiber.StatusOK)
	}
}

// serveWithoutXMLComments intercepts ".xml" GET requests ahead of the plain
// `static.New` file server for a mount so comments strip on render without
// static.New's own streaming path (which serves the on-disk bytes verbatim).
// Scoped to ".xml" only — .js/.css/etc. keep streaming straight through
// static.New, matching sendStaticAsset's own HTML/XML-only carve-out.
func serveWithoutXMLComments(fsys fs.FS, mountPrefix string) fiber.Handler {
	return func(c fiber.Ctx) error {
		if c.Method() != fiber.MethodGet || filepath.Ext(c.Path()) != ".xml" {
			return c.Next()
		}
		rel := strings.TrimPrefix(c.Path(), mountPrefix)
		return sendStaticAsset(c, fsys, rel, fiber.StatusOK)
	}
}

// pathCleanAssetName normalizes asset lookups so callers can pass friendly names
// like `index.html` or `/403.html` without exposing traversal outside the asset root.
func pathCleanAssetName(name string) string {
	cleaned := filepath.ToSlash(filepath.Clean("/" + strings.TrimSpace(name)))
	return strings.TrimPrefix(cleaned, "/")
}

// errorsIsNotExist smooths over `os` and `io/fs` miss variants so static asset
// lookup can treat embedded and disk filesystems with the same not-found path.
// errors.Is unwraps *os.PathError and matches fs.ErrNotExist directly, covering
// both the wrapped `os` case and the raw sentinel from embedded filesystems.
func errorsIsNotExist(err error) bool {
	return errors.Is(err, fs.ErrNotExist)
}
