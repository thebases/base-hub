---
name: discuss
version: 0.0.1
category: Discuss
installable: true
depends: [base, base-bus, base-credential]
auto_install: false
frontend_widgets: {}
---

# Discuss

Generic chatter/mail-thread core for Base Hub.
Phase 1 (chatter core) and Phase 2 (channels/chat) are both implemented.

This module owns:

- `discuss.message` — a polymorphic chatter message keyed off an arbitrary
  `(res_model, res_id)` target, with `subject`, `body`, `message_type`
  (`comment`/`notification`/`email`/`auto_comment`), an optional `subtype_id`,
  `author_id`, self-referencing `parent_id` for threading, and an optional
  `message_id_header` (unique) storing the email Message-ID this message
  corresponds to, used to match inbound replies back to it.
- `discuss.message.subtype` — a classification tag on a message that also
  drives which followers receive a notification for it.
- `discuss.followers` — a `(res_model, res_id, user_id)` subscription row,
  optionally scoped to a subset of subtypes.
- `discuss.activity.type` — a reusable activity template (delay/chaining
  rules, decoration, icon).
- `discuss.activity` — a scheduled to-do/reminder on a target record; no
  persisted state column, `overdue`/`today`/`planned` is always derived from
  `date_deadline` at read time.
- `discuss.canned.response` — a reusable text shortcut, optionally scoped to
  groups.
- `discuss.alias` — an inbound-mail alias definition (`alias_model_id` is a
  plain dotted model-name string, not a foreign key — this repo's model
  registry has no FK-by-id precedent). Routing is live: `POST
  /discuss/inbound_email` (a provider-agnostic webhook — see
  `routes/inbound_email_route.go`) accepts a parsed inbound email and
  `models/inbound_router.go`'s `RouteInboundEmail` either matches it to an
  existing thread via `message_id_header`/In-Reply-To/References, matches the
  recipient's local-part against an alias, or reports `no_route` (never
  errors on an unroutable email, bouncing-without-crash
  behavior). Address matching is local-part-only — no `alias_domain` concept
  yet. The webhook requires config `app.discussInboundWebhookSecret` sent as the
  `X-Discuss-Webhook-Secret` header; unset means the webhook is disabled (403).
  Alias targets must be models with a chatter view (not `base.users`,
  `tbl.groups`, `sys.*`, `ui.*`), and only `base.group_system` can manage aliases.
- `discuss.notification` — the per-recipient fan-out/read-tracking row for a
  message, delivered live over `base-bus`'s `base.service.bus.publish`
  (no outbound email in this repo).
- `discuss.channel` — a chat/group/channel container (`channel_type`
  `chat`/`group`/`channel`); its messages are ordinary `discuss.message` rows
  keyed off `(res_model="discuss.channel", res_id=channel.id)`, no separate
  message table.
- `discuss.channel.member` — a `(channel_id, user_id)` membership row with a
  `last_seen_message_id` read receipt and a `mute_until` timestamp.
- `discuss.mail.server` — a configurable outbound SMTP server definition
  (host/port/credentials/TLS/sequence). Outbound email requires configuring
  at least one `discuss.mail.server` row — this is an operational
  prerequisite, not a code gap; with none configured, queued mail is simply
  marked `exception` with reason "no mail server configured".
- `discuss.mail` — the outbound email queue, one row per send attempt
  (`state`: `outgoing`/`sent`/`exception`/`cancel`), optionally mirroring a
  `discuss.message`.
- `discuss.channel.rtc.session` — one row per member currently in a channel's
  live call (`is_screen_sharing_on`/`is_camera_on`/`is_muted`/`is_deaf`);
  deleted when that member leaves the call (ephemeral, like `discuss.activity`
  — history lives in `discuss.call.history`, not here). This module has no
  TURN/STUN server or media transport of its own; it only tracks state and
  relays opaque SDP/ICE signaling payloads through `base-bus`.
- `discuss.call.history` — a durable start/end ledger row per call on a
  channel, linked to the `discuss.message` announcing it started.
- `discuss.guest` — a minimal identity (`name`/`email`/`access_token`) for an
  external, non-`base.users` participant, as a guest.
  `discuss.message` gained an `author_guest_id` field (alongside the existing
  `author_id`) so a message can be authored by a guest instead of an
  internal user. **Deliberately narrow scope**: `CreateGuest`/
  `AuthenticateGuestToken`/`PostMessageAsGuest` (`models/guest_service.go`)
  give a guest an identity, a bearer token, and a way to post a chatter
  message — they do NOT include session/cookie handling, per-record
  authorization checks, or any portal HTTP route/UI. A caller of
  `AuthenticateGuestToken`/`PostMessageAsGuest` is responsible for deciding
  which record a given guest/token may actually touch; this module has no
  opinion on that. Full portal auth integration remains a separate, larger,
  not-yet-started task.

`discuss` is not a core module (`base`/`base-*`): it is `installable: true`,
`auto_install: false`, and depends on `base` plus `base-bus` (for the
`base.service.bus.publish` runtime method `PostMessage` and the Phase 2/3
channel/RTC helpers call). It gets generic model-registry CRUD/RPC for free
through `orm.NewHandlerWithModel` for every model above — the only custom Go
logic is the `PostMessage`/`Subscribe`/`Unsubscribe` chatter helpers in
`models/service.go`, the Phase 2 `CreateChannel`/`GetOrCreateChat`/
`JoinChannel`/`LeaveChannel`/`MarkSeen`/`SetMute`/`PostChannelMessage`
channel helpers in `models/channel_service.go`, the outbound-email
`EnqueueMail`/`SendQueuedMail` helpers in `models/mail_sender.go`, inbound
routing (`RouteInboundEmail` in `models/inbound_router.go`), and the WebRTC
`StartRtcSession`/`EndRtcSession`/`UpdateRtcSessionState`/`RelaySignal`
helpers in `models/rtc_service.go`. All realtime delivery (message posts,
join/leave, read receipts, mute changes, call lifecycle, and point-to-point
SDP/ICE signaling) goes through the same `base.service.bus.publish` call
Phase 1 already uses — no new WebSocket/pub-sub transport anywhere in this
module. Call lifecycle events broadcast on the channel's own topic
(`discuss:channel:<id>`); point-to-point signaling targets the recipient's
own topic (`discuss:user:<id>`), matching Phase 1's per-user notification
channel. Outbound email sends over the standard `net/smtp` (with
`crypto/tls` for TLS-configured servers) and is swept by a
`sys.cron`-registered handler (`discuss.send_queued_mail`); no sys.cron seed
row is included in this pass, so the sweep runs once an operator or another
module schedules one. Inbound email routing is exposed over one custom Fiber
route (`RegisterRoutes` in `discuss.go` → `routes/inbound_email_route.go`),
the module's only departure from pure model-registry CRUD/RPC, since an
external mail relay can't authenticate through the RPC Bearer-token path. No
TURN/STUN server or media transport is provisioned by this module — that is
entirely a client-side/infrastructure concern outside its scope. No UI/menu/
views exist yet for channels, calls, or for managing mail servers/the
outbound queue/aliases; that is deferred to a later frontend pass.
