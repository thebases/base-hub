// Port of frontend-admin's modules/base-bus/bus-service.ts — the real
// WebSocket "bus" transport (with MQTT/polling fallbacks), byte-faithful to
// the React source's reconnect/backoff/multi-tab logic. Types dropped (plain
// JS here); the only substantive retargeting is the two imports at the top:
//   "@/services/api/config"  -> "@/services/api/config.js" (BUS_POLL_INTERVAL_MS,
//                                BACKEND_API_PREFIX — both already existed in
//                                this tree's config.js, confirmed before writing
//                                this file, see the task's Step 0 instructions)
//   "@/modules/base-bus/*"   -> "./*.js" (sibling files, same directory, no
//                                "@/" prefix needed — matches how the React
//                                source imports its own siblings)
//
// Everything else — the WS URL construction (protocol-swap + token/db/channels/
// last query params), the exponential-backoff reconnect loop, the multi-tab
// leadership gate before opening a socket, the MQTT/polling fallback paths,
// and the public subscribe/addChannel/deleteChannel/start/stop surface — is
// kept identical to the .ts source.
//
// Token-refresh note (see this repo's task instructions vs. the actual React
// source): bus-service.ts itself never calls authStore.performTokenRefresh()
// directly — there is no such call anywhere in the .ts file. Token refresh on
// this tree (same as the React app) instead flows through authStore's own
// reactive (401-triggered, via jsonv2.js's registerAuthRefreshHandler) and
// proactive (on-rehydrate, within REFRESH_SKEW_MS of expiry) refresh paths;
// whenever those update authStore's accessToken, bus-root.js's authStore
// subscription (mirroring BusProvider's effect) re-calls busService.setAuth()
// with the fresh token so the *next* reconnect picks it up. This file is a
// faithful port of that (lack of direct) behavior, not a simplification.

import { BUS_POLL_INTERVAL_MS, BACKEND_API_PREFIX } from "@/services/api/config.js";
import { createBusEventTarget } from "./event-bus.js";
import { createMultiTabService } from "./multi-tab.js";

const POLL_INTERVAL_MS = BUS_POLL_INTERVAL_MS;
const RETRY_BASE_MS = 1_000;
const RETRY_MAX_MS = 60_000;
// WS_FALLBACK_AFTER_FAILURES: consecutive /bus/ws handshakes that never
// reached `open` before this tab gives up on WebSocket and moves down the
// chain WebSocket -> SSE (/bus/sse, same real-time push over plain HTTP) ->
// HTTP polling. The same limit applies to SSE connects before polling. A proxy that strips the Upgrade header (nginx without
// proxy_http_version 1.1, Cloudflare WebSockets off, a gateway route without
// websocket enabled) makes every handshake fail with 426 — without this the
// tab would retry forever and never receive a notification.
const WS_FALLBACK_AFTER_FAILURES = 3;
const LAST_NOTIFICATION_KEY = "basehub.bus.lastNotificationId";
const BROADCAST_CHANNEL_NAME = "basehub.bus.notifications";
const INTERNAL_EVENTS = new Set(["BUS:INITIALIZED", "BUS:OUTDATED", "BUS:NOTIFICATION"]);

// readLastNotificationId restores the shared bus cursor used after refreshes
// and across browser tabs.
function readLastNotificationId() {
  return Number(localStorage.getItem(LAST_NOTIFICATION_KEY) ?? "0");
}

// writeLastNotificationId persists the latest cursor for missed-notification
// checks and future polls.
function writeLastNotificationId(id) {
  localStorage.setItem(LAST_NOTIFICATION_KEY, String(id));
}

// clearLastNotificationId drops the shared bus cursor so a different user
// logging in on the same browser doesn't resume polling from the previous
// user's position.
export function clearLastNotificationId() {
  localStorage.removeItem(LAST_NOTIFICATION_KEY);
}

// normalizeNotifications converts backend bus rows — served flat as
// {id, event_type, payload} by /websocket/peek_notifications, the MQTT
// bridge, and /bus/ws (base_service_bus.go's serviceBusRowMap has no
// "message" wrapper) — into the UI notification objects consumed by
// subscribers.
function normalizeNotifications(rows = []) {
  return rows.flatMap((row) => {
    const id = Number(row.id ?? 0);
    const type = row.event_type;
    if (!id || !type) return [];
    return [
      {
        id,
        type,
        payload: row.payload,
      },
    ];
  });
}

// createBusService ports legacy bus service into a plain-JS singleton using
// authenticated HTTP polling / MQTT / a real WebSocket against the current
// backend bus controller.
export function createBusService(options = {}) {
  const bus = createBusEventTarget();
  const notificationBus = createBusEventTarget();
  const multiTab = createMultiTabService();
  const subscribeFnToWrapper = new Map();
  const channels = new Set();
  let auth = options.auth;
  let isActive = false;
  let state = "IDLE";
  let pollTimeout;
  let retryDelay = RETRY_BASE_MS;
  let lastNotificationId = readLastNotificationId();
  let hasConnected = false;
  let activeTransport = "polling";
  let mqttClient;
  let mqttTopics = new Set();
  let wsURL;
  let wsSocket;
  let wsReconnectTimeout;
  let wsHandshakeFailures = 0;
  let sseURL;
  let sseSource;
  let sseReconnectTimeout;
  let sseConnectFailures = 0;
  const broadcastChannel =
    "BroadcastChannel" in globalThis ? new BroadcastChannel(BROADCAST_CHANNEL_NAME) : undefined;

  // onBroadcastMessage lets non-main tabs receive notifications polled by the
  // elected main tab without opening their own backend polling loop.
  function onBroadcastMessage(event) {
    const notifications = event.data?.notifications;
    if (Array.isArray(notifications)) {
      dispatchNotifications(notifications);
    }
  }

  broadcastChannel?.addEventListener("message", onBroadcastMessage);

  // updateState mirrors legacy worker state events for UI/debug subscribers.
  function updateState(nextState) {
    state = nextState;
    bus.trigger("BUS:WORKER_STATE_UPDATED", state);
  }

  // triggerConnectionEvent emits public connection lifecycle events.
  function triggerConnectionEvent(type, detail) {
    if (!INTERNAL_EVENTS.has(type)) {
      bus.trigger(type, detail);
    }
  }

  // dispatchNotifications fans bus notifications into local subscribers and
  // keeps the persisted cursor aligned with the newest delivered row. This is
  // the "send event" step from the transport's point of view — every
  // notification that reaches here is about to be handed to whatever
  // subscribed via `busService.subscribe(type, ...)`.
  function dispatchNotifications(notifications) {
    if (notifications.length === 0) return;

    lastNotificationId = notifications.at(-1)?.id ?? lastNotificationId;
    writeLastNotificationId(lastNotificationId);
    for (const notification of notifications) {
      // Dev-only debug log in the React source is gated on
      // `import.meta.env.DEV`; this static/no-bundler tree has no such flag
      // (confirmed: no DEV/isDev key anywhere in env.js/window.__ENV__), so
      // that console.info call is intentionally omitted rather than faked.
      bus.trigger("BUS:NOTIFICATION", notification);
      notificationBus.trigger(notification.type, {
        id: notification.id,
        payload: notification.payload,
      });
      bus.trigger(notification.type, notification.payload);
    }
  }

  // authHeaders builds the Bearer auth headers required by the backend bus
  // endpoints (matching jsonv2.js's rpcHeaders).
  function authHeaders() {
    const headers = {
      "Content-Type": "application/json",
      Authorization: `Bearer ${auth?.accessToken ?? ""}`,
    };
    // Match the RPC client behavior: only send the database header when the
    // authenticated session selected one explicitly.
    if (auth?.db) {
      headers["X-Database-Name"] = auth.db;
    }
    return headers;
  }

  // fetchTransportConfig lets the backend switch the bus transport at runtime
  // from the default WebSocket path to MQTT.
  async function fetchTransportConfig() {
    if (!auth?.accessToken) return undefined;
    const response = await fetch(new URL("/bus/transport_config", BACKEND_API_PREFIX).toString(), {
      method: "GET",
      headers: authHeaders(),
    });
    if (!response.ok) return undefined;
    return await response.json();
  }

  // fetchMqttSubscription asks the backend to normalize user-specific bus
  // channels and return the MQTT topics for those channel keys.
  async function fetchMqttSubscription() {
    if (!auth?.accessToken) return [];
    const response = await fetch(new URL("/bus/subscribe", BACKEND_API_PREFIX).toString(), {
      method: "POST",
      headers: authHeaders(),
      body: JSON.stringify({
        params: {
          channels: [...channels].sort(),
          last: lastNotificationId,
        },
      }),
    });
    if (!response.ok) {
      throw new Error(`Bus MQTT subscribe failed: ${response.status}`);
    }
    const data = await response.json();
    lastNotificationId = Number(data.result?.last ?? lastNotificationId);
    writeLastNotificationId(lastNotificationId);
    return Object.values(data.result?.topics ?? {}).filter(Boolean);
  }

  // handleMqttMessage converts MQTT payloads back into the RawBusNotification
  // shape already used by the HTTP polling fallback.
  function handleMqttMessage(_topic, payload) {
    try {
      const raw = new TextDecoder().decode(payload);
      const notification = JSON.parse(raw);
      dispatchNotifications(normalizeNotifications([notification]));
    } catch (error) {
      console.warn("Failed to parse MQTT bus message", error);
    }
  }

  // refreshMqttSubscriptions keeps broker topic subscriptions aligned with the
  // current channel set after login or channel changes.
  async function refreshMqttSubscriptions() {
    if (!mqttClient) return;
    const nextTopics = new Set(await fetchMqttSubscription());
    for (const topic of mqttTopics) {
      if (!nextTopics.has(topic)) {
        mqttClient.unsubscribe(topic);
      }
    }
    for (const topic of nextTopics) {
      if (!mqttTopics.has(topic)) {
        mqttClient.subscribe(topic, { qos: 1 });
      }
    }
    mqttTopics = nextTopics;
  }

  // startMqtt connects through the globally loaded mqtt.js bundle when the
  // backend reports MQTT as the active bus transport.
  async function startMqtt(config) {
    if (!config?.url || !window.mqtt) {
      throw new Error("mqtt.js is not available");
    }
    activeTransport = "mqtt";
    const clientId = `${config.client_id || "basehub_bus"}_${Math.random().toString(36).slice(2)}`;
    mqttClient = window.mqtt.connect(config.url, {
      clientId,
      username: config.username || undefined,
      password: config.password || undefined,
      clean: true,
      reconnectPeriod: RETRY_BASE_MS,
    });
    mqttClient.on("connect", () => {
      updateState("CONNECTED");
      triggerConnectionEvent(hasConnected ? "BUS:RECONNECT" : "BUS:CONNECT");
      hasConnected = true;
      void refreshMqttSubscriptions();
    });
    mqttClient.on("message", handleMqttMessage);
    mqttClient.on("close", (error) => {
      if (!isActive) return;
      updateState("DISCONNECTED");
      triggerConnectionEvent("BUS:DISCONNECT", error);
      triggerConnectionEvent("BUS:RECONNECTING", error);
    });
    mqttClient.on("offline", (error) => {
      if (!isActive) return;
      updateState("DISCONNECTED");
      triggerConnectionEvent("BUS:RECONNECTING", error);
    });
    mqttClient.on("error", (error) => {
      if (!isActive) return;
      updateState("DISCONNECTED");
      triggerConnectionEvent("BUS:DISCONNECT", error);
    });
  }

  // handleWSMessage parses one WebSocket text frame — the backend sends one
  // JSON-encoded base_service_bus row per message, no framing/wrapper beyond
  // that.
  function handleWSMessage(event) {
    try {
      const row = JSON.parse(event.data);
      const notifications = normalizeNotifications([row]);
      dispatchNotifications(notifications);
      if (notifications.length > 0) {
        broadcastChannel?.postMessage({ notifications });
      }
    } catch (error) {
      console.warn("Failed to parse WebSocket bus message", error);
    }
  }

  // scheduleWSReconnect applies the same exponential-backoff shape the old
  // polling loop used, but re-enters through ensureWSConnection (which
  // re-checks main-tab leadership) instead of jumping straight back into a
  // new WebSocket.
  function scheduleWSReconnect(delay = retryDelay + Math.random() * RETRY_BASE_MS) {
    window.clearTimeout(wsReconnectTimeout);
    if (!isActive) return;
    wsReconnectTimeout = window.setTimeout(() => {
      void ensureWSConnection();
    }, delay);
  }

  // connectWS opens the long-lived /bus/ws connection. Auth can't use the
  // Authorization header here — the native WebSocket API cannot set custom
  // headers on its handshake request — so the access token (and db, in place
  // of X-Database-Name) travel as query params instead; the backend
  // (serviceBusWebSocketIdentity) validates them the same way it validates
  // the Bearer header everywhere else.
  function connectWS(url) {
    if (!auth?.accessToken) return;
    const requestUrl = new URL(url, BACKEND_API_PREFIX);
    requestUrl.protocol = requestUrl.protocol === "https:" ? "wss:" : "ws:";
    requestUrl.searchParams.set("channels", [...channels].sort().join(","));
    requestUrl.searchParams.set("last", String(lastNotificationId));
    requestUrl.searchParams.set("token", auth.accessToken);
    if (auth.db) {
      requestUrl.searchParams.set("db", auth.db);
    }

    const socket = new WebSocket(requestUrl.toString());
    wsSocket = socket;
    let opened = false;

    socket.onopen = () => {
      opened = true;
      wsHandshakeFailures = 0;
      updateState("CONNECTED");
      triggerConnectionEvent(hasConnected ? "BUS:RECONNECT" : "BUS:CONNECT");
      hasConnected = true;
      retryDelay = RETRY_BASE_MS;
    };
    socket.onmessage = handleWSMessage;
    socket.onerror = () => {
      // A close event always follows an error event for WebSocket, so
      // reconnect scheduling lives in onclose — this handler intentionally
      // stays a no-op (the React source only logged here behind
      // import.meta.env.DEV, which has no equivalent in this static tree).
    };
    socket.onclose = (event) => {
      if (wsSocket !== socket) return; // superseded by a newer connection already
      wsSocket = undefined;
      if (!isActive) return;
      if (event.code === 1000) {
        // Normal closure: addChannel/deleteChannel intentionally closed this
        // socket to reopen it with an updated channel list — go again
        // immediately instead of applying failure backoff.
        scheduleWSReconnect(0);
        return;
      }
      updateState("DISCONNECTED");
      triggerConnectionEvent("BUS:DISCONNECT", event);
      triggerConnectionEvent("BUS:RECONNECTING", event);
      // Only handshakes that never opened count toward the fallback: a
      // socket that was live and then dropped is a normal reconnect case.
      if (!opened && ++wsHandshakeFailures >= WS_FALLBACK_AFTER_FAILURES) {
        fallBackFromWS();
        return;
      }
      retryDelay = Math.min(retryDelay * 1.5, RETRY_MAX_MS);
      scheduleWSReconnect();
    };
  }

  // fallBackFromWS runs after repeated /bus/ws handshake failures (a proxy
  // is stripping the Upgrade header). SSE keeps real-time push over plain
  // HTTP when the backend advertises it; otherwise go straight to polling.
  // The switch lasts for this page load only, so the next load retries
  // WebSocket and a fixed proxy restores it with no frontend change.
  function fallBackFromWS() {
    window.clearTimeout(wsReconnectTimeout);
    retryDelay = RETRY_BASE_MS;
    if (sseURL && typeof EventSource !== "undefined") {
      console.warn(
        `[bus] /bus/ws handshake failed ${wsHandshakeFailures} times in a row; switching to SSE (${sseURL}). ` +
          "A reverse proxy is likely stripping the WebSocket Upgrade header."
      );
      activeTransport = "sse";
      void ensureSSEConnection();
      return;
    }
    fallBackToPolling(`/bus/ws handshake failed ${wsHandshakeFailures} times in a row`);
  }

  // fallBackToPolling moves the tab to the HTTP polling loop, the last
  // transport in the chain, for the rest of this page load.
  function fallBackToPolling(reason) {
    window.clearTimeout(wsReconnectTimeout);
    window.clearTimeout(sseReconnectTimeout);
    console.warn(`[bus] ${reason}; falling back to HTTP polling.`);
    activeTransport = "polling";
    retryDelay = RETRY_BASE_MS;
    void loop();
  }

  // scheduleSSEReconnect mirrors scheduleWSReconnect for the SSE transport.
  function scheduleSSEReconnect(delay = retryDelay + Math.random() * RETRY_BASE_MS) {
    window.clearTimeout(sseReconnectTimeout);
    if (!isActive) return;
    sseReconnectTimeout = window.setTimeout(() => {
      void ensureSSEConnection();
    }, delay);
  }

  // connectSSE opens /bus/sse. EventSource can't set headers either, so the
  // token/db/channels/cursor travel as query params exactly like /bus/ws.
  // EventSource's built-in auto-reconnect is disabled (close on error) so
  // reconnects rebuild the URL with the current channel list and cursor.
  function connectSSE(url) {
    if (!auth?.accessToken) return;
    const requestUrl = new URL(url, BACKEND_API_PREFIX);
    requestUrl.searchParams.set("channels", [...channels].sort().join(","));
    requestUrl.searchParams.set("last", String(lastNotificationId));
    requestUrl.searchParams.set("token", auth.accessToken);
    if (auth.db) {
      requestUrl.searchParams.set("db", auth.db);
    }

    const source = new EventSource(requestUrl.toString());
    sseSource = source;
    let opened = false;

    source.onopen = () => {
      opened = true;
      sseConnectFailures = 0;
      updateState("CONNECTED");
      triggerConnectionEvent(hasConnected ? "BUS:RECONNECT" : "BUS:CONNECT");
      hasConnected = true;
      retryDelay = RETRY_BASE_MS;
    };
    // Each SSE `data:` line is one JSON bus row — the same payload a
    // WebSocket frame carries, so the WS message handler parses it as-is.
    source.onmessage = handleWSMessage;
    source.onerror = () => {
      source.close();
      if (sseSource !== source) return; // superseded by a newer connection already
      sseSource = undefined;
      if (!isActive) return;
      updateState("DISCONNECTED");
      triggerConnectionEvent("BUS:DISCONNECT");
      triggerConnectionEvent("BUS:RECONNECTING");
      if (!opened && ++sseConnectFailures >= WS_FALLBACK_AFTER_FAILURES) {
        fallBackToPolling(`/bus/sse failed to connect ${sseConnectFailures} times in a row`);
        return;
      }
      retryDelay = Math.min(retryDelay * 1.5, RETRY_MAX_MS);
      scheduleSSEReconnect();
    };
  }

  // reopenSSE restarts the stream immediately so a channel change takes
  // effect (the channel list is baked into the URL, like /bus/ws).
  function reopenSSE() {
    sseSource?.close();
    sseSource = undefined;
    scheduleSSEReconnect(0);
  }

  // ensureSSEConnection is the SSE counterpart of ensureWSConnection: only
  // the elected main tab holds a stream; other tabs get rows via
  // BroadcastChannel.
  async function ensureSSEConnection() {
    if (!isActive || activeTransport !== "sse" || !sseURL) return;
    if (!(await multiTab.isOnMainTab())) {
      updateState("IDLE");
      scheduleSSEReconnect(5_000);
      return;
    }
    if (hasConnected && (await checkHasMissedNotifications())) {
      bus.trigger("BUS:OUTDATED");
    }
    connectSSE(sseURL);
  }

  // ensureWSConnection is the single re-entry point for (re)connecting the
  // WebSocket transport: every reconnect and every "am I still the elected
  // tab" recheck funnels back through here so there is only one place that
  // can open a new socket.
  async function ensureWSConnection() {
    if (!isActive || activeTransport !== "ws" || !wsURL) return;
    if (!(await multiTab.isOnMainTab())) {
      updateState("IDLE");
      scheduleWSReconnect(5_000);
      return;
    }
    if (hasConnected && (await checkHasMissedNotifications())) {
      bus.trigger("BUS:OUTDATED");
    }
    connectWS(wsURL);
  }

  // pollOnce performs one subscribe+poll round through
  // /websocket/peek_notifications and dispatches any returned notifications.
  async function pollOnce() {
    if (!auth?.accessToken) return;
    const response = await fetch(new URL("/websocket/peek_notifications", BACKEND_API_PREFIX).toString(), {
      method: "POST",
      headers: authHeaders(),
      body: JSON.stringify({
        params: {
          channels: [...channels].sort(),
          last: lastNotificationId,
          is_first_poll: lastNotificationId === 0,
        },
      }),
    });

    if (!response.ok) {
      throw new Error(`Bus poll failed: ${response.status}`);
    }

    const data = await response.json();
    const notifications = normalizeNotifications(data.result?.notifications);
    // Dev-only poll-summary log omitted, same reason as dispatchNotifications
    // above — no import.meta.env.DEV equivalent in this static tree.
    dispatchNotifications(notifications);
    if (notifications.length > 0) {
      broadcastChannel?.postMessage({ notifications });
    }
  }

  // checkHasMissedNotifications asks the backend whether the last known cursor
  // was garbage-collected while the client was disconnected.
  async function checkHasMissedNotifications() {
    if (!auth?.accessToken || !lastNotificationId) return false;
    const response = await fetch(new URL("/bus/has_missed_notifications", BACKEND_API_PREFIX).toString(), {
      method: "POST",
      headers: authHeaders(),
      body: JSON.stringify({
        params: { last_notification_id: lastNotificationId },
      }),
    });
    if (!response.ok) return false;
    const data = await response.json();
    return Boolean(data.result);
  }

  // schedulePoll queues the next poll while the service remains active.
  function schedulePoll(delay = options.pollIntervalMs ?? POLL_INTERVAL_MS) {
    window.clearTimeout(pollTimeout);
    if (!isActive) return;
    pollTimeout = window.setTimeout(() => {
      void loop();
    }, delay);
  }

  // loop keeps the polling lifecycle running and applies backend-driven reconnect
  // backoff after transient transport failures.
  async function loop() {
    if (!isActive) return;
    if (!(await multiTab.isOnMainTab())) {
      updateState("IDLE");
      schedulePoll();
      return;
    }

    try {
      updateState("CONNECTED");
      if (hasConnected && (await checkHasMissedNotifications())) {
        bus.trigger("BUS:OUTDATED");
      }
      await pollOnce();
      retryDelay = RETRY_BASE_MS;
      triggerConnectionEvent(hasConnected ? "BUS:RECONNECT" : "BUS:CONNECT");
      hasConnected = true;
      schedulePoll();
    } catch (error) {
      updateState("DISCONNECTED");
      triggerConnectionEvent("BUS:DISCONNECT", error);
      triggerConnectionEvent("BUS:RECONNECTING", error);
      retryDelay = Math.min(retryDelay * 1.5, RETRY_MAX_MS);
      schedulePoll(retryDelay + Math.random() * RETRY_BASE_MS);
    }
  }

  // start activates the bus transport once credentials are available. MQTT
  // wins when the backend reports it (never today — no browser-reachable
  // broker is configured); a real WebSocket (`/bus/ws`, real-time push via
  // Postgres LISTEN/NOTIFY) is the default. If its handshake keeps failing
  // (proxy stripping Upgrade), fallBackFromWS moves to SSE (`/bus/sse`, when
  // advertised) and then to polling. Polling is also used directly for
  // browsers without the WebSocket API, or if the backend ever reports
  // something other than "mqtt"/"websocket".
  async function start() {
    if (isActive) return;
    isActive = true;
    updateState("CONNECTING");
    const transportConfig = await fetchTransportConfig();
    if (transportConfig?.result?.transport === "mqtt") {
      try {
        await startMqtt(transportConfig.result.mqtt);
        return;
      } catch (error) {
        console.warn("Falling back to polling bus transport", error);
      }
    }
    const wsSupported = typeof WebSocket !== "undefined";
    const wsEndpoint = transportConfig?.result?.ws?.url;
    if (wsSupported && wsEndpoint) {
      activeTransport = "ws";
      wsURL = wsEndpoint;
      sseURL = transportConfig?.result?.sse?.url;
      triggerConnectionEvent("BUS:RECONNECT");
      await ensureWSConnection();
      return;
    }
    activeTransport = "polling";
    triggerConnectionEvent("BUS:RECONNECT");
    await loop();
  }

  // stop leaves the bus and releases pending timers for this tab.
  function stop() {
    isActive = false;
    window.clearTimeout(pollTimeout);
    window.clearTimeout(wsReconnectTimeout);
    window.clearTimeout(sseReconnectTimeout);
    wsSocket?.close(1000);
    wsSocket = undefined;
    sseSource?.close();
    sseSource = undefined;
    mqttTopics = new Set();
    mqttClient?.end(true);
    mqttClient = undefined;
    updateState("DISCONNECTED");
    triggerConnectionEvent("BUS:DISCONNECT", { code: 1000 });
  }

  return {
    get isActive() {
      return isActive;
    },
    get lastNotificationId() {
      return lastNotificationId;
    },
    get workerState() {
      return state;
    },
    addEventListener: bus.addEventListener,
    removeEventListener: bus.removeEventListener,
    trigger: bus.trigger,
    addChannel(channel) {
      channels.add(channel);
      if (!isActive) return;
      if (activeTransport === "mqtt") {
        void refreshMqttSubscriptions();
      } else if (activeTransport === "ws") {
        // The channel list is baked into the socket's query string at
        // connect time, so a change requires reopening it.
        wsSocket?.close(1000);
      } else if (activeTransport === "sse") {
        reopenSSE();
      } else {
        void pollOnce();
      }
    },
    deleteChannel(channel) {
      channels.delete(channel);
      if (!isActive) return;
      if (activeTransport === "mqtt") {
        void refreshMqttSubscriptions();
      } else if (activeTransport === "ws") {
        wsSocket?.close(1000);
      } else if (activeTransport === "sse") {
        reopenSSE();
      }
    },
    setAuth(nextAuth) {
      auth = nextAuth;
    },
    start,
    stop,
    subscribe(notificationType, callback) {
      const wrapper = (event) => {
        callback(structuredClone(event.detail.payload), {
          id: event.detail.id,
        });
      };
      subscribeFnToWrapper.set(callback, wrapper);
      notificationBus.addEventListener(notificationType, wrapper);
    },
    unsubscribe(notificationType, callback) {
      const wrapper = subscribeFnToWrapper.get(callback);
      if (wrapper) {
        notificationBus.removeEventListener(notificationType, wrapper);
        subscribeFnToWrapper.delete(callback);
      }
    },
    destroy() {
      stop();
      broadcastChannel?.removeEventListener("message", onBroadcastMessage);
      broadcastChannel?.close();
      multiTab.unregister();
    },
  };
}

export const busService = createBusService();
