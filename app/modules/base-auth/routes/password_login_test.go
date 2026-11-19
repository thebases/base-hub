package routes

// File overview:
// - Responsibility: end-to-end coverage for POST /oauth/token's JSON-body login variant and
//   GET /oauth/me against a real Postgres fixture (skip-if-unreachable), plus the SEC-03 rate
//   limiter as seen through that JSON route. Mirrors oauth_route_test.go's coverage of the
//   RFC 6749 form-encoded grant.
// - Key symbols: `TestJSONPasswordLoginHydratesSessionInfo`, `TestJSONPasswordLoginReturns429WhenRateLimited`.

import (
	"net/http"
	"strings"
	"testing"

	"git.thebase.vn/base_hub/internal/models"
	"git.thebase.vn/base_hub/internal/orm"
	testutil "git.thebase.vn/base_hub/internal/test"
	authmodels "git.thebase.vn/base_hub/modules/base-auth/models"
	"github.com/gofiber/fiber/v3"
	"github.com/uptrace/bun"
)

func jsonRequest(t *testing.T, method, path, body string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(method, path, strings.NewReader(body))
	if err != nil {
		t.Fatalf("build request %s %s: %v", method, path, err)
	}
	req.Header.Set("Content-Type", "application/json")
	return req
}

// TestJSONPasswordLoginHydratesSessionInfo covers POST /oauth/token's
// JSON-body login (originally POST /web/session/authenticate, then
// POST /auth/token) end to end into GET /oauth/me (originally
// GET /web/session/get_session_info, then GET /auth/me) — the login result
// carries the token pair + user_context directly, and /oauth/me must report
// the same identity for a bearer minted either way.
func TestJSONPasswordLoginHydratesSessionInfo(t *testing.T) {
	bunDB, err := orm.NewBunDB(testutil.EnvPostgresConfig())
	if err != nil {
		t.Skipf("open postgres fixture: %v", err)
	}
	defer bunDB.Close()
	testutil.SwapBunDB(t, bunDB)
	dbName := testutil.EnvPostgresConfig().Name
	orm.SetNamedBunDB(dbName, bunDB)
	orm.SetDefaultBunDBName(dbName)
	defer orm.SetNamedBunDB(dbName, nil)

	models.ResGroupModel.InitDefault()
	defer models.ResGroupModel.Shutdown()
	models.ResUserModel.InitDefault()
	defer models.ResUserModel.Shutdown()
	authmodels.SysOauthRefreshTokenModel.InitDefault()
	defer authmodels.SysOauthRefreshTokenModel.Shutdown()
	authmodels.SysOauthClientModel.InitDefault()
	defer authmodels.SysOauthClientModel.Shutdown()

	db := bunDB.DB
	if err := orm.AutoMigrateRegisteredModelsToDB(t.Context(), db, orm.DefaultModelRegistry()); err != nil {
		t.Fatalf("auto-migrate registered models: %v", err)
	}

	userID := int64(930201)
	type passwordLoginTestUserRow struct {
		bun.BaseModel `bun:"table:base_users"`
		ID            int64  `bun:"id,pk"`
		Login         string `bun:"login"`
		Email         string `bun:"email"`
		Name          string `bun:"name"`
		Password      string `bun:"password"`
		Lang          string `bun:"lang"`
	}
	_, _ = db.NewDelete().Model((*passwordLoginTestUserRow)(nil)).ModelTableExpr("base_users").Where("id = ?", userID).Exec(t.Context())
	if _, err := db.NewInsert().Model(&passwordLoginTestUserRow{
		ID: userID, Login: "password_login_test_user", Email: "password_login_test_user@example.test",
		Name: "Password Login Test User", Password: "secret", Lang: "vi",
	}).ModelTableExpr("base_users").Exec(t.Context()); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	defer func() {
		_, _ = db.NewDelete().Model((*passwordLoginTestUserRow)(nil)).ModelTableExpr("base_users").Where("id = ?", userID).Exec(t.Context())
		_, _ = db.NewDelete().Table("sys_oauth_refresh_token").Where("user_id = ?", userID).Exec(t.Context())
	}()

	app := newOAuthTestApp()

	loginReq := jsonRequest(t, http.MethodPost, "/oauth/token",
		`{"params":{"db":"`+dbName+`","login":"password_login_test_user","password":"secret","context":{"lang":"en_US"}}}`)
	loginResp, err := app.Test(loginReq)
	if err != nil {
		t.Fatalf("login request: %v", err)
	}
	testutil.RequireStatus(t, loginResp, http.StatusOK)
	loginPayload := testutil.DecodeJSON[map[string]any](t, loginResp.Body)
	loginResult, ok := loginPayload["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected login result map, got %#v", loginPayload["result"])
	}
	if got := loginResult["uid"]; got != float64(userID) {
		t.Fatalf("uid = %#v, want %d", got, userID)
	}
	if got := loginResult["auth_type"]; got != "oauth" {
		t.Fatalf("auth_type = %#v, want %q", got, "oauth")
	}
	accessToken, _ := loginResult["access_token"].(string)
	if accessToken == "" {
		t.Fatalf("expected non-empty access_token in login result: %#v", loginResult)
	}

	infoReq, err := http.NewRequest(http.MethodGet, "/oauth/me", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	infoReq.Header.Set("Authorization", "Bearer "+accessToken)
	infoReq.Header.Set("X-Database-Name", dbName)
	infoResp, err := app.Test(infoReq)
	if err != nil {
		t.Fatalf("session info request: %v", err)
	}
	testutil.RequireStatus(t, infoResp, http.StatusOK)
	infoPayload := testutil.DecodeJSON[map[string]any](t, infoResp.Body)
	infoResult, _ := infoPayload["result"].(map[string]any)
	if infoResult == nil {
		t.Fatalf("missing result in response: %#v", infoPayload)
	}
	if got := infoResult["valid"]; got != true {
		t.Fatalf("valid = %#v, want true", got)
	}
	if got := infoResult["username"]; got != "password_login_test_user" {
		t.Fatalf("username = %#v, want password_login_test_user", got)
	}
	userContext, _ := infoResult["user_context"].(map[string]any)
	if userContext == nil {
		t.Fatalf("missing user_context in response: %#v", infoResult)
	}
	if got := userContext["lang"]; got != "vi" {
		t.Fatalf("user_context.lang = %#v, want vi", got)
	}
}

func TestJSONPasswordLoginReturns429WhenRateLimited(t *testing.T) {
	// Swap in an isolated limiter so this test never shares failure state with
	// other tests (including the form-grant tests in oauth_route_test.go,
	// which share this same oauthLoginLimiter) or leaks its lockout into them.
	originalLimiter := oauthLoginLimiter
	oauthLoginLimiter = newLoginRateLimiter()
	t.Cleanup(func() { oauthLoginLimiter = originalLimiter })

	app := newOAuthTestApp()
	// Discover the client IP fiber reports for app.Test connections so the
	// preloaded failures target the same limiter key as the real request.
	app.Get("/__probe_ip", func(c fiber.Ctx) error { return c.SendString(c.IP()) })
	probeResp, err := app.Test(jsonRequest(t, http.MethodGet, "/__probe_ip", ""))
	if err != nil {
		t.Fatalf("probe request: %v", err)
	}
	clientIP := testutil.ReadBody(t, probeResp)

	// Five recorded failures cross the lockout threshold for this login+IP pair.
	for i := 0; i < loginFailureThreshold; i++ {
		oauthLoginLimiter.recordFailure("admin", clientIP)
	}

	body := `{"params":{"login":"admin","password":"wrong"}}`
	limitedResp, err := app.Test(jsonRequest(t, http.MethodPost, "/oauth/token", body))
	if err != nil {
		t.Fatalf("rate-limited login request: %v", err)
	}
	testutil.RequireStatus(t, limitedResp, http.StatusTooManyRequests)
	if limitedResp.Header.Get("Retry-After") == "" {
		t.Fatalf("expected a Retry-After header on the 429 response")
	}

	// A successful login resets the counter: the next attempt must get past the
	// limiter (it then fails deeper in the stack, but never with 429).
	oauthLoginLimiter.recordSuccess("admin", clientIP)
	retryResp, err := app.Test(jsonRequest(t, http.MethodPost, "/oauth/token", body))
	if err != nil {
		t.Fatalf("post-reset login request: %v", err)
	}
	if retryResp.StatusCode == http.StatusTooManyRequests {
		t.Fatalf("expected the limiter to allow the attempt after a success reset")
	}
}

// TestSessionAllowedCompaniesKeepsIdentityOrder covers the company switcher payload in
// user_context: names for the authorized ids only, in identity order, unknown ids dropped.
func TestSessionAllowedCompaniesKeepsIdentityOrder(t *testing.T) {
	bunDB, err := orm.NewBunDB(testutil.EnvPostgresConfig())
	if err != nil {
		t.Skipf("open postgres fixture: %v", err)
	}
	defer bunDB.Close()
	dbName := testutil.EnvPostgresConfig().Name
	orm.SetNamedBunDB(dbName, bunDB)
	defer orm.SetNamedBunDB(dbName, nil)

	db := bunDB.DB
	// One connection so a TEMP tbl_company (fixtures that never migrated tbl.company) stays
	// visible to the helper's query; pg_temp is searched first, so it also shadows a real table.
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(t.Context(), "CREATE TEMP TABLE IF NOT EXISTS tbl_company (id bigint PRIMARY KEY, name text NOT NULL)"); err != nil {
		t.Skipf("create temp tbl_company: %v", err)
	}
	ids := []int64{930211, 930212}
	cleanup := func() {
		_, _ = db.NewDelete().TableExpr("tbl_company").Where("id IN (?)", bun.In(ids)).Exec(t.Context())
	}
	cleanup()
	defer cleanup()
	for i, id := range ids {
		if _, err := db.NewRaw("INSERT INTO tbl_company (id, name) VALUES (?, ?)", id, []string{"Switcher Test A", "Switcher Test B"}[i]).Exec(t.Context()); err != nil {
			t.Skipf("insert fixture company: %v", err)
		}
	}

	got := sessionAllowedCompanies(t.Context(), dbName, 1, []int64{930212, 930299, 930211})
	if len(got) != 2 {
		t.Fatalf("companies = %#v, want 2 rows", got)
	}
	if got[0]["id"] != int64(930212) || got[0]["name"] != "Switcher Test B" || got[1]["id"] != int64(930211) {
		t.Fatalf("companies = %#v, want identity order [930212, 930211]", got)
	}
	if sessionAllowedCompanies(t.Context(), dbName, 1, nil) != nil {
		t.Fatalf("expected nil for an empty company set")
	}
}
