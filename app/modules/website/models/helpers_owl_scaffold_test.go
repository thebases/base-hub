package models

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"git.thebase.vn/base_hub/internal/config"
	kernelmodules "git.thebase.vn/base_hub/internal/modules"
	"git.thebase.vn/base_hub/internal/orm"
)

func TestWebsiteOwlComponentName(t *testing.T) {
	cases := map[string]string{
		"about-us": "AboutUs",
		"Home":     "Home",
		"":         "Website",
		"2025-faq": "Page2025Faq",
	}
	for slug, want := range cases {
		if got := websiteOwlComponentName(slug); got != want {
			t.Fatalf("websiteOwlComponentName(%q) = %q, want %q", slug, got, want)
		}
	}
}

func TestAssignWebsiteComponentNamesDedupesCollisions(t *testing.T) {
	pages := []orm.RecordMap{
		{"id": int64(1), "slug": "about-us"},
		{"id": int64(2), "slug": "about_us"},
	}
	names := assignWebsiteComponentNames(pages)
	if names[1] != "AboutUs" {
		t.Fatalf("names[1] = %q, want AboutUs", names[1])
	}
	if names[2] != "AboutUs2" {
		t.Fatalf("names[2] = %q, want AboutUs2 (deduped)", names[2])
	}
}

func TestExtractWebsiteInlineStyles(t *testing.T) {
	body := `<main><style>.btn{color:red}</style><button class="btn">Go</button></main>`
	cleaned, css := extractWebsiteInlineStyles(body)
	if strings.Contains(cleaned, "<style>") {
		t.Fatalf("extractWebsiteInlineStyles() cleaned body still contains <style>: %q", cleaned)
	}
	if !strings.Contains(css, ".btn{color:red}") {
		t.Fatalf("extractWebsiteInlineStyles() css = %q, want to contain .btn rule", css)
	}
	if !strings.Contains(cleaned, `<button class="btn">Go</button>`) {
		t.Fatalf("extractWebsiteInlineStyles() lost body content: %q", cleaned)
	}
}

func TestNormalizeWebsiteHTMLToXMLFragmentSelfClosesVoidElementsAndEscapesAmp(t *testing.T) {
	fragment, err := normalizeWebsiteHTMLToXMLFragment(`<p>Terms & Conditions<br><img src="a.png"></p>`)
	if err != nil {
		t.Fatalf("normalizeWebsiteHTMLToXMLFragment() error = %v", err)
	}
	for _, want := range []string{"<br/>", `<img src="a.png"/>`, "Terms &amp; Conditions"} {
		if !strings.Contains(fragment, want) {
			t.Fatalf("normalizeWebsiteHTMLToXMLFragment() = %q, want to contain %q", fragment, want)
		}
	}
}

func TestWriteWebsitePageComponentFilesWritesAllThreeScaffoldFiles(t *testing.T) {
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	moduleRoot := t.TempDir()
	if err := os.Chdir(moduleRoot); err != nil {
		t.Fatalf("chdir tempdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWD) })
	kernelmodules.SetEmbeddedFS(nil)
	t.Cleanup(func() { kernelmodules.SetEmbeddedFS(nil) })

	for _, rel := range []string{
		"modules/website/static/bs/css/bootstrap.min.css",
		"modules/website/static/baseui/baseui.min.js",
	} {
		full := filepath.Join(moduleRoot, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %q: %v", full, err)
		}
		if err := os.WriteFile(full, []byte("/* stub */"), 0o644); err != nil {
			t.Fatalf("write vendor stub %q: %v", full, err)
		}
	}

	previous := *config.Get()
	t.Cleanup(func() { config.Set(previous) })
	siteDeployFolder := filepath.Join(t.TempDir(), "site-1")
	if err := os.MkdirAll(siteDeployFolder, 0o755); err != nil {
		t.Fatalf("mkdir site deploy folder: %v", err)
	}

	pageRow := orm.RecordMap{
		"id":            int64(1),
		"name":          "About Us",
		"slug":          "about-us",
		"seo":           map[string]any{"title": "About Us"},
		"rendered_html": `<style>.hero{color:blue}</style><main><h1>About</h1><img src="/a.png"></main>`,
	}
	componentName := websiteOwlComponentName("about-us")
	folder, err := writeWebsitePageComponentFiles(siteDeployFolder, pageRow, componentName)
	if err != nil {
		t.Fatalf("writeWebsitePageComponentFiles() error = %v", err)
	}
	wantFolder := filepath.Join(siteDeployFolder, "pages", "AboutUs")
	if folder != wantFolder {
		t.Fatalf("writeWebsitePageComponentFiles() folder = %q, want %q", folder, wantFolder)
	}
	for _, name := range []string{"AboutUs.js", "AboutUs.xml", "AboutUs.css"} {
		if _, err := os.Stat(filepath.Join(folder, name)); err != nil {
			t.Fatalf("scaffold file %q was not written: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(folder, "index.html")); err == nil {
		t.Fatalf("writeWebsitePageComponentFiles() must not write a per-page index.html")
	}
	componentJS, err := os.ReadFile(filepath.Join(folder, "AboutUs.js"))
	if err != nil {
		t.Fatalf("read AboutUs.js: %v", err)
	}
	for _, want := range []string{"export class AboutUs extends owl.Component", `new URL("./AboutUs.xml", import.meta.url)`} {
		if !strings.Contains(string(componentJS), want) {
			t.Fatalf("AboutUs.js missing %q:\n%s", want, componentJS)
		}
	}
	for _, dest := range []string{"bootstrap.min.css", "baseui.min.js"} {
		if _, err := os.Stat(filepath.Join(siteDeployFolder, "vendor", dest)); err != nil {
			t.Fatalf("vendor asset %q was not copied: %v", dest, err)
		}
	}
}

func TestWriteWebsitePageComponentFilesProtectedPage(t *testing.T) {
	oldWD, _ := os.Getwd()
	moduleRoot := t.TempDir()
	if err := os.Chdir(moduleRoot); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWD) })
	kernelmodules.SetEmbeddedFS(nil)
	t.Cleanup(func() { kernelmodules.SetEmbeddedFS(nil) })
	for _, rel := range []string{"modules/website/static/bs/css/bootstrap.min.css", "modules/website/static/baseui/baseui.min.js"} {
		full := filepath.Join(moduleRoot, rel)
		_ = os.MkdirAll(filepath.Dir(full), 0o755)
		_ = os.WriteFile(full, []byte("/* stub */"), 0o644)
	}
	site := t.TempDir()
	page := orm.RecordMap{"id": int64(1), "name": "Members", websitePublishHTMLKey: "<p>secret</p>", "visibility": "logged_in"}
	folder, err := writeWebsitePageComponentFiles(site, page, "Members")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(folder, "Members.xml")); err == nil {
		t.Fatal("protected template must not be a public file")
	}
	if b, err := os.ReadFile(filepath.Join(site, "protected", "Members.xml")); err != nil || !strings.Contains(string(b), "secret") {
		t.Fatalf("protected template missing: %v", err)
	}
	js, _ := os.ReadFile(filepath.Join(folder, "Members.js"))
	if !strings.Contains(string(js), "Authorization") {
		t.Fatal("protected component must send the session token")
	}
	// Back to public: the protected copy is removed and the template is public again.
	page["visibility"] = "public"
	if _, err := writeWebsitePageComponentFiles(site, page, "Members"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(site, "protected", "Members.xml")); err == nil {
		t.Fatal("protected copy must be removed when the page becomes public")
	}
	if _, err := os.Stat(filepath.Join(folder, "Members.xml")); err != nil {
		t.Fatal("public template missing")
	}
}
