package routes

// File overview:
// - Responsibility: verifies the JSON-RPC error format and request-id echo.

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"git.thebase.vn/base_hub/internal/base"
	"git.thebase.vn/base_hub/internal/orm"
	"github.com/gofiber/fiber/v3"
)

// newErrorTestApp mounts routes that fail in each way the handler must classify.
func newErrorTestApp() *fiber.App {
	app := fiber.New(fiber.Config{JSONEncoder: base.JSONMarshal, ErrorHandler: APIErrorHandler})
	app.Use(RecoverMiddleware)
	app.Get("/typed", func(c fiber.Ctx) error {
		setRequestID(c, c.Query("id"))
		return orm.NewError(orm.ErrNotFound, 0, "device not found", map[string]any{"field": "serial"})
	})
	app.Get("/plain", func(c fiber.Ctx) error { return errors.New("boom") })
	app.Get("/fiber", func(c fiber.Ctx) error { return fiber.NewError(fiber.StatusForbidden, "nope") })
	app.Get("/alias", func(c fiber.Ctx) error {
		return &orm.AliasError{Status: 409, Code: "conflict", Message: "state clash"}
	})
	app.Get("/panic", func(c fiber.Ctx) error { panic("secret internals") })
	return app
}

// callErrorTestApp runs one request and decodes the JSON body when there is one.
func callErrorTestApp(t *testing.T, app *fiber.App, path string) (*http.Response, string, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request %s: %v", path, err)
	}
	raw, _ := io.ReadAll(resp.Body)
	var body map[string]any
	_ = json.Unmarshal(raw, &body)
	return resp, string(raw), body
}

func TestAPIErrorV2Shapes(t *testing.T) {
	app := newErrorTestApp()
	cases := []struct {
		path   string
		status int
		code   float64
	}{
		{"/typed", 404, orm.ErrNotFound},
		{"/plain", 400, orm.ErrServer},
		{"/fiber", 403, orm.ErrForbidden},
		{"/alias", 409, orm.ErrConflict},
		{"/panic", 500, orm.ErrInternal},
		{"/missing", 404, orm.ErrNotFound},
	}
	for _, tc := range cases {
		resp, raw, body := callErrorTestApp(t, app, tc.path)
		if resp.StatusCode != tc.status {
			t.Fatalf("%s: status = %d, want %d (%s)", tc.path, resp.StatusCode, tc.status, raw)
		}
		if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
			t.Fatalf("%s: content-type = %q", tc.path, ct)
		}
		errBody, _ := body["error"].(map[string]any)
		if errBody["code"] != tc.code {
			t.Fatalf("%s: code = %v, want %v (%s)", tc.path, errBody["code"], tc.code, raw)
		}
		if id, _ := body["id"].(string); !requestIDPattern.MatchString(id) && tc.path != "/typed" {
			t.Fatalf("%s: id = %v, want a generated 24-digit id", tc.path, body["id"])
		}
	}
}

func TestAPIErrorV2PanicDoesNotLeak(t *testing.T) {
	_, raw, body := callErrorTestApp(t, newErrorTestApp(), "/panic")
	if strings.Contains(raw, "secret") {
		t.Fatalf("panic text leaked: %s", raw)
	}
	if msg := body["error"].(map[string]any)["message"]; msg != orm.InternalErrorMessage {
		t.Fatalf("message = %v", msg)
	}
}

func TestAPIErrorV2EchoesIDAndData(t *testing.T) {
	_, raw, body := callErrorTestApp(t, newErrorTestApp(), "/typed?id=7")
	if body["id"] != "7" {
		t.Fatalf("id = %v, want echoed (%s)", body["id"], raw)
	}
	data := body["error"].(map[string]any)["data"].(map[string]any)
	if data["field"] != "serial" {
		t.Fatalf("data = %v", data)
	}
}

// requestIDPattern is the generated id: ddmmyyyyhhMMss + ms + instance + seq (24 digits).
var requestIDPattern = regexp.MustCompile(`^\d{24}$`)

func TestGenerateRequestIDFormat(t *testing.T) {
	now := time.Date(2026, 10, 11, 15, 35, 48, 123_000_000, time.UTC)
	seen := map[string]bool{}
	for i := 0; i < 5000; i++ {
		id := generateRequestID(now)
		if !requestIDPattern.MatchString(id) || id[:17] != "11102026153548123" {
			t.Fatalf("id = %q, want 11102026153548123 + 7 digits (instance+seq)", id)
		}
		if seen[id] {
			t.Fatalf("duplicate id %q within one millisecond", id)
		}
		seen[id] = true
	}
}

func TestSetRequestIDDropsNonScalar(t *testing.T) {
	app := fiber.New()
	app.Get("/", func(c fiber.Ctx) error {
		setRequestID(c, map[string]any{"a": 1})
		if id, _ := requestID(c).(string); !requestIDPattern.MatchString(id) {
			t.Fatalf("non-scalar id kept or no generated id: %v", requestID(c))
		}
		return nil
	})
	if _, err := app.Test(httptest.NewRequest(http.MethodGet, "/", nil)); err != nil {
		t.Fatal(err)
	}
}

func TestSetRequestIDAlwaysString(t *testing.T) {
	app := fiber.New()
	cases := []struct {
		in   any
		want string // "" means a generated id
	}{{"abc", "abc"}, {float64(7), "7"}, {float64(1.5), "1.5"}, {true, "true"}, {nil, ""}, {"", ""}, {map[string]any{"a": 1}, ""}}
	app.Get("/", func(c fiber.Ctx) error {
		for _, tc := range cases {
			c.Locals(localsRequestID, nil)
			setRequestID(c, tc.in)
			got, ok := requestID(c).(string)
			if !ok || (tc.want != "" && got != tc.want) || (tc.want == "" && !requestIDPattern.MatchString(got)) {
				t.Errorf("setRequestID(%#v) -> %#v, want string %q", tc.in, requestID(c), tc.want)
			}
		}
		return nil
	})
	if _, err := app.Test(httptest.NewRequest(http.MethodGet, "/", nil)); err != nil {
		t.Fatal(err)
	}
}
