package models

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"git.thebase.vn/base_hub/internal/config"
	"git.thebase.vn/base_hub/internal/orm"
)

func TestEnsureWebsiteDeploymentFolderCreatesTenantDeploymentPath(t *testing.T) {
	previous := *config.Get()
	t.Cleanup(func() {
		config.Set(previous)
	})

	sharedRoot := t.TempDir()
	config.Update(func(c *config.AppConfig) { c.Server.SharedFolder = sharedRoot })
	ctx := orm.ContextWithEnvironment(context.Background(), orm.NewEnvironmentWithDBName(nil, "tenant_db", 0, nil, false))

	got, err := ensureWebsiteDeploymentFolder(ctx, "site-123")
	if err != nil {
		t.Fatalf("ensureWebsiteDeploymentFolder() error = %v", err)
	}
	want := filepath.Join(sharedRoot, "tenant_db", "public", "deployments", "site-123")
	if got != want {
		t.Fatalf("ensureWebsiteDeploymentFolder() = %q, want %q", got, want)
	}
	info, err := os.Stat(want)
	if err != nil {
		t.Fatalf("deployment folder was not created: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("deployment path %q is not a directory", want)
	}
}

func TestDeleteWebsiteStaticFileRefusesOutsidePublicRoot(t *testing.T) {
	previous := *config.Get()
	t.Cleanup(func() {
		config.Set(previous)
	})

	sharedRoot := t.TempDir()
	config.Update(func(c *config.AppConfig) { c.Server.SharedFolder = sharedRoot })
	ctx := orm.ContextWithEnvironment(context.Background(), orm.NewEnvironmentWithDBName(nil, "tenant_db", 0, nil, false))
	outside := filepath.Join(sharedRoot, "tenant_db", "other", "file.html")
	if err := os.MkdirAll(filepath.Dir(outside), 0o755); err != nil {
		t.Fatalf("create outside folder: %v", err)
	}
	if err := os.WriteFile(outside, []byte("keep"), 0o644); err != nil {
		t.Fatalf("write outside file: %v", err)
	}

	if err := deleteWebsiteStaticFile(ctx, outside); err == nil {
		t.Fatalf("expected delete outside public root to fail")
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("outside file should not be removed: %v", err)
	}
}

func TestDeleteWebsiteStaticFileRemovesFileUnderPublicRoot(t *testing.T) {
	previous := *config.Get()
	t.Cleanup(func() {
		config.Set(previous)
	})

	sharedRoot := t.TempDir()
	config.Update(func(c *config.AppConfig) { c.Server.SharedFolder = sharedRoot })
	ctx := orm.ContextWithEnvironment(context.Background(), orm.NewEnvironmentWithDBName(nil, "tenant_db", 0, nil, false))
	deployed := filepath.Join(sharedRoot, "tenant_db", "public", "deployments", "site-1", "abcdefgh.html")
	if err := os.MkdirAll(filepath.Dir(deployed), 0o755); err != nil {
		t.Fatalf("create deploy folder: %v", err)
	}
	if err := os.WriteFile(deployed, []byte("content"), 0o644); err != nil {
		t.Fatalf("write deployed file: %v", err)
	}

	if err := deleteWebsiteStaticFile(ctx, deployed); err != nil {
		t.Fatalf("deleteWebsiteStaticFile() error = %v", err)
	}
	if _, err := os.Stat(deployed); !os.IsNotExist(err) {
		t.Fatalf("deployed file should be removed, stat err=%v", err)
	}
}

func TestDeleteWebsiteStaticFileRemovesOwlScaffoldFolderUnderPublicRoot(t *testing.T) {
	previous := *config.Get()
	t.Cleanup(func() {
		config.Set(previous)
	})

	sharedRoot := t.TempDir()
	config.Update(func(c *config.AppConfig) { c.Server.SharedFolder = sharedRoot })
	ctx := orm.ContextWithEnvironment(context.Background(), orm.NewEnvironmentWithDBName(nil, "tenant_db", 0, nil, false))
	pageFolder := filepath.Join(sharedRoot, "tenant_db", "public", "deployments", "site-1", "pages", "home-abcdefgh")
	if err := os.MkdirAll(pageFolder, 0o755); err != nil {
		t.Fatalf("create page scaffold folder: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pageFolder, "index.html"), []byte("<html></html>"), 0o644); err != nil {
		t.Fatalf("write index.html: %v", err)
	}

	if err := deleteWebsiteStaticFile(ctx, pageFolder); err != nil {
		t.Fatalf("deleteWebsiteStaticFile() error = %v", err)
	}
	if _, err := os.Stat(pageFolder); !os.IsNotExist(err) {
		t.Fatalf("deployed folder should be removed, stat err=%v", err)
	}
}

func TestStripWebsiteServerManagedFieldsDropsClientValuesAndAliases(t *testing.T) {
	// The payload a client could send to page create to point publish/serve at a system folder.
	values := orm.RecordMap{
		"name":            "About",
		"deploy_path":     "/etc",
		"DeployPath":      "/etc",
		"deployPath":      "/etc",
		"revision":        int64(99),
		"publish_pending": true,
		"body_html":       "<p>x</p>",
	}
	stripWebsiteServerManagedFields(values, "deploy_path", "revision", "publish_pending")
	if len(values) != 2 || values["name"] != "About" || values["body_html"] != "<p>x</p>" {
		t.Fatalf("unexpected payload after strip: %#v", values)
	}
}

func TestConfinedWebsiteDeployPath(t *testing.T) {
	previous := *config.Get()
	t.Cleanup(func() { config.Set(previous) })
	sharedRoot := t.TempDir()
	config.Update(func(c *config.AppConfig) { c.Server.SharedFolder = sharedRoot })

	public := filepath.Join(sharedRoot, "tenant_db", "public")
	inside := filepath.Join(public, "deployments", "site-abcdefgh")
	if err := os.MkdirAll(inside, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	link := filepath.Join(public, "deployments", "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}

	if got, ok := ConfinedWebsiteDeployPath("tenant_db", inside+"/"); !ok || got != inside {
		t.Fatalf("inside path: got (%q, %v)", got, ok)
	}
	// Not yet created folders under the root are allowed (publish creates them).
	if _, ok := ConfinedWebsiteDeployPath("tenant_db", filepath.Join(public, "deployments", "new")); !ok {
		t.Fatalf("missing folder under root must be allowed")
	}
	for _, bad := range []string{
		"/etc",
		public,
		filepath.Join(public, "..", "private"),
		filepath.Join(sharedRoot, "other_db", "public", "deployments", "x"),
		"deployments/relative",
		link,
		"",
	} {
		if got, ok := ConfinedWebsiteDeployPath("tenant_db", bad); ok {
			t.Errorf("ConfinedWebsiteDeployPath(%q) = %q, want rejected", bad, got)
		}
	}
	if _, ok := ConfinedWebsiteDeployPath("", inside); ok {
		t.Fatalf("empty db name must be rejected")
	}
}

// A relative shared folder (the "./data" default) must still accept the folders publish creates:
// install published the seeded site, then failed with "deploy_path is outside the tenant public folder".
func TestConfinedWebsiteDeployPathRelativeSharedFolder(t *testing.T) {
	previous := *config.Get()
	t.Cleanup(func() { config.Set(previous) })
	workDir := t.TempDir()
	t.Chdir(workDir)
	config.Update(func(c *config.AppConfig) { c.Server.SharedFolder = "./data" })

	ctx := orm.ContextWithEnvironment(context.Background(), orm.NewEnvironmentWithDBName(nil, "tenant_db", 0, nil, false))
	folder, err := ensureWebsiteDeploymentFolder(ctx, "site-abcdefgh")
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(folder) {
		t.Fatalf("deploy folder %q must be stored absolute", folder)
	}
	if got, ok := ConfinedWebsiteDeployPath("tenant_db", folder); !ok || got != folder {
		t.Fatalf("freshly created folder: got (%q, %v)", got, ok)
	}
	// Rows saved by the old code hold the relative form; they resolve to the same folder.
	if got, ok := ConfinedWebsiteDeployPath("tenant_db", "data/tenant_db/public/deployments/site-abcdefgh"); !ok || got != folder {
		t.Fatalf("legacy relative path: got (%q, %v)", got, ok)
	}
	if _, ok := ConfinedWebsiteDeployPath("tenant_db", "data/tenant_db/private"); ok {
		t.Fatalf("relative path outside the public root must be rejected")
	}
}
