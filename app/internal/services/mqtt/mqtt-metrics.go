package mqtt

// File overview:
// - Responsibility: in-process health and performance counters for the MQTT v3 client (connection
//   churn, subscription resync, per-topic inbound traffic) plus a hook for the workflow trigger
//   registry to add its per-topic queue and run-time stats.
// - Key symbols: `Snapshot`, `RegisterTopicStatsProvider`, `TopicRuntimeStats`.
// - Pattern: lock-free atomics on the hot path (one add per inbound frame); a mutex only guards
//   the topic map and the last-error strings. Counters live for the process lifetime; the UI derives
//   rates from deltas between polls.

import (
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"git.thebase.vn/base_hub/internal/config"
)

// TopicRuntimeStats is what the workflow trigger registry reports for one subscribed topic: the
// hand-off queue between paho's handler and the workflow worker, and how long runs take.
type TopicRuntimeStats struct {
	QueueDepth      int     `json:"queue_depth"`
	QueueCap        int     `json:"queue_cap"`
	Enqueued        int64   `json:"enqueued"`
	Processed       int64   `json:"processed"`
	Dropped         int64   `json:"dropped"`
	Failed          int64   `json:"failed"`
	LastRunMs       float64 `json:"last_run_ms"`
	AvgRunMs        float64 `json:"avg_run_ms"`
	MaxRunMs        float64 `json:"max_run_ms"`
	LastProcessedAt int64   `json:"last_processed_at"`
	Triggers        int     `json:"triggers"`
}

type topicMetric struct {
	received       atomic.Int64
	bytes          atomic.Int64
	lastReceivedAt atomic.Int64 // unix seconds
	lastAssertedAt atomic.Int64 // unix seconds of the last SUBACK-confirmed SUBSCRIBE
	subscribed     atomic.Bool
	lastError      atomic.Value // string; "" once the topic is healthy again
}

var metrics = struct {
	startedAt        atomic.Int64
	connects         atomic.Int64
	connectionLost   atomic.Int64
	reconnecting     atomic.Int64
	lastConnectedAt  atomic.Int64
	lastLostAt       atomic.Int64
	supervisorTicks  atomic.Int64
	resyncs          atomic.Int64
	resyncFailures   atomic.Int64 // resync rounds that needed a re-login
	resyncRefused    atomic.Int64 // topic subscribes the broker refused (ACL), across rounds
	resyncTimeouts   atomic.Int64 // topic subscribes that got no SUBACK / hit a dead session
	lastResyncAt     atomic.Int64
	lastResyncMicros atomic.Int64
	maxResyncMicros  atomic.Int64
	forcedReconnects atomic.Int64
	receivedTotal    atomic.Int64
	bytesTotal       atomic.Int64
	published        atomic.Int64
	publishErrors    atomic.Int64

	mu            sync.Mutex
	lastLostError string
	brokerURL     string
	clientID      string
	topics        map[string]*topicMetric
	provider      func() map[string]TopicRuntimeStats
}{topics: map[string]*topicMetric{}}

func init() { metrics.startedAt.Store(time.Now().Unix()) }

// RegisterTopicStatsProvider lets the workflow trigger registry contribute per-topic queue stats
// (keyed by the subscribed topic filter). Last registration wins.
func RegisterTopicStatsProvider(provider func() map[string]TopicRuntimeStats) {
	metrics.mu.Lock()
	metrics.provider = provider
	metrics.mu.Unlock()
}

func topicMetricFor(topic string) *topicMetric {
	metrics.mu.Lock()
	defer metrics.mu.Unlock()
	m := metrics.topics[topic]
	if m == nil {
		m = &topicMetric{}
		metrics.topics[topic] = m
	}
	return m
}

func dropTopicMetric(topic string) {
	metrics.mu.Lock()
	delete(metrics.topics, topic)
	metrics.mu.Unlock()
}

func noteInbound(topic string, size int) {
	m := topicMetricFor(topic)
	m.received.Add(1)
	m.bytes.Add(int64(size))
	m.lastReceivedAt.Store(time.Now().Unix())
	metrics.receivedTotal.Add(1)
	metrics.bytesTotal.Add(int64(size))
}

// noteTopicState records the outcome of one SUBSCRIBE for a topic; err == nil clears the last error.
func noteTopicState(topic string, err error) {
	m := topicMetricFor(topic)
	if err != nil {
		m.subscribed.Store(false)
		m.lastError.Store(err.Error())
		return
	}
	m.subscribed.Store(true)
	m.lastAssertedAt.Store(time.Now().Unix())
	m.lastError.Store("")
}

func noteConnectionLost(err error) {
	metrics.connectionLost.Add(1)
	metrics.lastLostAt.Store(time.Now().Unix())
	metrics.mu.Lock()
	if err != nil {
		metrics.lastLostError = err.Error()
	}
	metrics.mu.Unlock()
}

func noteResync(res syncResult, took time.Duration) {
	metrics.resyncs.Add(1)
	metrics.lastResyncAt.Store(time.Now().Unix())
	micros := took.Microseconds()
	metrics.lastResyncMicros.Store(micros)
	for {
		cur := metrics.maxResyncMicros.Load()
		if micros <= cur || metrics.maxResyncMicros.CompareAndSwap(cur, micros) {
			break
		}
	}
	metrics.resyncRefused.Add(int64(res.refused))
	metrics.resyncTimeouts.Add(int64(res.sessionFailed))
	if res.needsRelogin() {
		metrics.resyncFailures.Add(1)
	}
}

// Snapshot returns a JSON-ready view of the client and every subscribed topic. Safe to call from
// request handlers; it only reads atomics and takes the map lock briefly.
func Snapshot() map[string]any {
	metrics.mu.Lock()
	lostErr := metrics.lastLostError
	broker, clientID := metrics.brokerURL, metrics.clientID
	provider := metrics.provider
	names := make([]string, 0, len(metrics.topics))
	byName := make(map[string]*topicMetric, len(metrics.topics))
	for name, m := range metrics.topics {
		names = append(names, name)
		byName[name] = m
	}
	metrics.mu.Unlock()
	sort.Strings(names)

	var runtime map[string]TopicRuntimeStats
	if provider != nil {
		runtime = provider()
	}
	// Topics the registry holds but the client never subscribed (broker down at registration) still
	// belong on the dashboard so a never-subscribed topic is visible, not absent.
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		seen[name] = struct{}{}
	}
	for name := range runtime {
		if _, ok := seen[name]; !ok {
			names = append(names, name)
			byName[name] = &topicMetric{}
		}
	}
	sort.Strings(names)

	topics := make([]map[string]any, 0, len(names))
	for _, name := range names {
		m := byName[name]
		lastErr, _ := m.lastError.Load().(string)
		row := map[string]any{
			"topic":            name,
			"subscribed":       m.subscribed.Load(),
			"received":         m.received.Load(),
			"bytes":            m.bytes.Load(),
			"last_received_at": m.lastReceivedAt.Load(),
			"last_asserted_at": m.lastAssertedAt.Load(),
			"last_error":       lastErr,
		}
		if rt, ok := runtime[name]; ok {
			row["runtime"] = rt
		}
		topics = append(topics, row)
	}

	mqttCfg := config.Get().MqttClient
	return map[string]any{
		"now":                  time.Now().Unix(),
		"enabled":              mqttCfg.Enable,
		"connected":            IsConnected(),
		"connection_open":      connectionOpen(),
		"broker":               broker,
		"client_id":            clientID,
		"started_at":           metrics.startedAt.Load(),
		"connects":             metrics.connects.Load(),
		"connection_lost":      metrics.connectionLost.Load(),
		"reconnect_attempts":   metrics.reconnecting.Load(),
		"last_connected_at":    metrics.lastConnectedAt.Load(),
		"last_lost_at":         metrics.lastLostAt.Load(),
		"last_lost_error":      lostErr,
		"supervisor_ticks":     metrics.supervisorTicks.Load(),
		"resyncs":              metrics.resyncs.Load(),
		"resync_relogins":      metrics.resyncFailures.Load(),
		"resync_refused":       metrics.resyncRefused.Load(),
		"resync_timeouts":      metrics.resyncTimeouts.Load(),
		"last_resync_at":       metrics.lastResyncAt.Load(),
		"last_resync_ms":       float64(metrics.lastResyncMicros.Load()) / 1000,
		"max_resync_ms":        float64(metrics.maxResyncMicros.Load()) / 1000,
		"forced_reconnects":    metrics.forcedReconnects.Load(),
		"received_total":       metrics.receivedTotal.Load(),
		"bytes_total":          metrics.bytesTotal.Load(),
		"published_total":      metrics.published.Load(),
		"publish_errors_total": metrics.publishErrors.Load(),
		"topics":               topics,
	}
}

func connectionOpen() bool {
	mqttClientMu.RLock()
	defer mqttClientMu.RUnlock()
	return MqttClient != nil && MqttClient.IsConnectionOpen()
}
