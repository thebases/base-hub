package routes

import (
	"encoding/json"
	"strings"
	"testing"

	"git.thebase.vn/base_hub/internal/config"
)

// The server's MQTT account must never reach a browser, even when browser MQTT
// is configured (plan T10): the advertisement stays on WebSocket/SSE.
func TestServiceBusTransportConfigNeverCarriesBrokerCredentials(t *testing.T) {
	previous := config.Get().MqttClient
	t.Cleanup(func() { config.Update(func(cfg *config.AppConfig) { cfg.MqttClient = previous }) })
	config.Update(func(cfg *config.AppConfig) {
		cfg.MqttClient.Enable = true
		cfg.MqttClient.PublicWSURL = "wss://broker.example.invalid/mqtt"
		cfg.MqttClient.Username = "server-account"
		cfg.MqttClient.Password = "server-secret-password"
		cfg.MqttClient.ClientID = "server-client-id"
	})

	raw, err := json.Marshal(serviceBusTransportConfig())
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	for _, leaked := range []string{"password", "username", "client_id", "server-account", "server-secret-password", "server-client-id", "broker.example.invalid", `"mqtt"`} {
		if strings.Contains(body, leaked) {
			t.Fatalf("transport config leaks %q: %s", leaked, body)
		}
	}
	if !strings.Contains(body, `"transport":"websocket"`) || !strings.Contains(body, "/bus/ws") || !strings.Contains(body, "/bus/sse") {
		t.Fatalf("transport config must advertise websocket with the SSE fallback: %s", body)
	}
}
