package mqtt

// File overview:
// - Responsibility: publishes over an MQTT 5 connection so the caller learns whether the broker
//   routed the message to at least one subscriber (PUBACK reason code 0x10 = no matching subscribers).
// - Key symbols: `PublishWithReceipt`, `PublishReceipt`.
// - Pattern: MQTT 3.1.1 (paho.mqtt.golang, used by the rest of this package) has no PUBACK reason
//   codes, so this keeps a separate, lazily dialed v5 connection used only for receipt-aware publishes.
//   Broker address, credentials, and TLS come from the same `mqtt` config as the v3 client.

import (
	"context"
	"fmt"
	"net/url"
	"sync"

	"git.thebase.vn/base_hub/internal/config"
	"git.thebase.vn/base_hub/internal/services/log"
	"github.com/eclipse/paho.golang/autopaho"
	"github.com/eclipse/paho.golang/packets"
	"github.com/eclipse/paho.golang/paho"
	"github.com/google/uuid"
)

var (
	mqtt5Manager *autopaho.ConnectionManager
	mqtt5Mu      sync.Mutex
)

// PublishReceipt is the broker's QoS 1 acknowledgement for one publish.
// HasSubscribers reflects broker routing at publish time (a subscription matched),
// not an end-to-end acknowledgement from the receiving client.
type PublishReceipt struct {
	ReasonCode     byte
	HasSubscribers bool
}

// PublishWithReceipt publishes payload at QoS 1 over MQTT 5 and reports whether
// any subscriber matched. A context timeout after the PUBLISH was written leaves
// delivery unknown — callers must not blindly retry money-moving messages.
func PublishWithReceipt(ctx context.Context, topic string, payload []byte) (PublishReceipt, error) {
	manager, err := mqtt5Connection()
	if err != nil {
		return PublishReceipt{}, err
	}
	// Bound the wait for a (re)connect by the caller's context instead of failing on a transient drop.
	if err := manager.AwaitConnection(ctx); err != nil {
		return PublishReceipt{}, fmt.Errorf("mqtt5 not connected: %w", err)
	}
	resp, err := manager.Publish(ctx, &paho.Publish{Topic: topic, QoS: 1, Payload: payload})
	// paho already turns reason codes >= 0x80 (not authorized, quota, ...) into an error.
	if err != nil {
		return PublishReceipt{}, fmt.Errorf("publish error: %w", err)
	}
	return PublishReceipt{
		ReasonCode:     resp.ReasonCode,
		HasSubscribers: resp.ReasonCode != packets.PubackNoMatchingSubscribers,
	}, nil
}

// mqtt5Connection returns the shared v5 connection manager, dialing it on first use.
// autopaho owns reconnects afterwards, so the manager is created exactly once per process.
func mqtt5Connection() (*autopaho.ConnectionManager, error) {
	mqtt5Mu.Lock()
	defer mqtt5Mu.Unlock()
	if mqtt5Manager != nil {
		return mqtt5Manager, nil
	}
	if !config.Get().MqttClient.Enable {
		return nil, fmt.Errorf("mqtt client is disabled")
	}
	cfg, err := mqtt5ClientConfig()
	if err != nil {
		return nil, err
	}
	// Background context: the connection must outlive the request that first triggered it.
	manager, err := autopaho.NewConnection(context.Background(), cfg)
	if err != nil {
		return nil, fmt.Errorf("mqtt5 connect: %w", err)
	}
	mqtt5Manager = manager
	return manager, nil
}

// mqtt5ClientConfig mirrors InitMQTT's broker/auth/TLS settings for the v5 connection.
func mqtt5ClientConfig() (autopaho.ClientConfig, error) {
	brokerURL, err := url.Parse(mqttBrokerURL())
	if err != nil {
		return autopaho.ClientConfig{}, fmt.Errorf("mqtt5 broker url: %w", err)
	}
	// Distinct client id so the v5 session never kicks the v3 client off the broker.
	clientID := config.Get().MqttClient.ClientID + "_v5_" + uuid.NewString()[:8]
	cfg := autopaho.ClientConfig{
		ServerUrls:                    []*url.URL{brokerURL},
		KeepAlive:                     30,
		CleanStartOnInitialConnection: true,
		ConnectUsername:               config.Get().MqttClient.Username,
		ConnectPassword:               []byte(config.Get().MqttClient.Password),
		OnConnectionUp: func(*autopaho.ConnectionManager, *paho.Connack) {
			log.Infof("mqtt5: connected to broker=%s client_id=%s", brokerURL.Redacted(), clientID)
		},
		OnConnectError: func(err error) {
			log.Errorf("mqtt5: connect failed broker=%s client_id=%s: %v", brokerURL.Redacted(), clientID, err)
		},
		ClientConfig: paho.ClientConfig{ClientID: clientID},
	}
	if config.Get().MqttClient.UseSSL {
		tlsConfig, err := buildMQTTTLSConfig()
		if err != nil {
			return autopaho.ClientConfig{}, fmt.Errorf("mqtt5 tls config: %w", err)
		}
		cfg.TlsCfg = tlsConfig
	}
	return cfg, nil
}
