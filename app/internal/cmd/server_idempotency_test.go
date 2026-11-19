package cmd

import (
	"io"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/idempotency"
)

const testIdempotencyKey = "00000000-0000-0000-0000-000000000042"

// newIdempotencyTestApp wires scopeIdempotencyKey + the fiber middleware the
// way buildServerApp does (in-memory storage) in front of a handler that
// returns a fresh counter value on every real execution.
func newIdempotencyTestApp(t *testing.T) *fiber.App {
	t.Helper()
	var calls atomic.Int64
	app := fiber.New()
	app.Use(scopeIdempotencyKey)
	app.Use(idempotency.New(idempotency.Config{
		Next:              shouldSkipIdempotencyMiddleware,
		KeyHeader:         idempotencyKeyHeader,
		KeyHeaderValidate: validateIdempotencyKey,
	}))
	handler := func(c fiber.Ctx) error {
		return c.SendString(strconv.FormatInt(calls.Add(1), 10))
	}
	app.Post("/rpc/x/create", handler)
	app.Post("/oauth/token", handler)
	app.Post("/auth/token", handler)
	return app
}

func doIdempotencyRequest(t *testing.T, app *fiber.App, path, auth, key string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(fiber.MethodPost, path, nil)
	if auth != "" {
		req.Header.Set(fiber.HeaderAuthorization, auth)
	}
	if key != "" {
		req.Header.Set(idempotencyKeyHeader, key)
	}
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request %s: %v", path, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

func TestIdempotencyKeyScopedToCredential(t *testing.T) {
	app := newIdempotencyTestApp(t)

	_, first := doIdempotencyRequest(t, app, "/rpc/x/create", "Bearer alice", testIdempotencyKey)
	_, replay := doIdempotencyRequest(t, app, "/rpc/x/create", "Bearer alice", testIdempotencyKey)
	if replay != first {
		t.Fatalf("same caller, same key: want replay %q, got %q", first, replay)
	}

	_, other := doIdempotencyRequest(t, app, "/rpc/x/create", "Bearer mallory", testIdempotencyKey)
	if other == first {
		t.Fatalf("another caller with the same key received the first caller's cached response %q", first)
	}
}

func TestIdempotencyKeyNotAppliedToOAuthOrAnonymous(t *testing.T) {
	app := newIdempotencyTestApp(t)

	for _, path := range []string{"/oauth/token", "/auth/token", "/OAuth/token"} {
		_, a := doIdempotencyRequest(t, app, path, "Bearer stale", testIdempotencyKey)
		_, b := doIdempotencyRequest(t, app, path, "Bearer stale", testIdempotencyKey)
		if a == b {
			t.Fatalf("%s response was replayed from the idempotency cache: %q", path, a)
		}
	}

	_, c := doIdempotencyRequest(t, app, "/rpc/x/create", "", testIdempotencyKey)
	_, d := doIdempotencyRequest(t, app, "/rpc/x/create", "", testIdempotencyKey)
	if c == d {
		t.Fatalf("anonymous request was replayed from the idempotency cache: %q", c)
	}
}

func TestIdempotencyKeyRejectsBadLengthAndPreScopedKeys(t *testing.T) {
	app := newIdempotencyTestApp(t)

	if status, _ := doIdempotencyRequest(t, app, "/rpc/x/create", "Bearer alice", "short"); status != fiber.StatusBadRequest {
		t.Fatalf("short key: want 400, got %d", status)
	}

	// A client-sent value shaped like a scoped key is treated as a raw key
	// (wrong length) and never reaches the cache as-is.
	forged := scopedIdempotencyKey("home", "authorization:Bearer alice", fiber.MethodPost, "/rpc/x/create", testIdempotencyKey)
	if status, _ := doIdempotencyRequest(t, app, "/rpc/x/create", "Bearer mallory", forged); status != fiber.StatusBadRequest {
		t.Fatalf("pre-scoped key: want 400, got %d", status)
	}
}

func TestScopedIdempotencyKeyDiffersPerPart(t *testing.T) {
	base := scopedIdempotencyKey("db1", "cred", "POST", "/a", testIdempotencyKey)
	for name, got := range map[string]string{
		"db":     scopedIdempotencyKey("db2", "cred", "POST", "/a", testIdempotencyKey),
		"cred":   scopedIdempotencyKey("db1", "cred2", "POST", "/a", testIdempotencyKey),
		"method": scopedIdempotencyKey("db1", "cred", "PUT", "/a", testIdempotencyKey),
		"path":   scopedIdempotencyKey("db1", "cred", "POST", "/b", testIdempotencyKey),
	} {
		if got == base {
			t.Fatalf("changing %s did not change the scoped key", name)
		}
	}
	if err := validateIdempotencyKey(base); err != nil {
		t.Fatalf("scoped key rejected by validator: %v", err)
	}
	if err := validateIdempotencyKey(testIdempotencyKey); err == nil {
		t.Fatal("raw client key accepted by validator")
	}
}
