// Page /web/workflow/mqtt-monitor — live health and performance of the server's MQTT client:
// connection churn, subscription resync (self-healing) counters, and per-topic inbound rate,
// worker queue depth, drops and workflow run times. It polls base.workflow.trigger.mqtt_metrics;
// the backend only exposes lifetime counters, so message rates come from deltas between polls.
import { Component, loadTemplate, onMounted, onWillUnmount, useState, xml } from "nodexui/owl";
import { Alert, Badge, Button } from "nodexui/component";

import { getMqttMetrics } from "@/modules/workflow/api.js";

const template = await loadTemplate(new URL("./MqttMonitorPage.xml", import.meta.url));

const POLL_MS = 5000;
const HISTORY_POINTS = 60;
// A topic with no inbound frame for this long is flagged idle (informational: quiet fleets are normal).
const IDLE_WARN_SECONDS = 300;

function ago(now, at) {
  if (!at) return t("never");
  const s = Math.max(0, now - at);
  if (s < 60) return t("{s}s ago", { s });
  if (s < 3600) return t("{m}m {s}s ago", { m: Math.floor(s / 60), s: s % 60 });
  if (s < 86400) return t("{h}h {m}m ago", { h: Math.floor(s / 3600), m: Math.floor((s % 3600) / 60) });
  return t("{d}d ago", { d: Math.floor(s / 86400) });
}

function fmtNum(n) {
  return Number(n ?? 0).toLocaleString();
}

function fmtMs(n) {
  const v = Number(n ?? 0);
  return v >= 100 ? v.toFixed(0) : v.toFixed(1);
}

function fmtRate(n) {
  return n >= 10 ? n.toFixed(0) : n.toFixed(1);
}

function fmtBytes(n) {
  const v = Number(n ?? 0);
  if (v < 1024) return `${v} B`;
  if (v < 1048576) return `${(v / 1024).toFixed(1)} KB`;
  return `${(v / 1048576).toFixed(1)} MB`;
}

export class MqttMonitorPage extends Component {
  static template = xml({ raw: [template] });
  static components = { Alert, Badge, Button };
  static props = { params: { type: Object, optional: true } };

  setup() {
    this.state = useState({ data: null, error: "", loading: true, paused: false, rate: 0, topicRates: {}, history: [] });
    this.prev = null;
    this.timer = null;
    onMounted(() => {
      this.refresh();
      this.timer = setInterval(() => {
        if (!this.state.paused) this.refresh();
      }, POLL_MS);
    });
    onWillUnmount(() => clearInterval(this.timer));
  }

  async refresh() {
    try {
      const data = await getMqttMetrics();
      this.applyRates(data);
      this.state.data = data;
      this.state.error = "";
    } catch (error) {
      this.state.error = error instanceof Error ? error.message : t("Failed to load MQTT metrics");
    } finally {
      this.state.loading = false;
    }
  }

  // applyRates turns lifetime counters into msg/s using the client clock; a counter that went
  // backwards means the server restarted, so that sample is 0 instead of negative.
  applyRates(data) {
    const nowMs = Date.now();
    if (this.prev) {
      const dt = (nowMs - this.prev.at) / 1000;
      if (dt > 0) {
        const rate = Math.max(0, (data.received_total - this.prev.total) / dt);
        const topicRates = {};
        for (const topic of data.topics ?? []) {
          const before = this.prev.topics[topic.topic];
          topicRates[topic.topic] = before === undefined ? 0 : Math.max(0, (topic.received - before) / dt);
        }
        this.state.rate = rate;
        this.state.topicRates = topicRates;
        this.state.history = [...this.state.history, rate].slice(-HISTORY_POINTS);
      }
    }
    const topics = {};
    for (const topic of data.topics ?? []) topics[topic.topic] = topic.received;
    this.prev = { at: nowMs, total: data.received_total, topics };
  }

  togglePaused() {
    this.state.paused = !this.state.paused;
  }

  get d() {
    return this.state.data;
  }

  get statusKey() {
    const d = this.d;
    if (!d) return "loading";
    if (!d.enabled) return "disabled";
    if (d.connection_open) return "connected";
    return d.connected ? "reconnecting" : "disconnected";
  }

  get statusLabel() {
    switch (this.statusKey) {
      case "loading":
        return t("Loading");
      case "disabled":
        return t("Disabled");
      case "connected":
        return t("Connected");
      case "reconnecting":
        return t("Reconnecting");
      default:
        return t("Disconnected");
    }
  }

  get statusVariant() {
    const key = this.statusKey;
    if (key === "connected") return "success";
    if (key === "reconnecting" || key === "loading") return "warning";
    return "danger";
  }

  get cards() {
    const d = this.d;
    if (!d) return [];
    const now = d.now;
    return [
      { title: t("Inbound"), value: t("{rate} msg/s", { rate: fmtRate(this.state.rate) }), sub: t("{frames} frames · {bytes}", { frames: fmtNum(d.received_total), bytes: fmtBytes(d.bytes_total) }) },
      { title: t("Connection"), value: t("{count} connects", { count: fmtNum(d.connects) }), sub: t("{lost} lost · last up {ago}", { lost: fmtNum(d.connection_lost), ago: ago(now, d.last_connected_at) }) },
      { title: t("Resync (self-heal)"), value: t("{count} rounds", { count: fmtNum(d.resyncs) }), sub: t("{forced} forced re-logins · {refused} refused · {timeouts} timeouts", { forced: fmtNum(d.forced_reconnects), refused: fmtNum(d.resync_refused), timeouts: fmtNum(d.resync_timeouts) }) },
      { title: t("Resync latency"), value: t("{ms} ms", { ms: fmtMs(d.last_resync_ms) }), sub: t("max {max} ms · last {ago}", { max: fmtMs(d.max_resync_ms), ago: ago(now, d.last_resync_at) }) },
      { title: t("Publish"), value: fmtNum(d.published_total), sub: t("{count} errors", { count: fmtNum(d.publish_errors_total) }) },
    ];
  }

  get alerts() {
    const d = this.d;
    if (!d) return [];
    const out = [];
    if (d.enabled && !d.connection_open) {
      out.push({
        variant: "danger",
        text: d.last_lost_error
          ? t("MQTT client is not connected to the broker. Last error: {error}", { error: d.last_lost_error })
          : t("MQTT client is not connected to the broker."),
      });
    }
    const dropped = (d.topics ?? []).reduce((sum, topic) => sum + (topic.runtime?.dropped ?? 0), 0);
    if (dropped > 0) out.push({ variant: "danger", text: t("{count} inbound frame(s) were dropped because a topic queue stayed full. Workflow runs on that topic are too slow for its message rate.", { count: fmtNum(dropped) }) });
    for (const topic of d.topics ?? []) {
      if (topic.last_error) out.push({ variant: "warning", text: t("Topic {topic}: {error}", { topic: topic.topic, error: topic.last_error }) });
    }
    return out;
  }

  // sparkline bars scaled to the busiest sample in the window.
  get bars() {
    const history = this.state.history;
    const max = Math.max(...history, 0.1);
    return history.map((value) => ({ height: Math.max(2, Math.round((value / max) * 36)), title: t("{rate} msg/s", { rate: fmtRate(value) }) }));
  }

  get rows() {
    const d = this.d;
    if (!d) return [];
    const now = d.now;
    return (d.topics ?? []).map((topic) => {
      const rt = topic.runtime ?? {};
      const idleSeconds = topic.last_received_at ? now - topic.last_received_at : null;
      const fill = rt.queue_cap ? Math.min(100, Math.round((rt.queue_depth / rt.queue_cap) * 100)) : 0;
      return {
        topic: topic.topic,
        subscribed: topic.subscribed,
        stateLabel: topic.subscribed ? t("Subscribed") : t("Not subscribed"),
        stateVariant: topic.subscribed ? "success" : "danger",
        received: fmtNum(topic.received),
        rate: fmtRate(this.state.topicRates[topic.topic] ?? 0),
        lastReceived: ago(now, topic.last_received_at),
        idle: idleSeconds !== null && idleSeconds > IDLE_WARN_SECONDS,
        asserted: ago(now, topic.last_asserted_at),
        hasRuntime: Boolean(topic.runtime),
        queue: `${fmtNum(rt.queue_depth)} / ${fmtNum(rt.queue_cap)}`,
        queueStyle: `width: ${fill}%`,
        queueBar: fill >= 80 ? "bg-danger" : fill >= 40 ? "bg-warning" : "bg-success",
        processed: fmtNum(rt.processed),
        dropped: fmtNum(rt.dropped),
        hasDropped: (rt.dropped ?? 0) > 0,
        failed: fmtNum(rt.failed),
        hasFailed: (rt.failed ?? 0) > 0,
        avgMs: fmtMs(rt.avg_run_ms),
        maxMs: fmtMs(rt.max_run_ms),
        triggers: fmtNum(rt.triggers),
        error: topic.last_error,
      };
    });
  }
}
