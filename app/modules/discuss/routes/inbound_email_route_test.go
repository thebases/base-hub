package routes

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"git.thebase.vn/base_hub/internal/config"
	"git.thebase.vn/base_hub/internal/orm"
	"github.com/gofiber/fiber/v3"
)

// postInbound sends a webhook call whose body is deliberately malformed: a
// request that passes the secret check stops at 400 (bind error) before any
// DB access, so the status alone shows which gate answered.
func postInbound(t *testing.T, secret, target, header string) int {
	t.Helper()
	config.Update(func(c *config.AppConfig) { c.App.DiscussInboundWebhookSecret = secret })
	app := fiber.New(fiber.Config{ErrorHandler: statusFromTypedError})
	RegisterRoutes(app)
	req := httptest.NewRequest(http.MethodPost, target, strings.NewReader("{not json"))
	req.Header.Set("Content-Type", "application/json")
	if header != "" {
		req.Header.Set(inboundWebhookSecretHeader, header)
	}
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	return resp.StatusCode
}

// statusFromTypedError answers with the typed error's HTTP status, as the app-wide
// APIErrorHandler does (importing internal/routes would pull every module into this test).
func statusFromTypedError(c fiber.Ctx, err error) error {
	return c.SendStatus(orm.ErrorFrom(err).Status)
}

func TestInboundEmailWebhookSecret(t *testing.T) {
	prev := config.Get().App.DiscussInboundWebhookSecret
	t.Cleanup(func() { config.Update(func(c *config.AppConfig) { c.App.DiscussInboundWebhookSecret = prev }) })

	cases := []struct {
		name, secret, target, header string
		want                         int
	}{
		{"no secret configured", "", "/discuss/inbound_email", "", http.StatusForbidden},
		{"no secret configured, header sent", "", "/discuss/inbound_email", "anything", http.StatusForbidden},
		{"missing header", "s3cret", "/discuss/inbound_email", "", http.StatusForbidden},
		{"wrong header", "s3cret", "/discuss/inbound_email", "s3cre", http.StatusForbidden},
		{"query string secret no longer accepted", "s3cret", "/discuss/inbound_email?secret=s3cret", "", http.StatusForbidden},
		{"correct header passes the gate", "s3cret", "/discuss/inbound_email", "s3cret", http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := postInbound(t, tc.secret, tc.target, tc.header); got != tc.want {
				t.Fatalf("status = %d, want %d", got, tc.want)
			}
		})
	}
}
