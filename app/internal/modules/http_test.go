package modules

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"
)

type httpTestModule struct {
	name    string
	visited *[]string
}

func (m httpTestModule) Manifest() Manifest {
	return Manifest{Name: m.name}
}

func (m httpTestModule) Bootstrap(_ context.Context, _ *BootstrapContext) error {
	return nil
}

func (m httpTestModule) RegisterRoutes(router fiber.Router) {
	*m.visited = append(*m.visited, m.name)
	router.Get("/"+m.name, func(c fiber.Ctx) error {
		return c.SendString(m.name)
	})
}

type bootstrapOnlyModule struct {
	name string
}

func (m bootstrapOnlyModule) Manifest() Manifest {
	return Manifest{Name: m.name}
}

func (m bootstrapOnlyModule) Bootstrap(_ context.Context, _ *BootstrapContext) error {
	return nil
}

func TestRegisterHTTPRoutesUsesModuleRegistrarsInNameOrder(t *testing.T) {
	registryMu.Lock()
	previous := registry
	registryMu.Unlock()
	defer func() {
		registryMu.Lock()
		registry = previous
		registryMu.Unlock()
	}()

	visited := make([]string, 0, 2)

	registryMu.Lock()
	registry = map[string]Module{
		"workflow": httpTestModule{name: "workflow", visited: &visited},
		"base":     httpTestModule{name: "base", visited: &visited},
		"plain":    bootstrapOnlyModule{name: "plain"},
	}
	registryMu.Unlock()

	app := fiber.New()
	RegisterHTTPRoutes(app)

	if len(visited) != 2 {
		t.Fatalf("visited count = %d, want 2 (%v)", len(visited), visited)
	}
	if visited[0] != "base" || visited[1] != "workflow" {
		t.Fatalf("visited order = %v, want [base workflow]", visited)
	}

	req := httptest.NewRequest(fiber.MethodGet, "/base", nil)
	resp, err := app.Test(req, fiber.TestConfig{Timeout: 0, FailOnTimeout: false})
	if err != nil {
		t.Fatalf("app.Test() error = %v", err)
	}
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, fiber.StatusOK)
	}
}
