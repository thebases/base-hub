package routes

// File overview:
// - Responsibility: verifies HTTP/JSON-RPC transport behavior for the /mcp gateway, including bearer
//   auth enforcement and RBAC/ACL parity with /rpc for the generic record tools.
// - Key symbols: focused Test cases plus file-local fixtures and assertion helpers.

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"git.thebase.vn/base_hub/internal/base"
	"git.thebase.vn/base_hub/internal/config"
	"git.thebase.vn/base_hub/internal/models"
	"git.thebase.vn/base_hub/internal/orm"
	testutil "git.thebase.vn/base_hub/internal/test"
	runtest "git.thebase.vn/base_hub/internal/test/runtime"
	authmodels "git.thebase.vn/base_hub/modules/base-auth/models"
	baseauth "git.thebase.vn/base_hub/modules/base-auth/service"
	"github.com/gofiber/fiber/v3"
)

// TestMCPParseToolArgumentsAcceptsStringifiedObject guards against a
// regression where a client-side adapter forwards params.arguments as an
// OpenAI-style JSON-encoded *string* instead of an object: the old bare
// `.(map[string]any)` type assertion silently dropped it to an empty map,
// which then surfaced downstream as a misleading "arguments.path is
// required" even though the caller supplied a path.
func TestMCPParseToolArgumentsAcceptsStringifiedObject(t *testing.T) {
	got := mcpParseToolArguments(`{"path":"user_guide/base-mcp-user-guide.md"}`)
	if got["path"] != "user_guide/base-mcp-user-guide.md" {
		t.Fatalf("expected path to survive stringified arguments, got %#v", got)
	}

	if got := mcpParseToolArguments(map[string]any{"path": "user_guide/x.md"}); got["path"] != "user_guide/x.md" {
		t.Fatalf("expected object arguments to pass through unchanged, got %#v", got)
	}

	if got := mcpParseToolArguments(nil); len(got) != 0 {
		t.Fatalf("expected nil arguments to become an empty map, got %#v", got)
	}

	if got := mcpParseToolArguments("not json"); len(got) != 0 {
		t.Fatalf("expected malformed string arguments to become an empty map, got %#v", got)
	}
}

func TestMCPRouteHealth(t *testing.T) {
	t.Parallel()

	app := newMCPRouteTestApp()
	req := testutil.NewJSONRequest(t, http.MethodGet, "/mcp/health", "")
	resp, err := app.Test(req, fiber.TestConfig{Timeout: 0, FailOnTimeout: false})
	if err != nil {
		t.Fatalf("health request: %v", err)
	}
	testutil.RequireStatus(t, resp, http.StatusOK)

	var payload map[string]any
	payload = testutil.DecodeJSON[map[string]any](t, resp.Body)
	if got := payload["status"]; got != "healthy" {
		t.Fatalf("status = %v, want healthy", got)
	}
	if got := payload["path"]; got != mcpRouteBasePath {
		t.Fatalf("path = %v, want %s", got, mcpRouteBasePath)
	}
}

func TestMCPRouteBridgeDownloadUnauthenticated(t *testing.T) {
	t.Parallel()

	app := newMCPRouteTestApp()
	req := testutil.NewJSONRequest(t, http.MethodGet, "/mcp/bridge", "")
	resp, err := app.Test(req, fiber.TestConfig{Timeout: 0, FailOnTimeout: false})
	if err != nil {
		t.Fatalf("bridge download request: %v", err)
	}
	testutil.RequireStatus(t, resp, http.StatusOK)

	body := testutil.ReadBody(t, resp)
	if !strings.Contains(body, "BASE_HUB_MCP_KEY") {
		t.Fatalf("bridge script body missing expected env var reference, got: %s", body)
	}
	if got := resp.Header.Get(fiber.HeaderContentDisposition); !strings.Contains(got, bridgeScriptFilename) {
		t.Fatalf("Content-Disposition = %q, want filename %q", got, bridgeScriptFilename)
	}
}

func TestMCPRouteToolsRequiresAuth(t *testing.T) {
	t.Parallel()

	app := newMCPRouteTestApp()
	req := testutil.NewJSONRequest(t, http.MethodGet, "/mcp/tools", "")
	resp, err := app.Test(req, fiber.TestConfig{Timeout: 0, FailOnTimeout: false})
	if err != nil {
		t.Fatalf("tools request: %v", err)
	}
	testutil.RequireStatus(t, resp, http.StatusUnauthorized)
}

func TestMCPRouteJSONRPCToolsListRequiresAuth(t *testing.T) {
	t.Parallel()

	app := newMCPRouteTestApp()
	req := testutil.NewJSONRequest(t, http.MethodPost, "/mcp", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	resp, err := app.Test(req, fiber.TestConfig{Timeout: 0, FailOnTimeout: false})
	if err != nil {
		t.Fatalf("jsonrpc request: %v", err)
	}
	testutil.RequireStatus(t, resp, http.StatusUnauthorized)
}

func TestMCPRouteJSONRPCRejectsNonBearerAuth(t *testing.T) {
	t.Parallel()

	app := newMCPRouteTestApp()
	req := testutil.NewJSONRequest(t, http.MethodPost, "/mcp", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	req.Header.Set(fiber.HeaderAuthorization, "Basic dXNlcjpwYXNz")
	resp, err := app.Test(req, fiber.TestConfig{Timeout: 0, FailOnTimeout: false})
	if err != nil {
		t.Fatalf("jsonrpc request: %v", err)
	}
	testutil.RequireStatus(t, resp, http.StatusUnauthorized)
}

func TestMCPRouteJSONRPCInitializeRequiresAuth(t *testing.T) {
	t.Parallel()

	app := newMCPRouteTestApp()
	req := testutil.NewJSONRequest(t, http.MethodPost, "/mcp", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`)
	resp, err := app.Test(req, fiber.TestConfig{Timeout: 0, FailOnTimeout: false})
	if err != nil {
		t.Fatalf("initialize request: %v", err)
	}
	testutil.RequireStatus(t, resp, http.StatusUnauthorized)
}

func TestMCPRouteJSONRPCRejectsInvalidVersion(t *testing.T) {
	t.Parallel()

	app := newMCPRouteTestApp()
	req := testutil.NewJSONRequest(t, http.MethodPost, "/mcp", `{"jsonrpc":"1.0","id":1,"method":"tools/list"}`)
	resp, err := app.Test(req, fiber.TestConfig{Timeout: 0, FailOnTimeout: false})
	if err != nil {
		t.Fatalf("jsonrpc request: %v", err)
	}
	testutil.RequireStatus(t, resp, http.StatusBadRequest)
}

// TestMCPRouteJSONRPCRejectsUnacceptableAccept proves the Streamable HTTP
// Accept-header check (mcpAcceptsJSON) runs before body decoding/auth, so a
// client that explicitly can't accept application/json gets 406 rather than
// a misleading 401/400 from a later check.
func TestMCPRouteJSONRPCRejectsUnacceptableAccept(t *testing.T) {
	t.Parallel()

	app := newMCPRouteTestApp()
	req := testutil.NewJSONRequest(t, http.MethodPost, "/mcp", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	req.Header.Set(fiber.HeaderAccept, "text/html")
	resp, err := app.Test(req, fiber.TestConfig{Timeout: 0, FailOnTimeout: false})
	if err != nil {
		t.Fatalf("jsonrpc request: %v", err)
	}
	testutil.RequireStatus(t, resp, http.StatusNotAcceptable)
}

// TestMCPRouteJSONRPCAcceptsMissingOrWildcardAccept proves the Accept check
// stays backward compatible: no header, or an explicit "application/json,
// text/event-stream"/"*/*", must not be rejected before reaching auth.
func TestMCPRouteJSONRPCAcceptsMissingOrWildcardAccept(t *testing.T) {
	t.Parallel()

	for _, accept := range []string{"", "application/json, text/event-stream", "*/*"} {
		app := newMCPRouteTestApp()
		req := testutil.NewJSONRequest(t, http.MethodPost, "/mcp", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
		if accept != "" {
			req.Header.Set(fiber.HeaderAccept, accept)
		}
		resp, err := app.Test(req, fiber.TestConfig{Timeout: 0, FailOnTimeout: false})
		if err != nil {
			t.Fatalf("jsonrpc request (accept=%q): %v", accept, err)
		}
		// No auth header supplied, so the request must fall through the
		// Accept check and fail on auth (401), not 406.
		testutil.RequireStatus(t, resp, http.StatusUnauthorized)
	}
}

// TestMCPSessionLifecycle exercises mcpCreateSession/mcpTouchSession/
// mcpDeleteSession directly (no HTTP/DB fixtures needed): a session is only
// valid for the identity that created it, survives repeated touches, and is
// gone after delete or once its TTL has elapsed.
func TestMCPSessionLifecycle(t *testing.T) {
	owner := &orm.RBACIdentity{DBName: "tenant_a", UserID: 42}
	other := &orm.RBACIdentity{DBName: "tenant_a", UserID: 43}

	id := mcpCreateSession(owner)
	if !mcpTouchSession(id, owner) {
		t.Fatalf("touch by owner identity should succeed")
	}
	if mcpTouchSession(id, other) {
		t.Fatalf("touch by a different identity must fail")
	}
	if mcpTouchSession("unknown-session-id", owner) {
		t.Fatalf("touch of an unknown session id must fail")
	}
	if !mcpDeleteSession(id) {
		t.Fatalf("delete of a live session should succeed")
	}
	if mcpDeleteSession(id) {
		t.Fatalf("delete of an already-deleted session must fail")
	}
	if mcpTouchSession(id, owner) {
		t.Fatalf("touch after delete must fail")
	}

	expired := mcpCreateSession(owner)
	mcpSessionRegistryMu.Lock()
	mcpSessionRegistry[expired].expiresAt = time.Now().Add(-time.Minute)
	mcpSessionRegistryMu.Unlock()
	if mcpTouchSession(expired, owner) {
		t.Fatalf("touch of an expired session must fail")
	}
}

// TestMCPSessionCapPerIdentity: re-initializing without DELETE keeps at most
// mcpMaxSessionsPerIdentity live ids per (db, user), evicting the oldest, and
// never touches another identity's sessions.
func TestMCPSessionCapPerIdentity(t *testing.T) {
	owner := &orm.RBACIdentity{DBName: "tenant_cap", UserID: 1}
	other := &orm.RBACIdentity{DBName: "tenant_cap", UserID: 2}
	otherID := mcpCreateSession(other)

	ids := make([]string, 0, mcpMaxSessionsPerIdentity+5)
	for i := 0; i < mcpMaxSessionsPerIdentity+5; i++ {
		ids = append(ids, mcpCreateSession(owner))
		time.Sleep(time.Millisecond) // distinct expiries so "oldest" is defined
	}
	live := 0
	mcpSessionRegistryMu.Lock()
	for _, session := range mcpSessionRegistry {
		if session.dbName == owner.DBName && session.userID == owner.UserID {
			live++
		}
	}
	mcpSessionRegistryMu.Unlock()
	if live != mcpMaxSessionsPerIdentity {
		t.Fatalf("live sessions for owner = %d, want %d", live, mcpMaxSessionsPerIdentity)
	}
	if mcpTouchSession(ids[0], owner) {
		t.Fatalf("the oldest session should have been evicted")
	}
	if !mcpTouchSession(ids[len(ids)-1], owner) {
		t.Fatalf("the newest session must stay valid")
	}
	if !mcpTouchSession(otherID, other) {
		t.Fatalf("another identity's session must not be evicted")
	}
}

// TestMCPAcceptsJSON is a pure unit test of the Accept-header predicate used
// by HandleJSONRPC.
func TestMCPAcceptsJSON(t *testing.T) {
	cases := []struct {
		accept string
		want   bool
	}{
		{"", true},
		{"application/json", true},
		{"application/json, text/event-stream", true},
		{"*/*", true},
		{"text/html", false},
	}
	for _, tc := range cases {
		app := fiber.New()
		app.Get("/probe", func(c fiber.Ctx) error {
			if got := mcpAcceptsJSON(c); got != tc.want {
				t.Errorf("mcpAcceptsJSON(Accept=%q) = %v, want %v", tc.accept, got, tc.want)
			}
			return c.SendStatus(fiber.StatusOK)
		})
		req := testutil.NewJSONRequest(t, http.MethodGet, "/probe", "")
		if tc.accept != "" {
			req.Header.Set(fiber.HeaderAccept, tc.accept)
		}
		if _, err := app.Test(req, fiber.TestConfig{Timeout: 0, FailOnTimeout: false}); err != nil {
			t.Fatalf("probe request (accept=%q): %v", tc.accept, err)
		}
	}
}

// mcpRBACDocument backs the RBAC-parity integration test below. It is a
// distinct Go type/model name from the /rpc RBAC test fixtures so both test
// suites can register their models independently in the shared registry.
type mcpRBACDocument struct {
	orm.BaseModel
	Name string `bun:"name" json:"name"`
}

type mcpRBACRoleRow struct {
	orm.BaseModel
	Name       string `bun:"name"`
	Superadmin bool   `bun:"superadmin"`
}

type mcpRBACUserRow struct {
	orm.BaseModel `bun:"table:base_users"`
	Login         string `bun:"login"`
	Email         string `bun:"email"`
	Name          string `bun:"name"`
	Password      string `bun:"password"`
	GroupID       int64  `bun:"group_id"`
}

const mcpRBACModelName = "test.mcp_rbac_model"

// TestMCPRouteToolsCallEnforcesRBACParity proves an MCP-authenticated caller
// gets the same allow/deny behavior /rpc would give the same user (SRS-F-006
// acceptance criteria): a read-only ACL grant lets base.record.search_read
// succeed but base.record.create must still be rejected with 403.
func TestMCPRouteToolsCallEnforcesRBACParity(t *testing.T) {
	testutil.RestoreConfig(t, func(cfg *config.AppConfig) {
		cfg.PostgresqlDb.HomeDBName = "bean_ipn"
		cfg.PostgresqlDb.Password = "db-password"
	})

	bunDB, err := orm.NewBunDB(testutil.EnvPostgresConfig())
	if err != nil {
		t.Skipf("open postgres fixture: %v", err)
	}
	defer bunDB.Close()
	testutil.SwapBunDB(t, bunDB)

	models.SysModelLogModel.InitDefault()
	defer models.SysModelLogModel.Shutdown()
	models.SysRuleModel.InitDefault()
	defer models.SysRuleModel.Shutdown()
	models.ResGroupModel.InitDefault()
	defer models.ResGroupModel.Shutdown()
	models.ResUserModel.InitDefault()
	defer models.ResUserModel.Shutdown()
	models.SysModelAccessModel.InitDefault()
	defer models.SysModelAccessModel.Shutdown()
	authmodels.SysOauthRefreshTokenModel.InitDefault()
	defer authmodels.SysOauthRefreshTokenModel.Shutdown()
	authmodels.SysOauthClientModel.InitDefault()
	defer authmodels.SysOauthClientModel.Shutdown()

	runtest.RegisterModelType[mcpRBACDocument](t, orm.ModelSpec{
		Name:       mcpRBACModelName,
		Label:      "MCP RBAC Test Model",
		Definition: orm.ModelDefinition(mcpRBACModelName, (*mcpRBACDocument)(nil)),
	})
	orm.RegisterRuntimeExecutor(mcpRBACModelName, &runtest.ExecutorStub{
		SearchReadResult:  []orm.RecordMap{{"id": int64(7), "name": "Visible"}},
		SearchCountResult: 1,
	})

	db := bunDB.DB
	if err := orm.AutoMigrateRegisteredModelsToDB(t.Context(), db, orm.DefaultModelRegistry()); err != nil {
		t.Fatalf("auto-migrate registered models: %v", err)
	}
	if _, err := db.ExecContext(t.Context(), `ALTER TABLE sys_model_access ADD COLUMN IF NOT EXISTS active boolean DEFAULT true`); err != nil {
		t.Fatalf("ensure sys_model_access.active column: %v", err)
	}

	roleID := int64(920001)
	userID := int64(920002)

	cleanup := func() {
		_, _ = db.NewDelete().Model((*models.SysModelAccessRecord)(nil)).ModelTableExpr("sys_model_access").Where("group_id = ?", roleID).Exec(t.Context())
		_, _ = db.NewDelete().Model((*mcpRBACUserRow)(nil)).ModelTableExpr("base_users").Where("id = ?", userID).Exec(t.Context())
		_, _ = db.NewDelete().Model((*mcpRBACRoleRow)(nil)).ModelTableExpr("tbl_groups").Where("id = ?", roleID).Exec(t.Context())
	}
	cleanup()
	defer cleanup()

	if _, err := db.NewInsert().Model(&mcpRBACRoleRow{
		BaseModel: orm.BaseModel{ID: roleID},
		Name:      "MCP Viewer",
	}).ModelTableExpr("tbl_groups").Exec(t.Context()); err != nil {
		t.Fatalf("insert role: %v", err)
	}
	if _, err := db.NewInsert().Model(&mcpRBACUserRow{
		BaseModel: orm.BaseModel{ID: userID},
		Login:     "mcp_rbac_viewer",
		Email:     "mcp_rbac_viewer@example.test",
		Name:      "MCP Viewer",
		Password:  "secret",
		GroupID:   roleID,
	}).ModelTableExpr("base_users").Exec(t.Context()); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	// mcp_api_key was removed (RBAC/OAuth backend plan §1/§7.4); /mcp now
	// accepts the same OAuth bearer /rpc does, so mint one via the real
	// password grant against the user just inserted.
	pair, err := baseauth.CurrentProvider().IssuePassword(t.Context(), "bean_ipn", "mcp_rbac_viewer", "secret")
	if err != nil {
		t.Fatalf("issue password grant: %v", err)
	}
	mcpToken := pair.AccessToken
	if _, err := db.NewInsert().Model(&models.SysModelAccessRecord{
		BaseModel:  orm.BaseModel{ID: 920004, Active: true},
		Name:       "mcp viewer read access",
		ModelName:  mcpRBACModelName,
		GroupID:    &roleID,
		PermRead:   true,
		PermWrite:  false,
		PermCreate: false,
		PermUnlink: false,
	}).ModelTableExpr("sys_model_access").Exec(t.Context()); err != nil {
		t.Fatalf("insert acl: %v", err)
	}

	app := newMCPRouteTestApp()

	// Wrong bearer credential must be rejected before any tool dispatch.
	badReq := testutil.NewJSONRequest(t, http.MethodPost, "/mcp", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	badReq.Header.Set(fiber.HeaderAuthorization, "Bearer not-a-real-token")
	badResp, err := app.Test(badReq)
	if err != nil {
		t.Fatalf("bad token request: %v", err)
	}
	testutil.RequireStatus(t, badResp, http.StatusUnauthorized)

	// A valid MCP key can complete the MCP handshake: initialize returns
	// protocolVersion/serverInfo, and the immediate notifications/initialized
	// that follows gets a bare 202 with no JSON-RPC body (SRS-F-007 parity
	// with the real MCP Streamable HTTP lifecycle).
	initReq := testutil.NewJSONRequest(t, http.MethodPost, "/mcp", `{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`)
	initReq.Header.Set(fiber.HeaderAuthorization, "Bearer "+mcpToken)
	initResp, err := app.Test(initReq)
	if err != nil {
		t.Fatalf("initialize request: %v", err)
	}
	if initResp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected initialize status: %d body=%s", initResp.StatusCode, testutil.ReadBody(t, initResp))
	}
	initPayload := testutil.DecodeJSON[map[string]any](t, initResp.Body)
	initResult, ok := initPayload["result"].(map[string]any)
	if !ok {
		t.Fatalf("initialize result type = %T, want map[string]any", initPayload["result"])
	}
	if initResult["protocolVersion"] == nil {
		t.Fatalf("initialize result missing protocolVersion: %#v", initResult)
	}
	if _, ok := initResult["serverInfo"].(map[string]any); !ok {
		t.Fatalf("initialize result missing serverInfo: %#v", initResult)
	}
	// Streamable HTTP: "initialize" issues a session id the client may
	// optionally carry on later requests (mcpSessionIDHeader).
	sessionID := initResp.Header.Get(mcpSessionIDHeader)
	if sessionID == "" {
		t.Fatalf("initialize response missing %s header", mcpSessionIDHeader)
	}

	// An unrecognized session id must be rejected (404) rather than silently
	// accepted, while a request that omits the header entirely still works
	// unchanged (checked further down via listReq, which sends no session id).
	staleReq := testutil.NewJSONRequest(t, http.MethodPost, "/mcp", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	staleReq.Header.Set(fiber.HeaderAuthorization, "Bearer "+mcpToken)
	staleReq.Header.Set(mcpSessionIDHeader, "not-a-real-session")
	staleResp, err := app.Test(staleReq)
	if err != nil {
		t.Fatalf("stale session request: %v", err)
	}
	testutil.RequireStatus(t, staleResp, http.StatusNotFound)

	// A valid session id is accepted on a subsequent call.
	validSessionReq := testutil.NewJSONRequest(t, http.MethodPost, "/mcp", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	validSessionReq.Header.Set(fiber.HeaderAuthorization, "Bearer "+mcpToken)
	validSessionReq.Header.Set(mcpSessionIDHeader, sessionID)
	validSessionResp, err := app.Test(validSessionReq)
	if err != nil {
		t.Fatalf("valid session request: %v", err)
	}
	testutil.RequireStatus(t, validSessionResp, http.StatusOK)

	// An unsupported MCP-Protocol-Version header must be rejected (400);
	// omitting it (every other request in this test) stays backward compatible.
	badVersionReq := testutil.NewJSONRequest(t, http.MethodPost, "/mcp", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	badVersionReq.Header.Set(fiber.HeaderAuthorization, "Bearer "+mcpToken)
	badVersionReq.Header.Set(mcpProtocolVersionHeader, "1999-01-01")
	badVersionResp, err := app.Test(badVersionReq)
	if err != nil {
		t.Fatalf("bad protocol version request: %v", err)
	}
	testutil.RequireStatus(t, badVersionResp, http.StatusBadRequest)

	notifyReq := testutil.NewJSONRequest(t, http.MethodPost, "/mcp", `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	notifyReq.Header.Set(fiber.HeaderAuthorization, "Bearer "+mcpToken)
	notifyResp, err := app.Test(notifyReq)
	if err != nil {
		t.Fatalf("notifications/initialized request: %v", err)
	}
	if notifyResp.StatusCode != http.StatusAccepted {
		t.Fatalf("unexpected notifications/initialized status: %d body=%s", notifyResp.StatusCode, testutil.ReadBody(t, notifyResp))
	}

	// A valid MCP key can list the generic tool catalog.
	listReq := testutil.NewJSONRequest(t, http.MethodPost, "/mcp", `{"jsonrpc":"2.0","id":9,"method":"tools/list"}`)
	listReq.Header.Set(fiber.HeaderAuthorization, "Bearer "+mcpToken)
	listResp, err := app.Test(listReq)
	if err != nil {
		t.Fatalf("tools/list request: %v", err)
	}
	if listResp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected tools/list status: %d body=%s", listResp.StatusCode, testutil.ReadBody(t, listResp))
	}
	listPayload := testutil.DecodeJSON[map[string]any](t, listResp.Body)
	listResult, ok := listPayload["result"].(map[string]any)
	if !ok {
		t.Fatalf("result type = %T, want map[string]any", listPayload["result"])
	}
	tools, ok := listResult["tools"].([]any)
	if !ok || len(tools) == 0 {
		t.Fatalf("result.tools = %#v, want non-empty []any", listResult["tools"])
	}
	// Every tool descriptor with a schema must serialize it as "inputSchema"
	// (MCP's literal field name), not "input_schema" — a client validating
	// tool schemas against the spec ignores an unrecognized key.
	for _, tool := range tools {
		descriptor, ok := tool.(map[string]any)
		if !ok {
			t.Fatalf("tool descriptor type = %T, want map[string]any", tool)
		}
		if _, hasSnakeCase := descriptor["input_schema"]; hasSnakeCase {
			t.Fatalf("tool descriptor %v uses input_schema, want inputSchema", descriptor["name"])
		}
	}

	// A valid MCP key can search_read a model it has read ACL for.
	searchReadBody := `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"base.record.search_read","arguments":{"model":"` + mcpRBACModelName + `","fields":["id","name"]}}}`
	searchReq := testutil.NewJSONRequest(t, http.MethodPost, "/mcp", searchReadBody)
	searchReq.Header.Set(fiber.HeaderAuthorization, "Bearer "+mcpToken)
	searchResp, err := app.Test(searchReq)
	if err != nil {
		t.Fatalf("search_read request: %v", err)
	}
	if searchResp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected search_read status: %d body=%s", searchResp.StatusCode, testutil.ReadBody(t, searchResp))
	}
	searchPayload := testutil.DecodeJSON[map[string]any](t, searchResp.Body)
	searchResult, ok := searchPayload["result"].(map[string]any)
	if !ok {
		t.Fatalf("search_read result type = %T, want map[string]any", searchPayload["result"])
	}
	// A conforming MCP client only reads `content`; requiring a "text" block
	// here (not the old custom "json" type) proves the result is visible to
	// Claude/OpenAI/DeepSeek/Kimi clients that ignore structuredContent.
	content, ok := searchResult["content"].([]any)
	if !ok || len(content) == 0 {
		t.Fatalf("search_read result.content = %#v, want non-empty []any", searchResult["content"])
	}
	block, ok := content[0].(map[string]any)
	if !ok || block["type"] != "text" {
		t.Fatalf("search_read content[0] = %#v, want type=text", content[0])
	}
	if _, ok := block["text"].(string); !ok {
		t.Fatalf("search_read content[0].text type = %T, want string", block["text"])
	}
	if _, ok := searchResult["structuredContent"]; !ok {
		t.Fatalf("search_read result missing structuredContent: %#v", searchResult)
	}

	// The same identity must still be denied create, exactly as /rpc would deny it.
	createBody := `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"base.record.create","arguments":{"model":"` + mcpRBACModelName + `","values":{"name":"Blocked"},"idempotency_key":"mcp-rbac-parity-test-create"}}}`
	createReq := testutil.NewJSONRequest(t, http.MethodPost, "/mcp", createBody)
	createReq.Header.Set(fiber.HeaderAuthorization, "Bearer "+mcpToken)
	createResp, err := app.Test(createReq)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	if createResp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected create to be forbidden, got %d body=%s", createResp.StatusCode, testutil.ReadBody(t, createResp))
	}

	// DELETE explicitly terminates the session: first call succeeds (204),
	// the id is then unusable both for another DELETE (404) and for a
	// tools/list that supplies it (404, same as the never-existed case above).
	deleteReq := testutil.NewJSONRequest(t, http.MethodDelete, "/mcp", "")
	deleteReq.Header.Set(fiber.HeaderAuthorization, "Bearer "+mcpToken)
	deleteReq.Header.Set(mcpSessionIDHeader, sessionID)
	deleteResp, err := app.Test(deleteReq)
	if err != nil {
		t.Fatalf("delete session request: %v", err)
	}
	testutil.RequireStatus(t, deleteResp, http.StatusNoContent)

	deleteAgainResp, err := app.Test(deleteReq)
	if err != nil {
		t.Fatalf("repeat delete session request: %v", err)
	}
	testutil.RequireStatus(t, deleteAgainResp, http.StatusNotFound)

	afterDeleteReq := testutil.NewJSONRequest(t, http.MethodPost, "/mcp", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	afterDeleteReq.Header.Set(fiber.HeaderAuthorization, "Bearer "+mcpToken)
	afterDeleteReq.Header.Set(mcpSessionIDHeader, sessionID)
	afterDeleteResp, err := app.Test(afterDeleteReq)
	if err != nil {
		t.Fatalf("post-delete session request: %v", err)
	}
	testutil.RequireStatus(t, afterDeleteResp, http.StatusNotFound)
}

func newMCPRouteTestApp() *fiber.App {
	app := fiber.New(fiber.Config{JSONEncoder: base.JSONMarshal})
	RegisterMCPRoutes(app)
	return app
}

// TestInt64FromArgument covers int64FromArgument's supported input shapes
// (JSON number, Go int/int64, numeric string) and its rejection of
// non-numeric input; mcpAddFieldRelationID relies on it to normalize a
// resolved field row's numeric id regardless of transport shape.
func TestInt64FromArgument(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		input any
		want  int64
		ok    bool
	}{
		{"float64", float64(12), 12, true},
		{"int", 7, 7, true},
		{"int64", int64(9), 9, true},
		{"numeric string", "42", 42, true},
		{"blank string", "  ", 0, false},
		{"non-numeric string", "abc", 0, false},
		{"unsupported type", true, 0, false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := int64FromArgument(tc.input)
			if ok != tc.ok || got != tc.want {
				t.Fatalf("int64FromArgument(%#v) = (%d, %v), want (%d, %v)", tc.input, got, ok, tc.want, tc.ok)
			}
		})
	}
}

// TestGenerateCustomBIDUnique proves the auto-generated bid used when a
// caller omits one is readable (stable model-name-derived prefix) and
// unique per call (random suffix), so two create_custom calls never produce
// the same bid.
func TestGenerateCustomBIDUnique(t *testing.T) {
	t.Parallel()

	first := models.GenerateBID("tbl.partner.address")
	second := models.GenerateBID("tbl.partner.address")
	if first == second {
		t.Fatalf("expected distinct bids, got %q twice", first)
	}
	if !strings.HasPrefix(first, "tblpartneraddress-") {
		t.Fatalf("bid = %q, want tblpartneraddress- prefix", first)
	}
}

func TestMCPCreateSessionSweepsExpired(t *testing.T) {
	mcpSessionRegistryMu.Lock()
	mcpSessionRegistry = map[string]*mcpSession{"old": {expiresAt: time.Now().Add(-time.Hour)}}
	mcpSessionLastSweep = time.Time{}
	mcpSessionRegistryMu.Unlock()
	mcpCreateSession(&orm.RBACIdentity{DBName: "d", UserID: 1})
	mcpSessionRegistryMu.Lock()
	defer mcpSessionRegistryMu.Unlock()
	if _, ok := mcpSessionRegistry["old"]; ok || len(mcpSessionRegistry) != 1 {
		t.Fatalf("expired session not swept: %d entries", len(mcpSessionRegistry))
	}
}
