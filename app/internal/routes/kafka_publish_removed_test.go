package routes

import (
	"net/http/httptest"
	"strings"
	"testing"

	"git.thebase.vn/base_hub/internal/config"
	"github.com/gofiber/fiber/v3"
)

// TestKafkaPublishRouteRemoved guards SEC-T-1: with Kafka enabled there must
// be no HTTP route that publishes request bodies to Kafka topics (bank IPN
// topics are consumed as trusted events).
func TestKafkaPublishRouteRemoved(t *testing.T) {
	previous := *config.Get()
	t.Cleanup(func() { config.Set(previous) })
	config.Update(func(c *config.AppConfig) {
		c.Kafka.Enable = true
		c.MqttClient.Enable = false
	})

	app := fiber.New()
	registerOptionalTransportRoutes(app)
	for _, topic := range []string{"bank_ipn_topic", "ipn_napas", "ipn_bidv"} {
		req := httptest.NewRequest("POST", "/publish/"+topic, strings.NewReader(`{"amount":1}`))
		req.Header.Set("Content-Type", "application/json")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("app.Test: %v", err)
		}
		if resp.StatusCode != fiber.StatusNotFound {
			t.Fatalf("POST /publish/%s status = %d, want 404", topic, resp.StatusCode)
		}
	}
}
