package cmd

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/static"
)

// TestWithModTimeRevalidation guards the stale-module bug: a zero-mtime FS
// (like embed.FS) must not answer a pre-deploy If-Modified-Since with 304.
func TestWithModTimeRevalidation(t *testing.T) {
	zero := fstest.MapFS{"app.js": {Data: []byte("console.log(1)")}}
	app := fiber.New()
	app.Use("/", static.New("", static.Config{FS: withModTime(zero), MaxAge: 3600}))

	get := func(ims string) (int, string) {
		req := httptest.NewRequest(fiber.MethodGet, "/app.js", nil)
		if ims != "" {
			req.Header.Set(fiber.HeaderIfModifiedSince, ims)
		}
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp.StatusCode, resp.Header.Get(fiber.HeaderLastModified)
	}

	status, lastModified := get("")
	if status != fiber.StatusOK || lastModified != embedFSModTime.Format(http.TimeFormat) {
		t.Fatalf("first GET: status=%d last-modified=%q", status, lastModified)
	}
	// A copy cached from an older build must be re-sent, not 304'd.
	if status, _ := get(embedFSModTime.Add(-time.Hour).Format(http.TimeFormat)); status != fiber.StatusOK {
		t.Fatalf("older If-Modified-Since: status=%d, want 200", status)
	}
	// Unchanged since this build: a cheap 304 still works.
	if status, _ := get(lastModified); status != fiber.StatusNotModified {
		t.Fatalf("current If-Modified-Since: status=%d, want 304", status)
	}
}
