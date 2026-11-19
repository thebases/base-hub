package routes

// File overview:
// - Responsibility: verifies GET /md/menu.json degrades to an empty tree (200)
//   when the knowledge module isn't installed, instead of the kernel guard's 404.

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"
)

func TestMarkdownMenuRouteEmptyWhenNotInstalled(t *testing.T) {
	app := fiber.New()
	RegisterMarkdownMenuRoute(app, MarkdownRouteOptions{
		// PublicRootForDB is never reached: the install check short-circuits first.
		Installed: func(context.Context) (bool, error) { return false, nil },
	})

	resp, err := app.Test(httptest.NewRequest("GET", "/md/menu.json", nil))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	raw, _ := io.ReadAll(resp.Body)
	var envelope struct {
		Result markdownMenuResponse `json:"result"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatalf("invalid json %q: %v", raw, err)
	}
	payload := envelope.Result
	if payload.Items == nil || len(payload.Items) != 0 {
		t.Fatalf("expected empty items array, got %s", raw)
	}
}
