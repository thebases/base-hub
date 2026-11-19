---
name: base-bus
version: 0.1.0
category: System
installable: true
depends: [base, base-auth]
auto_install: true
frontend_widgets: {}
---

# Base Bus

Kernel-adjacent service-bus module for Base Hub.

Owns the durable frontend/backend event stream (`base.service.bus`) and every
transport that delivers it:

- WebSocket push (`/bus/ws`), backed by Postgres LISTEN/NOTIFY for
  multi-instance fan-out — the default, always-available transport.
- Browser-facing MQTT over WebSocket, used instead of the WebSocket transport
  when `mqtt.enable` and `mqtt.public_ws_url` are both configured. The broker
  itself fans out to every subscribed browser in that mode, so the
  LISTEN/NOTIFY wake-up path is unused.
- HTTP long-poll (`/websocket/peek_notifications`) as the fallback for
  clients without WebSocket support.

Like `base-automation`, this module is always installed and cannot be
disabled or uninstalled.
