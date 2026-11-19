//go:build conformance

package conformance

// File overview:
// - Responsibility: CONF-03 — multi-DB binding: header-based selection,
//   cookie-based selection through the production bindRequestDatabase
//   middleware (exported as cmd.BindRequestDatabase), the unknown-DB 400/403
//   paths, and the home-DB-only visibility rule for base.database menus.
// - Pattern: a second logical DB name is registered against the same physical
//   pool, so both "home" and "service" behavior run on one PostgreSQL.

import (
	"context"
	"net/http"
	"testing"
	"testing/fstest"

	"git.thebase.vn/base_hub/internal/cmd"
	"git.thebase.vn/base_hub/internal/models"
	"git.thebase.vn/base_hub/internal/orm"
	testutil "git.thebase.vn/base_hub/internal/test"
	"github.com/gofiber/fiber/v3"
)

// confAltDBName is the second logical database registered for the suite; it
// points at the same physical pool, which is enough for binding semantics.
const confAltDBName = "conf_alt_db"

// registerAltDB registers the alternate logical name against the suite pool.
func registerAltDB(t *testing.T, suite *confSuite) {
	t.Helper()
	alt := orm.ResolveRegisteredDatabase(suite.dbName)
	if alt == nil {
		t.Fatalf("suite database %q is not registered", suite.dbName)
	}
	orm.SetNamedBunDB(confAltDBName, alt)
	t.Cleanup(func() { orm.SetNamedBunDB(confAltDBName, nil) })
}

// TestConf03HeaderSelection covers X-Database-Name resolution on the RPC
// surface: a registered name binds, an unknown name is a 400.
func TestConf03HeaderSelection(t *testing.T) {
	suite := newConfSuite(t)
	registerAltDB(t, suite)

	seed := asMap(t, suite.rpcResult(t, rpcPath(confPartnerModelName, "create"),
		map[string]any{"values": map[string]any{"name": "conf03 header row"}}, authAPIKey()))
	seedID := recordID(t, seed)
	t.Cleanup(func() {
		_, _ = suite.db.ExecContext(context.Background(), "DELETE FROM conf_partner WHERE id = ?", seedID)
	})

	// Registered alternate name binds and reads the same physical rows. The
	// suite's sudo bearer is minted scoped to suite.dbName (RBAC/OAuth backend
	// plan §12.5: an access token is only valid for the DB it was issued
	// against), so exercising a different logical name needs its own token —
	// mint one against confAltDBName for the same already-registered
	// sys.oauth_client row (same physical Postgres pool under both names).
	altToken := issueConfSudoBearerForDB(t, confAltDBName)
	status, payload := suite.rpcCall(t, rpcPath(confPartnerModelName, "read", seedID),
		map[string]any{"fields": []string{"id", "name"}}, authBearer(altToken),
		map[string]string{"X-Database-Name": confAltDBName})
	if status != http.StatusOK {
		t.Fatalf("alt-db read status = %d payload=%v", status, payload)
	}

	// Unknown database names must be rejected with 400, never fall back.
	status, _ = suite.rpcCall(t, rpcPath(confPartnerModelName, "read", seedID),
		map[string]any{"fields": []string{"id"}}, authAPIKey(),
		map[string]string{"X-Database-Name": "conf_unknown_db"})
	if status != http.StatusBadRequest {
		t.Fatalf("unknown-db read status = %d, want %d", status, http.StatusBadRequest)
	}
}

// TestConf03CookieBinding drives the production DB-binding middleware:
// locked cookies pin the environment DB; an unknown cookie DB clears the
// cookies and serves the 403 page.
func TestConf03CookieBinding(t *testing.T) {
	suite := newConfSuite(t)
	registerAltDB(t, suite)

	// The middleware's rejection path streams public/403.html, which resolves
	// through the embedded-assets bundle in packaged builds; provide one so
	// the test does not depend on repo-relative disk layout.
	cmd.SetEmbeddedAssets(cmd.EmbeddedAssets{StaticFrontend: fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<html>conf index</html>")},
		"403.html":   &fstest.MapFile{Data: []byte("<html>conf forbidden</html>")},
	}})
	t.Cleanup(func() { cmd.SetEmbeddedAssets(cmd.EmbeddedAssets{}) })

	app := fiber.New()
	app.Use(cmd.BindRequestDatabase)
	app.Get("/probe", func(c fiber.Ctx) error {
		env := orm.EnvironmentFromContext(c.Context())
		if env == nil {
			return c.JSON(fiber.Map{"db": ""})
		}
		return c.JSON(fiber.Map{"db": env.DBName()})
	})

	cases := []struct {
		name     string
		cookies  map[string]string
		wantCode int
		wantDB   string
	}{
		{
			name:     "no cookies leaves request unbound",
			cookies:  nil,
			wantCode: http.StatusOK,
			wantDB:   "",
		},
		{
			name:     "locked cookie pins the environment database",
			cookies:  map[string]string{"basehub_db_name": confAltDBName, "basehub_db_locked": "1"},
			wantCode: http.StatusOK,
			wantDB:   confAltDBName,
		},
		{
			name:     "cookie without lock flag is ignored",
			cookies:  map[string]string{"basehub_db_name": confAltDBName},
			wantCode: http.StatusOK,
			wantDB:   "",
		},
		{
			name:     "unknown cookie database serves the 403 page",
			cookies:  map[string]string{"basehub_db_name": "conf_unknown_db", "basehub_db_locked": "1"},
			wantCode: http.StatusForbidden,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			req := testutil.NewJSONRequest(t, http.MethodGet, "/probe", "")
			for name, value := range tc.cookies {
				req.AddCookie(&http.Cookie{Name: name, Value: value})
			}
			resp, err := app.Test(req, fiber.TestConfig{Timeout: 0, FailOnTimeout: false})
			if err != nil {
				t.Fatalf("probe request: %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != tc.wantCode {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tc.wantCode)
			}
			if tc.wantCode == http.StatusOK {
				payload := testutil.DecodeJSON[map[string]any](t, resp.Body)
				if got := payload["db"]; got != tc.wantDB {
					t.Fatalf("bound db = %v, want %q", got, tc.wantDB)
				}
			}
			if tc.wantCode == http.StatusForbidden {
				// Rejection must also clear both binding cookies so a broken
				// SPA state cannot wedge every subsequent request.
				cleared := 0
				for _, cookie := range resp.Cookies() {
					if (cookie.Name == "basehub_db_name" || cookie.Name == "basehub_db_locked") && cookie.MaxAge < 0 {
						cleared++
					}
				}
				if cleared != 2 {
					t.Fatalf("expected both binding cookies cleared, got %d", cleared)
				}
			}
		})
	}
}

// TestConf03BaseDatabaseMenuVisibility asserts the control-plane rule: the
// base.database menu entry is served on the home DB and hidden from every
// other database (SRS-F-002 / CONF-03).
func TestConf03BaseDatabaseMenuVisibility(t *testing.T) {
	suite := newConfSuite(t)
	registerAltDB(t, suite)

	// ui.menu plus everything its loader resolves join the registry for this
	// test only: seeded menus on shared dev databases reference real window
	// actions, and their resolution touches ui.view and sys.model.data.
	for _, boot := range []struct {
		init     func()
		shutdown func()
	}{
		{func() { models.UIActionWindowModel.InitDefault() }, models.UIActionWindowModel.Shutdown},
		{func() { models.UIActionClientModel.InitDefault() }, models.UIActionClientModel.Shutdown},
		{func() { models.UIViewModel.InitDefault() }, models.UIViewModel.Shutdown},
		{func() { models.SysModelDataModel.InitDefault() }, models.SysModelDataModel.Shutdown},
		{func() { models.UIMenuModel.InitDefault() }, models.UIMenuModel.Shutdown},
	} {
		boot.init()
		t.Cleanup(boot.shutdown)
	}
	if err := orm.AutoMigrateRegisteredModelsToDB(t.Context(), suite.db, orm.DefaultModelRegistry()); err != nil {
		t.Fatalf("auto-migrate ui models: %v", err)
	}

	cleanup := func(ctx context.Context) {
		_, _ = suite.db.ExecContext(ctx, `DELETE FROM ui_menu WHERE name LIKE 'conf03 %'`)
	}
	cleanup(t.Context())
	t.Cleanup(func() { cleanup(context.Background()) })

	for _, menu := range []struct {
		name  string
		route string
	}{
		{"conf03 databases", "/web/base.database"},
		{"conf03 ordinary", "/web/conf.partner"},
	} {
		if _, err := suite.db.ExecContext(t.Context(),
			`INSERT INTO ui_menu (name, route_path, active, sequence) VALUES (?, ?, TRUE, 10)`,
			menu.name, menu.route); err != nil {
			t.Fatalf("insert menu %q: %v", menu.name, err)
		}
	}

	// collectMenuURLs flattens the nested sidebar tree (records + child_ids)
	// into the set of menu URLs the given database binding exposes.
	var walkMenuNodes func(nodes []any, into map[string]bool)
	walkMenuNodes = func(nodes []any, into map[string]bool) {
		for _, raw := range nodes {
			node, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if url, ok := node["url"].(string); ok && url != "" {
				into[url] = true
			}
			if children, ok := node["child_ids"].([]any); ok {
				walkMenuNodes(children, into)
			}
		}
	}
	menuRoutes := func(headers map[string]string) map[string]bool {
		status, payload := suite.rpcCall(t, rpcPath("ui.menu", "load_menus"),
			map[string]any{}, authAPIKey(), headers)
		if status != http.StatusOK {
			t.Fatalf("load_menus status = %d payload=%v", status, payload)
		}
		result := asMap(t, payload["result"])
		records, ok := result["records"].([]any)
		if !ok {
			t.Fatalf("load_menus records shape: %v", result["records"])
		}
		routes := map[string]bool{}
		walkMenuNodes(records, routes)
		return routes
	}

	// Home DB (selected explicitly — with two registered names the default
	// binding is ambiguous): both menus are visible.
	homeRoutes := menuRoutes(map[string]string{"X-Database-Name": suite.dbName})
	if !homeRoutes["/web/base.database"] {
		t.Fatalf("home DB must expose the base.database menu, got %v", homeRoutes)
	}
	if !homeRoutes["/web/conf.partner"] {
		t.Fatalf("home DB must expose ordinary menus, got %v", homeRoutes)
	}

	// Service DB (alternate logical name): base.database is hidden, ordinary
	// menus stay.
	altRoutes := menuRoutes(map[string]string{"X-Database-Name": confAltDBName})
	if altRoutes["/web/base.database"] {
		t.Fatalf("non-home DB must hide the base.database menu, got %v", altRoutes)
	}
	if !altRoutes["/web/conf.partner"] {
		t.Fatalf("non-home DB must keep ordinary menus, got %v", altRoutes)
	}
}

// fiber:context-methods migrated
