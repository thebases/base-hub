package mqtt

// File overview:
// - Responsibility: encapsulates MQTT transport concerns for mqtt client, including client lifecycle, subscriptions, and broker-facing delivery behavior.
// - Key symbols: `InitMQTT`, `MqttPublish`, `PublishRequest`.
// - Pattern: Reads operational behavior from shared config so environments can tune the same code paths without forking implementation logic.

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"git.thebase.vn/base_hub/internal/config"
	"git.thebase.vn/base_hub/internal/services/log"
	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/google/uuid"
)

var (
	MqttClient   mqtt.Client
	mqttClientMu sync.RWMutex
)

// activeSubscription is one Subscribe call that is still live, kept so OnConnect can restore it.
type activeSubscription struct {
	qos     byte
	handler mqtt.MessageHandler
}

// activeSubscriptions tracks every live Subscribe by topic. The client connects with a clean
// session, so the broker forgets subscriptions on every reconnect; OnConnect replays this set
// so auto-reconnect does not silently stop inbound delivery for every subscriber (base-workflow
// mqtt triggers, edc device events, ...).
var activeSubscriptions = struct {
	mu     sync.Mutex
	topics map[string]activeSubscription
}{topics: map[string]activeSubscription{}}

type PublishRequest struct {
	Topic   string      `json:"topic"`
	Message interface{} `json:"message"`
}

// InitMQTT initializes the mqtt handled by this package.
// It returns an error (instead of exiting the process) only when the TLS config is invalid; the
// client is left unset in that case so a later call can retry. A broker or login failure is not an
// error here: paho retries in the background and the supervisor keeps the subscriptions healthy.
func InitMQTT() error {
	client, token, err := buildAndConnect()
	if err != nil || client == nil {
		return err
	}
	// Wait outside mqttClientMu so IsConnected/Snapshot/publish never queue behind a slow broker.
	if !token.WaitTimeout(initConnectWait) {
		log.Warnf("mqtt: client_id=%s not connected after %s (login/network failure?); retrying in background", metricsClientID(), initConnectWait)
	} else if err := token.Error(); err != nil {
		log.Errorf("mqtt: connect client_id=%s: %v", metricsClientID(), err)
	}
	startSupervisor()
	return nil
}

func metricsClientID() string {
	metrics.mu.Lock()
	defer metrics.mu.Unlock()
	return metrics.clientID
}

// buildAndConnect creates the client once under mqttClientMu and starts its Connect; it returns a
// nil client when one already exists.
func buildAndConnect() (mqtt.Client, mqtt.Token, error) {
	mqttClientMu.Lock()
	defer mqttClientMu.Unlock()
	// An existing client is kept even while reconnecting: paho's AutoReconnect restores it, and
	// building another one per caller would leave several live clients (duplicate deliveries).
	if MqttClient != nil {
		return nil, nil, nil
	}
	brokerURL := mqttBrokerURL()
	opts := mqtt.NewClientOptions()
	opts.AddBroker(brokerURL)

	clientID := config.Get().MqttClient.ClientID + "_" + uuid.NewString()[:8]
	log.Infof("mqtt: connecting to broker=%s client_id=%s", brokerURL, clientID)
	opts.SetClientID(clientID)

	// Enable automatic reconnect. Explicit liveness timers so a half-open TCP session (NAT/LB idle
	// drop, broker-side kick) is detected in ~keepalive+ping timeout instead of the OS TCP timeout.
	opts.AutoReconnect = true
	opts.ConnectRetry = true
	opts.ConnectRetryInterval = 3 * time.Second
	opts.MaxReconnectInterval = 30 * time.Second
	opts.KeepAlive = 30
	opts.PingTimeout = 10 * time.Second
	opts.ConnectTimeout = 10 * time.Second
	opts.WriteTimeout = 10 * time.Second

	// If your EMQX broker requires authentication
	opts.Username = config.Get().MqttClient.Username
	opts.Password = config.Get().MqttClient.Password
	if config.Get().MqttClient.UseSSL {
		tlsConfig, err := buildMQTTTLSConfig()
		// A broken TLS config guarantees every connect attempt fails, so report it instead of retrying.
		if err != nil {
			return nil, nil, fmt.Errorf("mqtt: invalid tls config broker=%s client_id=%s: %w", brokerURL, clientID, err)
		}
		opts.SetTLSConfig(tlsConfig)
		log.Infof(
			"mqtt: tls enabled broker=%s client_id=%s server_name=%s ca_file=%t client_cert=%t insecure_skip_verify=%t",
			brokerURL,
			clientID,
			tlsConfig.ServerName,
			strings.TrimSpace(config.Get().MqttClient.TLSCAFile) != "",
			strings.TrimSpace(config.Get().MqttClient.TLSCertFile) != "",
			tlsConfig.InsecureSkipVerify,
		)
	}

	// Logging callbacks
	opts.OnConnect = func(c mqtt.Client) {
		metrics.connects.Add(1)
		metrics.lastConnectedAt.Store(time.Now().Unix())
		log.Infof("mqtt: connected to broker=%s client_id=%s", brokerURL, clientID)
		// Off paho's callback goroutine: syncSubscriptions waits on SUBACKs.
		go syncSubscriptions(c)
	}
	opts.OnReconnecting = func(c mqtt.Client, opts *mqtt.ClientOptions) {
		metrics.reconnecting.Add(1)
		log.Infof("mqtt: reconnecting broker=%s client_id=%s", brokerURL, clientID)
	}
	opts.OnConnectionLost = func(c mqtt.Client, err error) {
		noteConnectionLost(err)
		log.Errorf("mqtt: connection lost broker=%s client_id=%s: %v", brokerURL, clientID, err)
	}

	client := mqtt.NewClient(opts)
	// ConnectRetry makes Connect() loop forever on ANY failure (broker down, bad login, ACL), so the
	// client is published before the first CONNACK and InitMQTT waits a bounded time outside the
	// lock; paho keeps retrying in the background and OnConnect restores the subscriptions.
	MqttClient = client
	metrics.mu.Lock()
	metrics.brokerURL, metrics.clientID = brokerURL, clientID
	metrics.mu.Unlock()
	return client, client.Connect(), nil
}

const (
	// initConnectWait bounds how long InitMQTT waits for the first CONNACK.
	initConnectWait = 10 * time.Second
	// supervisorInterval is how often the supervisor re-asserts subscriptions on the broker.
	supervisorInterval = 60 * time.Second
	// subscribeWait bounds one SUBACK wait; paho's token.Wait() has no timeout.
	subscribeWait = 10 * time.Second
	// resyncFailuresBeforeReconnect consecutive failed re-assertions force a fresh login.
	resyncFailuresBeforeReconnect = 2
)

var (
	supervisorOnce sync.Once
	syncMu         sync.Mutex // serializes syncSubscriptions (OnConnect vs supervisor tick)
)

// startSupervisor runs the self-healing loop once per process. Why it exists: the broker can drop
// or refuse this client's subscriptions while the TCP session and keepalive stay healthy (ACL or
// auth-cache refresh, broker-side session cleanup, a SUBACK that never arrived). paho then sees
// nothing wrong, OnConnect never fires, and base/evt + base/txn go silent until the client is kicked
// from the EMQX portal. Re-issuing SUBSCRIBE is idempotent and restores the subscription; repeated
// failure (refused/timeout) means the session itself is bad, so the supervisor re-logs in.
func startSupervisor() {
	supervisorOnce.Do(func() {
		go func() {
			failures := 0
			for range time.Tick(supervisorInterval) {
				failures = superviseOnce(failures)
			}
		}()
	})
}

func superviseOnce(failures int) int {
	metrics.supervisorTicks.Add(1)
	mqttClientMu.RLock()
	client := MqttClient
	mqttClientMu.RUnlock()
	if client == nil {
		return 0
	}
	// paho's IsConnected is true while connecting/reconnecting, so false means a prior forced
	// Disconnect left the client down for good (AutoReconnect does not fire after Disconnect).
	if !client.IsConnected() {
		log.Warnf("mqtt: supervisor: client is down, reconnecting")
		client.Connect()
		return 0
	}
	if !client.IsConnectionOpen() {
		return failures // paho is mid-reconnect; OnConnect will restore subscriptions
	}
	if !syncSubscriptions(client).needsRelogin() {
		return 0
	}
	failures++
	if failures < resyncFailuresBeforeReconnect {
		return failures
	}
	log.Errorf("mqtt: supervisor: subscriptions failed to restore %d times in a row, forcing a fresh login", failures)
	metrics.forcedReconnects.Add(1)
	// Disconnect then Connect: a new CONNECT re-authenticates and re-checks ACL, which is what the
	// manual EMQX kick achieved. OnConnect then replays the subscriptions.
	client.Disconnect(250)
	client.Connect()
	return 0
}

func MqttPublish(topic string, message string) (bool, error) {
	client, err := mqttClient()
	if err != nil {
		return false, err
	}
	log.Infof("Subscribing to topic: %s", topic)

	// Now publish
	pubToken := client.Publish(topic, 1, false, message)
	pubToken.Wait()
	metrics.published.Add(1)
	if pubToken.Error() != nil {
		metrics.publishErrors.Add(1)
		log.Errorf("publish error: %v", pubToken.Error())
		return false, fmt.Errorf("publish error: %v", pubToken.Error())
	}
	return true, nil
}

// subscribeQoS is the only QoS this client subscribes at (at-least-once, same as every publish).
const subscribeQoS byte = 1

// Subscribe handles the subscribe handled by this package. The qos argument is ignored: every
// subscription is pinned to QoS 1, so a caller cannot reintroduce QoS 0/2.
func Subscribe(topic string, qos byte, handler func([]byte)) (func() error, error) {
	qos = subscribeQoS
	client, err := mqttClient()
	if err != nil {
		return nil, err
	}
	if topic == "" {
		return nil, fmt.Errorf("mqtt subscribe topic is required")
	}
	if handler == nil {
		return nil, fmt.Errorf("mqtt subscribe handler is required")
	}
	messageHandler := func(_ mqtt.Client, msg mqtt.Message) {
		noteInbound(topic, len(msg.Payload()))
		handler(append([]byte(nil), msg.Payload()...))
	}
	token := client.Subscribe(topic, qos, messageHandler)
	if !token.WaitTimeout(subscribeWait) {
		return nil, fmt.Errorf("subscribe %q: no SUBACK within %s", topic, subscribeWait)
	}
	if err := subscribeTokenError(token, topic); err != nil {
		noteTopicState(topic, err)
		return nil, err
	}
	noteTopicState(topic, nil)
	activeSubscriptions.mu.Lock()
	activeSubscriptions.topics[topic] = activeSubscription{qos: qos, handler: messageHandler}
	activeSubscriptions.mu.Unlock()
	return func() error {
		// Forget the topic first so a reconnect racing this unsubscribe does not resurrect it.
		activeSubscriptions.mu.Lock()
		delete(activeSubscriptions.topics, topic)
		activeSubscriptions.mu.Unlock()
		dropTopicMetric(topic)
		client, err := mqttClient()
		if err != nil {
			return err
		}
		unsubscribeToken := client.Unsubscribe(topic)
		unsubscribeToken.Wait()
		if unsubscribeToken.Error() != nil {
			return fmt.Errorf("unsubscribe error: %v", unsubscribeToken.Error())
		}
		return nil
	}, nil
}

// syncResult classifies one resync round. Refusals (SUBACK 0x80 / QoS downgrade) are broker-side
// policy for that topic and say nothing about the session; timeouts and not-connected errors mean
// the session itself is bad.
type syncResult struct {
	total         int
	ok            int
	refused       int
	sessionFailed int
}

// needsRelogin reports whether a fresh login is warranted: any session-level failure, or most
// topics refused (e.g. the user's ACL was wiped). One refused topic among many (an ACL gap for that
// topic alone) is only logged, so it does not bounce the healthy topics every two minutes.
func (r syncResult) needsRelogin() bool {
	return r.sessionFailed > 0 || (r.total > 0 && r.refused*2 > r.total)
}

// syncSubscriptions (re)issues SUBSCRIBE for every live subscription and classifies the outcome.
// Called from OnConnect and from the supervisor tick. All SUBSCRIBEs are sent first and the SUBACKs
// are awaited against one shared deadline, so N topics cost one round trip, not N, and a dead
// broker cannot stretch a round to N * subscribeWait.
func syncSubscriptions(client mqtt.Client) syncResult {
	syncMu.Lock()
	defer syncMu.Unlock()
	started := time.Now()
	activeSubscriptions.mu.Lock()
	snapshot := make(map[string]activeSubscription, len(activeSubscriptions.topics))
	for topic, sub := range activeSubscriptions.topics {
		snapshot[topic] = sub
	}
	activeSubscriptions.mu.Unlock()

	tokens := make(map[string]mqtt.Token, len(snapshot))
	for topic, sub := range snapshot {
		tokens[topic] = client.Subscribe(topic, sub.qos, sub.handler)
	}
	res := syncResult{total: len(snapshot)}
	deadline := started.Add(subscribeWait)
	for topic, token := range tokens {
		wait := time.Until(deadline)
		if wait < time.Millisecond {
			wait = time.Millisecond // still collect tokens that already completed
		}
		if !token.WaitTimeout(wait) {
			err := fmt.Errorf("no SUBACK within %s", subscribeWait)
			noteTopicState(topic, err)
			log.Errorf("mqtt: resubscribe topic=%s timed out: %v", topic, err)
			res.sessionFailed++
			continue
		}
		if err := subscribeTokenError(token, topic); err != nil {
			noteTopicState(topic, err)
			log.Errorf("mqtt: resubscribe topic=%s failed: %v", topic, err)
			if errors.Is(err, errSubscribeRefused) {
				res.refused++
			} else {
				res.sessionFailed++
			}
			continue
		}
		noteTopicState(topic, nil)
		res.ok++
	}
	noteResync(res, time.Since(started))
	return res
}

// errSubscribeRefused marks a broker-side refusal (ACL deny, QoS cap), as opposed to a dead session.
var errSubscribeRefused = errors.New("subscribe refused by broker")

// subscribeSubackFailure is the SUBACK return code a broker sends when it refuses a subscription
// (ACL deny, invalid topic filter).
const subscribeSubackFailure byte = 0x80

// subscribeTokenError turns a finished subscribe token into an error. paho reports a broker-side
// refusal only as a 0x80 code in SubscribeToken.Result(), with token.Error() left nil, so checking
// Error() alone logs "subscribed" for a topic that will never deliver a message.
func subscribeTokenError(token mqtt.Token, topic string) error {
	if err := token.Error(); err != nil {
		return fmt.Errorf("subscribe error: %w", err)
	}
	subToken, ok := token.(*mqtt.SubscribeToken)
	if !ok {
		return nil
	}
	return subackRefusal(subToken.Result())
}

// subackRefusal reports the first topic in a SUBACK result map that the broker refused or granted
// below QoS 1 (a downgrade to QoS 0 would silently lose at-least-once delivery).
func subackRefusal(result map[string]byte) error {
	for granted, code := range result {
		if code == subscribeSubackFailure {
			return fmt.Errorf("%w for %q (SUBACK 0x80, check the broker ACL for this user)", errSubscribeRefused, granted)
		}
		if code < subscribeQoS {
			return fmt.Errorf("%w: %q granted QoS %d by broker, QoS 1 required (check the broker's max QoS limit)", errSubscribeRefused, granted, code)
		}
	}
	return nil
}

func mqttBrokerURL() string {
	protocol := "tcp://"
	if config.Get().MqttClient.UseSSL {
		protocol = "ssl://"
	}
	return protocol + config.Get().MqttClient.Domain + ":" + config.Get().MqttClient.Port
}

func buildMQTTTLSConfig() (*tls.Config, error) {
	serverName := strings.TrimSpace(config.Get().MqttClient.Domain)
	// Pin the handshake host explicitly so TLS validation keeps working even if the caller only configured host:port pieces separately.
	tlsConfig := &tls.Config{
		MinVersion:         tls.VersionTLS12,
		ServerName:         serverName,
		InsecureSkipVerify: config.Get().MqttClient.TLSInsecureSkipVerify,
	}
	if err := applyMQTTTLSRootCA(tlsConfig); err != nil {
		return nil, err
	}
	if err := applyMQTTTLSClientCertificate(tlsConfig); err != nil {
		return nil, err
	}
	return tlsConfig, nil
}

func applyMQTTTLSRootCA(tlsConfig *tls.Config) error {
	caFile := strings.TrimSpace(config.Get().MqttClient.TLSCAFile)
	if caFile == "" {
		return nil
	}
	caBytes, err := os.ReadFile(caFile)
	if err != nil {
		return fmt.Errorf("read mqtt tls ca file %q: %w", caFile, err)
	}
	rootCAs, err := x509.SystemCertPool()
	if err != nil {
		return fmt.Errorf("load system cert pool: %w", err)
	}
	if rootCAs == nil {
		rootCAs = x509.NewCertPool()
	}
	// Append the broker CA to the system roots so private PKI deployments validate without disabling verification globally.
	if !rootCAs.AppendCertsFromPEM(caBytes) {
		return fmt.Errorf("parse mqtt tls ca file %q: no certificates found", caFile)
	}
	tlsConfig.RootCAs = rootCAs
	return nil
}

func applyMQTTTLSClientCertificate(tlsConfig *tls.Config) error {
	certFile := strings.TrimSpace(config.Get().MqttClient.TLSCertFile)
	keyFile := strings.TrimSpace(config.Get().MqttClient.TLSKeyFile)
	if certFile == "" && keyFile == "" {
		return nil
	}
	// Require the cert/key pair together so mutual-TLS failures surface as a config problem before we start reconnecting forever.
	if certFile == "" || keyFile == "" {
		return fmt.Errorf("mqtt tls client certificate requires both tls_cert_file and tls_key_file")
	}
	certificate, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return fmt.Errorf("load mqtt tls client certificate: %w", err)
	}
	tlsConfig.Certificates = []tls.Certificate{certificate}
	return nil
}

// IsConnected reports whether the MQTT client is currently connected, safe
// for concurrent callers such as health checks.
func IsConnected() bool {
	mqttClientMu.RLock()
	defer mqttClientMu.RUnlock()
	return MqttClient != nil && MqttClient.IsConnected()
}

func mqttClient() (mqtt.Client, error) {
	if config.Get().MqttClient.Enable {
		if err := InitMQTT(); err != nil {
			return nil, err
		}
	}
	mqttClientMu.RLock()
	defer mqttClientMu.RUnlock()
	if MqttClient == nil || !MqttClient.IsConnected() {
		return nil, fmt.Errorf("mqtt client is not connected")
	}
	return MqttClient, nil
}
