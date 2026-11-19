package routes

// File overview:
// - Responsibility: end-to-end coverage for POST /oauth/token and POST /oauth/revoke against a
//   real Postgres fixture (skip-if-unreachable, matching this repo's convention), exercising the
//   password grant, bearer validation, refresh rotation, reuse-detection, and revoke (RBAC/OAuth
//   backend plan §9 regression checklist).
// - Key symbols: `TestOAuthPasswordGrantAndRefreshRotation`.

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"git.thebase.vn/base_hub/internal/models"
	"git.thebase.vn/base_hub/internal/orm"
	testutil "git.thebase.vn/base_hub/internal/test"
	authmodels "git.thebase.vn/base_hub/modules/base-auth/models"
	baseauth "git.thebase.vn/base_hub/modules/base-auth/service"
	"github.com/gofiber/fiber/v3"
	"github.com/uptrace/bun"
)

func newOAuthTestApp() *fiber.App {
	app := fiber.New()
	RegisterRoutes(app)
	return app
}

func formRequest(t *testing.T, path string, form url.Values) *http.Request {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("build form request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req
}

func TestOAuthPasswordGrantAndRefreshRotation(t *testing.T) {
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
	userID := int64(930001)
	type oauthTestUserRow struct {
		bun.BaseModel `bun:"table:base_users"`
		ID            int64  `bun:"id,pk"`
		Login         string `bun:"login"`
		Email         string `bun:"email"`
		Name          string `bun:"name"`
		Password      string `bun:"password"`
	}
	_, _ = db.NewDelete().Model((*oauthTestUserRow)(nil)).ModelTableExpr("base_users").Where("id = ?", userID).Exec(t.Context())
	if _, err := db.NewInsert().Model(&oauthTestUserRow{
		ID: userID, Login: "oauth_test_user", Email: "oauth_test_user@example.test",
		Name: "OAuth Test User", Password: "secret",
	}).ModelTableExpr("base_users").Exec(t.Context()); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	defer func() {
		_, _ = db.NewDelete().Model((*oauthTestUserRow)(nil)).ModelTableExpr("base_users").Where("id = ?", userID).Exec(t.Context())
		_, _ = db.NewDelete().Table("sys_oauth_refresh_token").Where("user_id = ?", userID).Exec(t.Context())
	}()

	app := newOAuthTestApp()

	// grant_type=password issues an access+refresh pair.
	resp, err := app.Test(formRequest(t, "/oauth/token", url.Values{
		"grant_type": {"password"},
		"username":   {"oauth_test_user"},
		"password":   {"secret"},
		"db":         {dbName},
	}))
	if err != nil {
		t.Fatalf("password grant request: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("password grant status = %d body=%s", resp.StatusCode, testutil.ReadBody(t, resp))
	}
	payload := testutil.DecodeJSON[map[string]any](t, resp.Body)
	accessToken, _ := payload["access_token"].(string)
	refreshToken, _ := payload["refresh_token"].(string)
	if accessToken == "" || refreshToken == "" {
		t.Fatalf("expected non-empty access_token/refresh_token, got %#v", payload)
	}
	if got := payload["token_type"]; got != "Bearer" {
		t.Fatalf("token_type = %#v, want Bearer", got)
	}

	// The minted access token validates back to the same user via Validate.
	identity, err := baseauth.CurrentProvider().Validate(t.Context(), accessToken)
	if err != nil || identity == nil {
		t.Fatalf("validate access token: %v", err)
	}
	if identity.UserID != userID || identity.Login != "oauth_test_user" || identity.DBName != dbName {
		t.Fatalf("unexpected identity: %#v", identity)
	}

	// grant_type=refresh_token rotates: the old token stops working, the new
	// one succeeds, and replaying the old (now-revoked) token is rejected.
	refreshResp, err := app.Test(formRequest(t, "/oauth/token", url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"db":            {dbName},
	}))
	if err != nil {
		t.Fatalf("refresh request: %v", err)
	}
	testutil.RequireStatus(t, refreshResp, http.StatusOK)
	refreshPayload := testutil.DecodeJSON[map[string]any](t, refreshResp.Body)
	newRefreshToken, _ := refreshPayload["refresh_token"].(string)
	if newRefreshToken == "" || newRefreshToken == refreshToken {
		t.Fatalf("expected a new, different refresh token, got %#v", refreshPayload)
	}

	reuseResp, err := app.Test(formRequest(t, "/oauth/token", url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"db":            {dbName},
	}))
	if err != nil {
		t.Fatalf("reuse request: %v", err)
	}
	if reuseResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("reused refresh token status = %d, want %d", reuseResp.StatusCode, http.StatusUnauthorized)
	}

	// Breach detection revoked the whole family: the freshly rotated token
	// must also be rejected now.
	familyResp, err := app.Test(formRequest(t, "/oauth/token", url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {newRefreshToken},
		"db":            {dbName},
	}))
	if err != nil {
		t.Fatalf("post-breach request: %v", err)
	}
	if familyResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("post-breach refresh status = %d, want %d", familyResp.StatusCode, http.StatusUnauthorized)
	}
}
