package routes

// File overview:
// - Responsibility: verifies the markdown routes take the tenant DB from the
//   request-bound environment (cookie-bound sessions), not only ?db=/header.

import (
	"io"
	"net/http/httptest"
	"testing"

	"git.thebase.vn/base_hub/internal/orm"
	"github.com/gofiber/fiber/v3"
)

func TestResolveMarkdownRequestStateUsesBoundEnvironmentDB(t *testing.T) {
	options := MarkdownRouteOptions{
		HomeDBName:      "home",
		PublicRootForDB: func(dbName string) string { return "/docs/" + dbName },
	}
	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		// What bindRequestDatabase does for a request pinned by the db cookie.
		if bound := c.Get("X-Test-Bound-DB"); bound != "" {
			env := orm.NewEnvironmentWithDBName(nil, bound, 0, map[string]any{orm.ContextKeyDatabaseName: bound}, false)
			c.SetContext(orm.ContextWithEnvironment(c.Context(), env))
		}
		return c.Next()
	})
	app.Get("/state", func(c fiber.Ctx) error {
		root, includeBase, err := resolveMarkdownRequestState(c, options)
		if err != nil {
			return err
		}
		if includeBase {
			root += " +base"
		}
		return c.SendString(root)
	})

	cases := []struct {
		name  string
		url   string
		bound string
		want  string
	}{
		{name: "cookie-bound tenant", url: "/state", bound: "tenant_a", want: "/docs/tenant_a"},
		{name: "explicit selector", url: "/state?db=tenant_b", bound: "tenant_b", want: "/docs/tenant_b"},
	}
	for _, tc := range cases {
		req := httptest.NewRequest("GET", tc.url, nil)
		req.Header.Set("X-Test-Bound-DB", tc.bound)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("%s: request failed: %v", tc.name, err)
		}
		body, _ := io.ReadAll(resp.Body)
		if string(body) != tc.want {
			t.Fatalf("%s: root = %q, want %q", tc.name, body, tc.want)
		}
	}
}
