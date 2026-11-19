package routes

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"git.thebase.vn/base_hub/internal/config"
	"github.com/gofiber/fiber/v3"
)

// Plan T36 / SEC-T-18: the sign-in form is never framed or cached, carries a double-submit CSRF
// token, and a POST is accepted only when the form token matches the cookie.
func TestAuthorizeFormCSRFAndFrameHeaders(t *testing.T) {
	app := fiber.New()
	app.Get("/form", func(c fiber.Ctx) error {
		return renderAuthorizeForm(c, authorizeParams{ClientID: "client"}, "")
	})
	app.Post("/check", func(c fiber.Ctx) error {
		if validAuthorizeCSRFToken(c) {
			return c.SendString("ok")
		}
		return c.SendStatus(fiber.StatusForbidden)
	})

	resp, err := app.Test(httpGet(t, "/form"))
	if err != nil {
		t.Fatal(err)
	}
	if resp.Header.Get("X-Frame-Options") != "DENY" || !strings.Contains(resp.Header.Get("Content-Security-Policy"), "frame-ancestors 'none'") || resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("missing frame/cache headers: %v", resp.Header)
	}
	var cookie *http.Cookie
	for _, candidate := range resp.Cookies() {
		if candidate.Name == authorizeCSRFCookie {
			cookie = candidate
		}
	}
	if cookie == nil || len(cookie.Value) != 43 || !cookie.HttpOnly {
		t.Fatalf("want an HttpOnly 43-char CSRF cookie, got %#v", cookie)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), `name="csrf_token" value="`+cookie.Value+`"`) {
		t.Fatalf("form does not carry the cookie token: %s", body)
	}

	// A second render with the cookie present reuses the token (other open tabs stay valid).
	again := httpGet(t, "/form")
	again.AddCookie(cookie)
	resp, err = app.Test(again)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	if !strings.Contains(string(body), `value="`+cookie.Value+`"`) {
		t.Fatal("re-render must reuse the existing token")
	}

	post := func(formToken string, withCookie bool) int {
		t.Helper()
		req := formRequest(t, "/check", url.Values{"csrf_token": {formToken}})
		if withCookie {
			req.AddCookie(cookie)
		}
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp.StatusCode
	}
	if got := post(cookie.Value, true); got != http.StatusOK {
		t.Fatalf("matching token: got %d", got)
	}
	if got := post(cookie.Value, false); got != http.StatusForbidden {
		t.Fatalf("no cookie (cross-site post): got %d", got)
	}
	if got := post(strings.Repeat("A", 43), true); got != http.StatusForbidden {
		t.Fatalf("wrong token: got %d", got)
	}
	if got := post("", true); got != http.StatusForbidden {
		t.Fatalf("missing token: got %d", got)
	}
}

// Production (or HTTPS) uses the `__Host-` cookie, always Secure, and validates against it.
func TestAuthorizeFormCSRFCookieSecureInProduction(t *testing.T) {
	previous := config.Get().App.Env
	config.Update(func(c *config.AppConfig) { c.App.Env = "production" })
	t.Cleanup(func() { config.Update(func(c *config.AppConfig) { c.App.Env = previous }) })

	app := fiber.New()
	app.Get("/form", func(c fiber.Ctx) error {
		return renderAuthorizeForm(c, authorizeParams{ClientID: "client"}, "")
	})
	app.Post("/check", func(c fiber.Ctx) error {
		if validAuthorizeCSRFToken(c) {
			return c.SendString("ok")
		}
		return c.SendStatus(fiber.StatusForbidden)
	})
	resp, err := app.Test(httpGet(t, "/form"))
	if err != nil {
		t.Fatal(err)
	}
	var cookie *http.Cookie
	for _, candidate := range resp.Cookies() {
		if candidate.Name == authorizeCSRFCookieSecure {
			cookie = candidate
		}
	}
	if cookie == nil || !cookie.Secure || !cookie.HttpOnly || cookie.Path != "/" {
		t.Fatalf("want a Secure HttpOnly __Host- cookie, got %#v", resp.Cookies())
	}
	// The plain-name cookie (e.g. planted over HTTP or by a sibling subdomain) is not accepted.
	req := formRequest(t, "/check", url.Values{"csrf_token": {cookie.Value}})
	req.AddCookie(&http.Cookie{Name: authorizeCSRFCookie, Value: cookie.Value})
	if resp, _ := app.Test(req); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("plain cookie accepted in production: %d", resp.StatusCode)
	}
	req = formRequest(t, "/check", url.Values{"csrf_token": {cookie.Value}})
	req.AddCookie(cookie)
	if resp, _ := app.Test(req); resp.StatusCode != http.StatusOK {
		t.Fatalf("__Host- cookie refused: %d", resp.StatusCode)
	}
}
