# Base Bus: user and administrator guide

## 1. Purpose and audience

Base Bus is the always-on service bus of Base HUB. It carries real-time events from the backend to the browser (toasts, "data changed" refreshes, menu updates, workflow events) and keeps a durable log of every event so a client that reconnects can catch up on what it missed.

| Audience | What they do with it |
| --- | --- |
| End users | Nothing directly. They simply see live notifications and refreshes. |
| Administrators / operators | Check that live updates work, inspect recent events, configure the browser transport (WebSocket or MQTT) and the reverse proxy. |
| Developers / AI agents | Publish and poll events through the `bus.*` MCP tools or the `base.service.bus` model. |

## 2. Prerequisites and installation

| Item | Detail |
| --- | --- |
| Depends on | `base` |
| Category | System |
| Install | Automatic (`auto_install`). The module is always installed and cannot be disabled or uninstalled. |
| Database | PostgreSQL (LISTEN/NOTIFY is used to wake up other server instances). |
| Optional | Nothing extra: browser push uses WebSocket or SSE. Browser MQTT is disabled (see "MQTT for browser push"). |

## 3. Key concepts

| Concept | Plain-words meaning |
| --- | --- |
| Service Bus (`base.service.bus`) | The durable, append-only log of events. One row per event per channel. |
| Channel | A named stream. `servicebus:global` is seen by everyone; `servicebus:user:<id>` is private to one user. Every user is subscribed to both by default. |
| Event Type | A short label describing the event (for example `action_client`). |
| Payload | Free-form JSON data attached to the event. |
| Cursor (`last`) | The id of the last event a client received. On reconnect the client resumes after this id. |
| Transport | How events reach the browser: WebSocket (default), MQTT over WebSocket (currently disabled), SSE or HTTP long-poll (fallbacks). |

Fields of a bus record: Channel, Event Type, Payload, Created By, Created At.

## 4. Where to find it

The module has no menus and no screens of its own. Its HTTP endpoints are used by the Base HUB web client automatically.

| Endpoint | Method | Purpose |
| --- | --- | --- |
| `/bus/transport_config` | GET | Tells the client which transport to use. |
| `/bus/subscribe` | POST | Resolves the channel list and the starting cursor. |
| `/bus/ws` | GET (WebSocket) | Default real-time push stream. |
| `/bus/sse` | GET | Same push stream over plain streaming HTTP (no Upgrade needed). |
| `/websocket/peek_notifications` | POST | HTTP long-poll fallback. |
| `/bus/has_missed_notifications` | POST | Tells a client whether its cursor event no longer exists. |
| `/websocket/health` | GET | Health check for the bus transport. |

## 5. Step-by-step tasks

### Check that live updates work
1. Log in to Base HUB in two browsers.
2. Change data in one (or trigger a workflow that notifies users).
3. The other browser should show the toast or refresh without a page reload.

### MQTT for browser push (disabled)
`/bus/transport_config` always reports `websocket` (with the SSE fallback), even when `mqtt.public_ws_url` is set. The only broker account is the server's own, and sending it to browsers would let any signed-in user publish or subscribe as the server. Browser MQTT returns once the broker issues short-lived per-user credentials with topic ACLs. Server-side MQTT (workflow triggers, device traffic) is not affected.

### Inspect recent events (administrator)
1. Use the MCP tool `bus.recent` (newest first) or `bus.record.search_read` on `base.service.bus`.
2. To follow another user, filter `bus.record.search_read` by channel: `bus.recent` only returns channels you may receive yourself (global, your own, your discuss channels).

### Publish a test event (developer)
1. As a `base.group_system` user, call `bus.publish` with `channels`, `event_type` and a `payload` object (other users get `permission_denied`).
2. Every subscribed browser receives it immediately and a row is stored per channel.

## 6. Administration and configuration

| Topic | Detail |
| --- | --- |
| Groups / ACL | `security/sys_model_access.csv` ships no extra rules. Access to `base.service.bus` records follows the normal ACL and record rules. |
| Seeds | None. |
| Config keys | `mqtt.public_ws_url` is currently ignored for browsers (see "MQTT for browser push"). |
| MQTT security | Broker credentials are never sent to a browser. |
| Reverse proxy | `/bus/ws` needs WebSocket upgrade support (see Troubleshooting). |

## 7. API and automation touchpoints

| MCP tool | Purpose |
| --- | --- |
| `bus.publish` | Publish an event on one or more channels (system administrators only). |
| `bus.poll` | Events newer than `last_id` on given channels plus the caller's defaults. |
| `bus.recent` | Most recent events, newest first (default 50). |
| `bus.last_id` | Highest event id stored, a starting cursor for `bus.poll`. |
| `bus.get_default_channels` | The caller's default channels. |
| `bus.record.search_read` / `bus.record.read` | Read-only record access to `base.service.bus` (system administrators only). |

There is no create, write or delete tool: the log is append-only, and `bus.publish` is the only write path.

## 8. Troubleshooting and FAQ

| Symptom | Cause and fix |
| --- | --- |
| `/bus/ws` returns 426 | The Upgrade header was stripped by a proxy. For nginx set `proxy_http_version 1.1`, pass `Upgrade` and `Connection` headers, and use a long `proxy_read_timeout`. With Cloudflare, enable Network, WebSockets. |
| `/bus/ws` returns 401 | Missing or invalid `token` query parameter, or the token belongs to another database. |
| No live updates, but data is correct | Falls back to the SSE or long-poll transport; check the browser console and proxy. |
| Client misses events after being offline | It resumes from its last cursor; if the cursor row was purged, `has_missed_notifications` returns true and the client should refresh. |
| Server hangs on shutdown | Should not happen: open `/bus/ws` connections are released on shutdown signal. Report if seen. |

## 9. Limits and notes

- Postgres NOTIFY is only a wake-up signal. The stored row is the source of truth.
- WebSocket and SSE authenticate with a `token` query parameter because browsers cannot set headers on those connections.
- In MQTT mode events are published directly to the broker (QoS 1) and the NOTIFY path is unused.
- The module is not removable; it is a dependency of workflow event delivery.
