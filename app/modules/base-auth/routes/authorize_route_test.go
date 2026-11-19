package routes

// File overview:
// - Responsibility: end-to-end coverage for GET/POST /oauth/authorize and the
//   grant_type=authorization_code exchange, against a real Postgres fixture
//   (skip-if-unreachable, same convention as oauth_route_test.go). Covers the
//   security-critical invariants from
//   .agents/planning/2026-08-09-oauth-authorization-code-flow-plan.md §8: unknown
//   client/redirect_uri rejected before any redirect, wrong password re-renders
//   instead of redirecting, a code is single-use, and a mismatched redirect_uri
//   at exchange time is rejected.
// - Key symbols: `TestOAuthAuthorizationCodeFlow`.

import (
	"crypto/sha256"
	"encoding/base64"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"git.thebase.vn/base_hub/internal/models"
	"git.thebase.vn/base_hub/internal/orm"
	internalroutes "git.thebase.vn/base_hub/internal/routes"
	testutil "git.thebase.vn/base_hub/internal/test"
	authmodels "git.thebase.vn/base_hub/modules/base-auth/models"
	baseauth "git.thebase.vn/base_hub/modules/base-auth/service"
	"github.com/gofiber/fiber/v3"
	"github.com/uptrace/bun"
)

func TestOAuthAuthorizationCodeFlow(t *testing.T) {
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
	authmodels.SysOauthAuthCodeModel.InitDefault()
	defer authmodels.SysOauthAuthCodeModel.Shutdown()

	db := bunDB.DB
	if err := orm.AutoMigrateRegisteredModelsToDB(t.Context(), db, orm.DefaultModelRegistry()); err != nil {
		t.Fatalf("auto-migrate registered models: %v", err)
	}

	const redirectURI = "https://chat.openai.com/aip/g-test/oauth/callback"
	userID := int64(930101)
	type authCodeTestUserRow struct {
		bun.BaseModel `bun:"table:base_users"`
		ID            int64  `bun:"id,pk"`
		Login         string `bun:"login"`
		Email         string `bun:"email"`
		Name          string `bun:"name"`
		Password      string `bun:"password"`
	}
	_, _ = db.NewDelete().Model((*authCodeTestUserRow)(nil)).ModelTableExpr("base_users").Where("id = ?", userID).Exec(t.Context())
	if _, err := db.NewInsert().Model(&authCodeTestUserRow{
		ID: userID, Login: "authz_test_user", Email: "authz_test_user@example.test",
		Name: "Authz Test User", Password: "secret",
	}).ModelTableExpr("base_users").Exec(t.Context()); err != nil {
		t.Fatalf("insert user: %v", err)
	}

	type oauthTestClientRow struct {
		bun.BaseModel    `bun:"table:sys_oauth_client"`
		ID               int64    `bun:"id,pk,autoincrement"`
		ClientID         string   `bun:"client_id"`
		ClientSecretHash string   `bun:"client_secret_hash"`
		ServiceName      string   `bun:"service_name"`
		RoleID           int64    `bun:"role_id"`
		IsSuperadmin     bool     `bun:"is_superadmin"`
		RedirectURIs     []string `bun:"redirect_uris,type:jsonb"`
		Active           bool     `bun:"active"`
	}
	clientSecret := "test-client-secret"
	secretHash, err := authmodels.HashClientSecret(clientSecret)
	if err != nil {
		t.Fatalf("hash client secret: %v", err)
	}
	_, _ = db.NewDelete().Model((*oauthTestClientRow)(nil)).ModelTableExpr("sys_oauth_client").Where("client_id = ?", "gpt-test-client").Exec(t.Context())
	if _, err := db.NewInsert().Model(&oauthTestClientRow{
		ClientID: "gpt-test-client", ClientSecretHash: secretHash, ServiceName: "Test GPT",
		RedirectURIs: []string{redirectURI}, Active: true,
	}).ModelTableExpr("sys_oauth_client").Exec(t.Context()); err != nil {
		t.Fatalf("insert oauth client: %v", err)
	}
	defer func() {
		_, _ = db.NewDelete().Model((*authCodeTestUserRow)(nil)).ModelTableExpr("base_users").Where("id = ?", userID).Exec(t.Context())
		_, _ = db.NewDelete().Model((*oauthTestClientRow)(nil)).ModelTableExpr("sys_oauth_client").Where("client_id = ?", "gpt-test-client").Exec(t.Context())
		_, _ = db.NewDelete().Table("sys_oauth_refresh_token").Where("user_id = ?", userID).Exec(t.Context())
		_, _ = db.NewDelete().Table("sys_oauth_auth_code").Where("user_id = ?", userID).Exec(t.Context())
	}()

	app := newOAuthTestApp()

	authorizeQueryForPath := func(path, clientID, redirectURI string) string {
		v := url.Values{
			"response_type": {"code"},
			"client_id":     {clientID},
			"redirect_uri":  {redirectURI},
			"state":         {"xyz123"},
			"db":            {dbName},
		}
		return path + "?" + v.Encode()
	}
	authorizeQuery := func(clientID, redirectURI string) string {
		return authorizeQueryForPath("/oauth/authorize", clientID, redirectURI)
	}

	// Unknown client_id must 400, never redirect.
	resp, err := app.Test(httpGet(t, authorizeQuery("no-such-client", redirectURI)))
	if err != nil {
		t.Fatalf("unknown client request: %v", err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("unknown client status = %d, want 400", resp.StatusCode)
	}

	// redirect_uri not on the allow-list must 400, never redirect.
	resp, err = app.Test(httpGet(t, authorizeQuery("gpt-test-client", "https://evil.example/callback")))
	if err != nil {
		t.Fatalf("bad redirect_uri request: %v", err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad redirect_uri status = %d, want 400", resp.StatusCode)
	}

	// Valid request renders the login form.
	resp, err = app.Test(httpGet(t, authorizeQuery("gpt-test-client", redirectURI)))
	if err != nil {
		t.Fatalf("valid authorize GET: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("authorize GET status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "Sign in") {
		t.Fatalf("expected login form in response, got %s", body)
	}

	// A "db"-less authorize request must still resolve the right tenant via
	// X-Database-Name — the header an operator-run proxy in front of Base HUB
	// injects when the caller's fixed authorize-URL shape (e.g. ChatGPT's GPT
	// Actions config) has no room for a db query param. Without this fallback
	// the request silently resolves to the home/control-plane DB instead,
	// where gpt-test-client doesn't exist, misreporting the failure as
	// "redirect_uri is not registered for this client".
	noDBQuery := url.Values{
		"response_type": {"code"},
		"client_id":     {"gpt-test-client"},
		"redirect_uri":  {redirectURI},
		"state":         {"xyz123"},
	}
	noDBReq := httpGet(t, "/oauth/authorize?"+noDBQuery.Encode())
	noDBReq.Header.Set(internalroutes.RPCHeaderDatabaseName, dbName)
	resp, err = app.Test(noDBReq)
	if err != nil {
		t.Fatalf("db-less authorize GET with X-Database-Name header: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("db-less authorize GET with X-Database-Name header status = %d, want 200, body=%s", resp.StatusCode, testutil.ReadBody(t, resp))
	}

	// /auth/authorize is kept as a deployed-client compatibility alias. The
	// production symptom this guards against is the request falling through to
	// the PWA fallback and showing a blank app shell instead of this form.
	resp, err = app.Test(httpGet(t, authorizeQueryForPath("/auth/authorize", "gpt-test-client", redirectURI)))
	if err != nil {
		t.Fatalf("auth alias authorize GET: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("auth alias authorize GET status = %d, want 200", resp.StatusCode)
	}
	body, _ = io.ReadAll(resp.Body)
	bodyText := string(body)
	if !strings.Contains(bodyText, `action="/auth/authorize"`) ||
		!strings.Contains(bodyText, `name="username"`) ||
		!strings.Contains(bodyText, `name="password"`) {
		t.Fatalf("expected /auth/authorize login form, got %s", bodyText)
	}

	implicitQuery := url.Values{
		"response_type": {"token"},
		"client_id":     {"gpt-test-client"},
		"redirect_uri":  {redirectURI},
		"state":         {"implicit123"},
		"db":            {dbName},
	}
	resp, err = app.Test(httpGet(t, "/oauth/authorize?"+implicitQuery.Encode()))
	if err != nil {
		t.Fatalf("implicit authorize GET: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("implicit authorize GET status = %d, want 200", resp.StatusCode)
	}

	authorizeForm := func(username, password string) url.Values {
		return url.Values{
			"response_type": {"code"},
			"client_id":     {"gpt-test-client"},
			"redirect_uri":  {redirectURI},
			"state":         {"xyz123"},
			"db":            {dbName},
			"username":      {username},
			"password":      {password},
		}
	}

	// Wrong password re-renders the form, does not redirect.
	resp, err = app.Test(authorizeFormRequest(t, authorizeForm("authz_test_user", "wrong")))
	if err != nil {
		t.Fatalf("wrong password POST: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("wrong password status = %d, want 200 (re-rendered form)", resp.StatusCode)
	}
	body, _ = io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "invalid login or password") {
		t.Fatalf("expected error message in re-rendered form, got %s", body)
	}

	// Correct login redirects to redirect_uri with code + the original state.
	resp, err = app.Test(authorizeFormRequest(t, authorizeForm("authz_test_user", "secret")), fiber.TestConfig{Timeout: 0, FailOnTimeout: false})
	if err != nil {
		t.Fatalf("correct login POST: %v", err)
	}
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("correct login status = %d, want 302", resp.StatusCode)
	}
	location, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		t.Fatalf("parse redirect location: %v", err)
	}
	if location.Query().Get("state") != "xyz123" {
		t.Fatalf("state = %q, want xyz123", location.Query().Get("state"))
	}
	code := location.Query().Get("code")
	if code == "" {
		t.Fatalf("expected non-empty code in redirect, got %s", location)
	}

	exchangeForm := func(code, redirectURI string) url.Values {
		return url.Values{
			"grant_type":    {"authorization_code"},
			"client_id":     {"gpt-test-client"},
			"client_secret": {clientSecret},
			"code":          {code},
			"redirect_uri":  {redirectURI},
		}
	}
	exchangeRequest := func(code, redirectURI string) *http.Request {
		req := formRequest(t, "/oauth/token", exchangeForm(code, redirectURI))
		req.Header.Set(internalroutes.RPCHeaderDatabaseName, dbName)
		return req
	}

	// Happy path exchange issues a real token pair.
	resp, err = app.Test(exchangeRequest(code, redirectURI))
	if err != nil {
		t.Fatalf("exchange request: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("exchange status = %d, want 200, body=%s", resp.StatusCode, testutil.ReadBody(t, resp))
	}
	payload := testutil.DecodeJSON[map[string]any](t, resp.Body)
	if accessToken, _ := payload["access_token"].(string); accessToken == "" {
		t.Fatalf("expected non-empty access_token, got %#v", payload)
	}

	// Reusing the same code a second time must fail — single-use — and the
	// error body must be the RFC 6749 §5.2 {error, error_description} shape,
	// not the generic {"error": "<message>"} Fiber's default handler emits.
	resp, err = app.Test(exchangeRequest(code, redirectURI))
	if err != nil {
		t.Fatalf("reuse exchange request: %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("reused code status = %d, want 401", resp.StatusCode)
	}
	errPayload := testutil.DecodeJSON[map[string]any](t, resp.Body)
	if errPayload["error"] != "invalid_grant" {
		t.Fatalf("reused code error = %#v, want invalid_grant", errPayload)
	}
	if desc, _ := errPayload["error_description"].(string); desc == "" {
		t.Fatalf("expected non-empty error_description, got %#v", errPayload)
	}

	// Issue a fresh code, then exchange it with a mismatched redirect_uri.
	resp, err = app.Test(authorizeFormRequest(t, authorizeForm("authz_test_user", "secret")), fiber.TestConfig{Timeout: 0, FailOnTimeout: false})
	if err != nil {
		t.Fatalf("second login POST: %v", err)
	}
	location, err = url.Parse(resp.Header.Get("Location"))
	if err != nil {
		t.Fatalf("parse second redirect location: %v", err)
	}
	secondCode := location.Query().Get("code")
	resp, err = app.Test(exchangeRequest(secondCode, "https://evil.example/callback"))
	if err != nil {
		t.Fatalf("mismatched redirect_uri exchange: %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("mismatched redirect_uri status = %d, want 401", resp.StatusCode)
	}

	// The mismatched exchange above must have consumed the code (single-use
	// applies even on a rejected exchange), so retrying it with the correct
	// redirect_uri must also fail rather than succeeding on a second try.
	resp, err = app.Test(exchangeRequest(secondCode, redirectURI))
	if err != nil {
		t.Fatalf("retry after mismatch exchange: %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("retry-after-mismatch status = %d, want 401", resp.StatusCode)
	}

	// Expired code: issue one, force its expires_at into the past, confirm
	// exchange fails closed.
	resp, err = app.Test(authorizeFormRequest(t, authorizeForm("authz_test_user", "secret")), fiber.TestConfig{Timeout: 0, FailOnTimeout: false})
	if err != nil {
		t.Fatalf("third login POST: %v", err)
	}
	location, err = url.Parse(resp.Header.Get("Location"))
	if err != nil {
		t.Fatalf("parse third redirect location: %v", err)
	}
	thirdCode := location.Query().Get("code")
	if _, err := db.NewUpdate().Table("sys_oauth_auth_code").
		Set("expires_at = ?", time.Now().Add(-time.Minute)).
		Where("user_id = ?", userID).
		Where("used = FALSE").
		Exec(t.Context()); err != nil {
		t.Fatalf("force-expire code: %v", err)
	}
	resp, err = app.Test(exchangeRequest(thirdCode, redirectURI))
	if err != nil {
		t.Fatalf("expired code exchange: %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expired code status = %d, want 401", resp.StatusCode)
	}

	// response_type=id_token is invalid, but client_id/redirect_uri are still
	// trusted at this point (both are registered) — RFC 6749 §4.1.2.1 requires
	// this to redirect with error/error_description, not a bare 400.
	badTypeQuery := url.Values{
		"response_type": {"id_token"},
		"client_id":     {"gpt-test-client"},
		"redirect_uri":  {redirectURI},
		"state":         {"xyz123"},
		"db":            {dbName},
	}
	resp, err = app.Test(httpGet(t, "/oauth/authorize?"+badTypeQuery.Encode()), fiber.TestConfig{Timeout: 0, FailOnTimeout: false})
	if err != nil {
		t.Fatalf("unsupported response_type request: %v", err)
	}
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("unsupported response_type status = %d, want 302", resp.StatusCode)
	}
	badTypeLocation, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		t.Fatalf("parse unsupported response_type redirect: %v", err)
	}
	if got := badTypeLocation.Query().Get("error"); got != "unsupported_response_type" {
		t.Fatalf("error = %q, want unsupported_response_type", got)
	}
	if badTypeLocation.Query().Get("error_description") == "" {
		t.Fatalf("expected non-empty error_description in redirect")
	}
	if badTypeLocation.Query().Get("state") != "xyz123" {
		t.Fatalf("state = %q, want xyz123", badTypeLocation.Query().Get("state"))
	}

	// Implicit flow: after the same login UI, response_type=token redirects
	// with the OAuth fields in the fragment, not the query string.
	implicitForm := url.Values{
		"response_type": {"token"},
		"client_id":     {"gpt-test-client"},
		"redirect_uri":  {redirectURI},
		"state":         {"implicit123"},
		"db":            {dbName},
		"scope":         {"read write"},
		"username":      {"authz_test_user"},
		"password":      {"secret"},
	}
	resp, err = app.Test(authorizeFormRequest(t, implicitForm), fiber.TestConfig{Timeout: 0, FailOnTimeout: false})
	if err != nil {
		t.Fatalf("implicit login POST: %v", err)
	}
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("implicit login status = %d, want 302", resp.StatusCode)
	}
	implicitLocation, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		t.Fatalf("parse implicit redirect location: %v", err)
	}
	if implicitLocation.Query().Get("access_token") != "" {
		t.Fatalf("implicit access_token must be in fragment, got query location %s", implicitLocation)
	}
	fragment, err := url.ParseQuery(implicitLocation.Fragment)
	if err != nil {
		t.Fatalf("parse implicit redirect fragment: %v", err)
	}
	implicitAccessToken := fragment.Get("access_token")
	if implicitAccessToken == "" {
		t.Fatalf("expected access_token in implicit redirect fragment, got %s", implicitLocation)
	}
	if fragment.Get("token_type") != "Bearer" || fragment.Get("expires_in") == "" || fragment.Get("state") != "implicit123" || fragment.Get("scope") != "read write" {
		t.Fatalf("unexpected implicit fragment values: %#v", fragment)
	}
	implicitIdentity, err := baseauth.CurrentProvider().Validate(t.Context(), implicitAccessToken)
	if err != nil {
		t.Fatalf("validate implicit access token: %v", err)
	}
	if implicitIdentity.UserID != userID || implicitIdentity.AuthMode != "oauth_implicit" {
		t.Fatalf("unexpected implicit identity: %#v", implicitIdentity)
	}

	// PKCE (RFC 7636), optional-if-present: a code issued with a code_challenge
	// exchanges successfully when the matching code_verifier is presented, and
	// fails closed when it isn't.
	codeVerifier := "this-is-a-valid-code-verifier-with-enough-entropy-12345"
	sum := sha256.Sum256([]byte(codeVerifier))
	codeChallenge := base64.RawURLEncoding.EncodeToString(sum[:])
	pkceAuthorizeForm := url.Values{
		"response_type":         {"code"},
		"client_id":             {"gpt-test-client"},
		"redirect_uri":          {redirectURI},
		"state":                 {"pkce123"},
		"db":                    {dbName},
		"username":              {"authz_test_user"},
		"password":              {"secret"},
		"code_challenge":        {codeChallenge},
		"code_challenge_method": {"S256"},
	}

	resp, err = app.Test(authorizeFormRequest(t, pkceAuthorizeForm), fiber.TestConfig{Timeout: 0, FailOnTimeout: false})
	if err != nil {
		t.Fatalf("pkce login POST: %v", err)
	}
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("pkce login status = %d, want 302", resp.StatusCode)
	}
	pkceLocation, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		t.Fatalf("parse pkce redirect location: %v", err)
	}
	pkceCode := pkceLocation.Query().Get("code")
	if pkceCode == "" {
		t.Fatalf("expected non-empty code in pkce redirect, got %s", pkceLocation)
	}

	exchangeWithVerifier := func(code, redirectURI, verifier string) *http.Request {
		req := formRequest(t, "/oauth/token", url.Values{
			"grant_type":    {"authorization_code"},
			"client_id":     {"gpt-test-client"},
			"client_secret": {clientSecret},
			"code":          {code},
			"redirect_uri":  {redirectURI},
			"code_verifier": {verifier},
		})
		req.Header.Set(internalroutes.RPCHeaderDatabaseName, dbName)
		return req
	}

	// Wrong code_verifier must fail closed and consume the code.
	resp, err = app.Test(exchangeWithVerifier(pkceCode, redirectURI, "the-wrong-verifier-value-entirely-000000000000000"))
	if err != nil {
		t.Fatalf("pkce mismatch exchange: %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("pkce mismatch status = %d, want 401", resp.StatusCode)
	}

	// A second PKCE code, exchanged with the correct verifier, must succeed.
	resp, err = app.Test(authorizeFormRequest(t, pkceAuthorizeForm), fiber.TestConfig{Timeout: 0, FailOnTimeout: false})
	if err != nil {
		t.Fatalf("second pkce login POST: %v", err)
	}
	pkceLocation2, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		t.Fatalf("parse second pkce redirect location: %v", err)
	}
	pkceCode2 := pkceLocation2.Query().Get("code")
	resp, err = app.Test(exchangeWithVerifier(pkceCode2, redirectURI, codeVerifier))
	if err != nil {
		t.Fatalf("pkce happy-path exchange: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("pkce happy-path status = %d, want 200, body=%s", resp.StatusCode, testutil.ReadBody(t, resp))
	}
	pkcePayload := testutil.DecodeJSON[map[string]any](t, resp.Body)
	if accessToken, _ := pkcePayload["access_token"].(string); accessToken == "" {
		t.Fatalf("expected non-empty access_token, got %#v", pkcePayload)
	}
}

func httpGet(t *testing.T, path string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, path, nil)
	if err != nil {
		t.Fatalf("build GET request: %v", err)
	}
	return req
}

// testAuthorizeCSRFToken is a well-formed (43-char) double-submit token for tests that POST the
// sign-in form directly instead of first loading it.
const testAuthorizeCSRFToken = "dGVzdC1hdXRob3JpemUtY3NyZi10b2tlbi0wMTIzNDU"

// authorizeFormRequest builds a sign-in POST that carries the CSRF token in both the form and
// the cookie, as a browser does after loading the GET form.
func authorizeFormRequest(t *testing.T, form url.Values) *http.Request {
	t.Helper()
	withToken := url.Values{}
	for key, values := range form {
		withToken[key] = values
	}
	withToken.Set("csrf_token", testAuthorizeCSRFToken)
	req := formRequest(t, "/oauth/authorize", withToken)
	req.AddCookie(&http.Cookie{Name: authorizeCSRFCookie, Value: testAuthorizeCSRFToken})
	return req
}
