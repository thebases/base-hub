// Port of frontend-admin's modules/base/pages/health-check-page.tsx. No
// Shell/sidebar wiring here — this file is only the page body component
// (same convention as ApiDocPage/NotificationsPage); routing to
// /web/system-health is handled centrally by main.js. Auto-refreshes every
// 30s via setInterval, same as the React source's REFRESH_INTERVAL_MS.

import { Component, loadTemplate, onMounted, onWillUnmount, useState, xml } from "nodexui/owl";

import { getHealthDetail } from "@/modules/base/api/health.js";
import { Badge, Button, Card, CardBody, CardHeader, CardTitle, CardDescription, Icon } from "nodexui/component";

const template = await loadTemplate(new URL("./HealthCheckPage.xml", import.meta.url));

const REFRESH_INTERVAL_MS = 30000;

// formatUptime mirrors the React source's day/hour/minute rollup: days>0 ->
// "Xd Yh", hours>0 -> "Xh Ym", else "Xm".
function formatUptime(totalSeconds) {
  const seconds = Number(totalSeconds) || 0;
  const days = Math.floor(seconds / 86400);
  const hours = Math.floor((seconds % 86400) / 3600);
  const minutes = Math.floor((seconds % 3600) / 60);
  if (days > 0) return `${days}d ${hours}h`;
  if (hours > 0) return `${hours}h ${minutes}m`;
  return `${minutes}m`;
}

export class HealthCheckPage extends Component {
  static template = xml({ raw: [template] });
  static components = { Badge, Button, Card, CardBody, CardHeader, CardTitle, CardDescription, Icon };

  setup() {
    this.state = useState({ status: "loading", message: "", detail: null });
    let timer = null;

    onMounted(() => {
      this.load();
      timer = window.setInterval(() => this.load(), REFRESH_INTERVAL_MS);
    });
    onWillUnmount(() => {
      if (timer) window.clearInterval(timer);
    });
  }

  // dependencies exposes the three fixed backend checks as a foreach-able
  // array so the template renders one DependencyCard-equivalent block per
  // entry instead of three hand-duplicated blocks.
  get dependencies() {
    const checks = this.state.detail ? this.state.detail.checks : {};
    return [
      { key: "database", label: t("Database"), dep: checks.database || { status: "disabled", enabled: false } },
      { key: "kafka", label: "Kafka", dep: checks.kafka || { status: "disabled", enabled: false } },
      { key: "mqtt", label: "MQTT", dep: checks.mqtt || { status: "disabled", enabled: false } },
    ];
  }

  get statusVariant() {
    return this.state.detail && this.state.detail.status === "healthy" ? "secondary" : "danger";
  }

  get uptimeLabel() {
    return this.state.detail ? formatUptime(this.state.detail.uptime_seconds) : "";
  }

  get requestsLabel() {
    if (!this.state.detail) return "";
    const stats = this.state.detail.stats || {};
    return `${stats.requests_total ?? 0} / ${stats.errors_total ?? 0}`;
  }

  // dependencyBodyText mirrors DependencyCard's body text rules: disabled ->
  // fixed copy, up -> "Responding (Nms)"/"Responding", down/other -> the
  // dependency's own error text or "Unreachable".
  dependencyBodyText(dep) {
    if (dep.status === "disabled") return t("Not enabled for this deployment.");
    if (dep.status === "up") {
      return dep.latency_ms !== undefined && dep.latency_ms !== null ? t("Responding ({ms}ms)", { ms: dep.latency_ms }) : t("Responding");
    }
    return dep.error || t("Unreachable");
  }

  async load() {
    try {
      const detail = await getHealthDetail();
      this.state.detail = detail;
      this.state.status = "ready";
      this.state.message = "";
    } catch (error) {
      this.state.status = "error";
      this.state.message = error instanceof Error ? error.message : t("Failed to load service health");
    }
  }

  refresh() {
    this.state.status = "loading";
    this.load();
  }
}
