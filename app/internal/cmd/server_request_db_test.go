package cmd

import (
	"context"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"git.thebase.vn/base_hub/internal/config"
	kernelmodules "git.thebase.vn/base_hub/internal/modules"
	"git.thebase.vn/base_hub/internal/orm"
	testutil "git.thebase.vn/base_hub/internal/test"
	"github.com/gofiber/fiber/v3"
)

// TestBindRequestDatabaseAllowlist covers SEC-T-8: the template and
// unregistered/stopped tenants get a generic 400 before any pool is opened;
// the home DB and a registered running tenant bind normally.
func TestBindRequestDatabaseAllowlist(t *testing.T) {
	cfg := testutil.EnvPostgresConfig()
	pool, err := orm.NewBunDB(cfg)
	if err != nil {
		t.Skipf("test postgres unavailable: %v", err)
	}
	if err := pool.DB.PingContext(context.Background()); err != nil {
		_ = pool.Close()
		t.Skipf("test postgres unavailable: %v", err)
	}
	homeName := cfg.Name
	orm.SetNamedBunDB(homeName, pool)
	orm.SetNamedBunDB("tenant_running", pool)
	previous := *config.Get()
	t.Cleanup(func() {
		config.Set(previous)
		kernelmodules.RegisterRequestDBValidator(nil)
		orm.SetNamedBunDB("tenant_running", nil)
		orm.SetNamedBunDB(homeName, nil)
		_ = pool.Close()
	})
	config.Update(func(c *config.AppConfig) {
		c.PostgresqlDb.HomeDBName = homeName
		c.PostgresqlDb.TemplateDBName = "tpl_db"
	})
	// Stub for the database module's registry validator.
	kernelmodules.RegisterRequestDBValidator(func(_ context.Context, name string) (bool, error) {
		return name == "tenant_running", nil
	})

	app := fiber.New()
	app.Use(bindRequestDatabase)
	app.Get("/probe", func(c fiber.Ctx) error {
		name, _ := c.Locals(orm.ContextKeyDatabaseName).(string)
		return c.SendString(name)
	})

	cases := []struct {
		name     string
		target   string
		header   string
		cookie   string
		wantCode int
		wantBody string
	}{
		{"template via header", "/probe", "tpl_db", "", fiber.StatusBadRequest, "unknown database"},
		{"template via query", "/probe?db=tpl_db", "", "", fiber.StatusBadRequest, "unknown database"},
		{"stopped tenant via locked cookie", "/probe", "", "tenant_stopped", fiber.StatusBadRequest, "unknown database"},
		{"unregistered tenant", "/probe", "nope_db", "", fiber.StatusBadRequest, "unknown database"},
		{"home db", "/probe", homeName, "", fiber.StatusOK, homeName},
		{"registered running tenant", "/probe", "tenant_running", "", fiber.StatusOK, "tenant_running"},
		{"no selector", "/probe", "", "", fiber.StatusOK, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", tc.target, nil)
			if tc.header != "" {
				req.Header.Set("X-Database-Name", tc.header)
			}
			if tc.cookie != "" {
				req.Header.Set("Cookie", requestDBNameCookie+"="+tc.cookie+"; "+requestDBLockedCookie+"=1")
			}
			resp, err := app.Test(req)
			if err != nil {
				t.Fatalf("app.Test: %v", err)
			}
			body, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != tc.wantCode {
				t.Fatalf("status = %d, want %d (body %q)", resp.StatusCode, tc.wantCode, body)
			}
			if strings.TrimSpace(string(body)) != tc.wantBody {
				t.Fatalf("body = %q, want %q", body, tc.wantBody)
			}
		})
	}
}
