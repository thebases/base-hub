package cmd

import (
	"io"
	"net/http/httptest"
	"testing"
	"time"

	"git.thebase.vn/base_hub/internal/config"
	"github.com/gofiber/fiber/v3"
)

// clientIPFor answers one request through an app built from serverFiberConfig
// and returns what c.IP() resolved. app.Test peers come from 0.0.0.0.
func clientIPFor(t *testing.T, server config.ServerConfig, xff string) string {
	t.Helper()
	app := fiber.New(serverFiberConfig(server))
	app.Get("/ip", func(c fiber.Ctx) error { return c.SendString(c.IP()) })
	req := httptest.NewRequest(fiber.MethodGet, "/ip", nil)
	if xff != "" {
		req.Header.Set(fiber.HeaderXForwardedFor, xff)
	}
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	return string(body)
}

func TestServerProxyTrust(t *testing.T) {
	// No trusted proxy: a client-sent X-Forwarded-For is ignored.
	if got := clientIPFor(t, config.ServerConfig{}, "203.0.113.7"); got == "203.0.113.7" {
		t.Fatalf("untrusted peer chose its own IP via X-Forwarded-For")
	}
	// Trusted proxy: the rightmost untrusted hop wins, so a spoofed leftmost
	// entry the proxy appended to is not used.
	trusted := config.ServerConfig{TrustedProxies: []string{" 0.0.0.0/32 ", "10.0.0.0/8"}}
	if got := clientIPFor(t, trusted, "198.51.100.1, 203.0.113.7, 10.0.0.5"); got != "203.0.113.7" {
		t.Fatalf("client IP behind trusted proxies = %q, want 203.0.113.7", got)
	}
}

func TestServerFiberConfigTimeouts(t *testing.T) {
	cfg := serverFiberConfig(config.ServerConfig{})
	if cfg.ReadTimeout != defaultServerReadTimeout || cfg.IdleTimeout != defaultServerIdleTimeout || cfg.WriteTimeout != 0 {
		t.Fatalf("default timeouts = %v/%v/%v", cfg.ReadTimeout, cfg.IdleTimeout, cfg.WriteTimeout)
	}
	cfg = serverFiberConfig(config.ServerConfig{ReadTimeoutS: 5, IdleTimeoutS: 7})
	if cfg.ReadTimeout != 5*time.Second || cfg.IdleTimeout != 7*time.Second {
		t.Fatalf("configured timeouts = %v/%v", cfg.ReadTimeout, cfg.IdleTimeout)
	}
}
