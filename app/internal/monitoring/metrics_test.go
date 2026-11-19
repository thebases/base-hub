package monitoring

import (
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/gofiber/fiber/v3"
)

func TestRegisterMetricsIsIdempotent(t *testing.T) {
	metricsRegisterOnce = sync.Once{}
	defer func() { metricsRegisterOnce = sync.Once{} }()

	// Prometheus registration is global and MustRegister panics on duplicates,
	// so this test asserts our sync.Once guard absorbs repeated bootstrap calls.
	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("RegisterMetrics should be idempotent, panic=%v", recovered)
		}
	}()
	RegisterMetrics()
	RegisterMetrics()
}

// TestMiddlewareCountsErrorStatusFromErrorHandler covers K-11: a handler that
// returns an error must be counted with the status the app error handler
// writes, not the 200 default present before it ran.
func TestMiddlewareCountsErrorStatusFromErrorHandler(t *testing.T) {
	app := fiber.New()
	app.Use(Middleware())
	app.Get("/boom", func(fiber.Ctx) error { return fiber.NewError(fiber.StatusServiceUnavailable, "down") })

	before := ErrorCount()
	resp, err := app.Test(httptest.NewRequest(fiber.MethodGet, "/boom", nil))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if resp.StatusCode != fiber.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 from the error handler", resp.StatusCode)
	}
	if got := ErrorCount() - before; got != 1 {
		t.Fatalf("error count delta = %d, want 1", got)
	}
}
