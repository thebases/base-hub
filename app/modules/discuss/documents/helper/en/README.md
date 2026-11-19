# Discuss: user and administrator guide

## 1. Purpose and who uses it

`discuss` adds chatter (message threads), followers, scheduled activities, canned responses, chat channels, outbound/inbound email handling and call tracking to Base HUB.

| Audience | What they do with it |
| --- | --- |
| End users (`base.group_user`) | Post and read messages on records, follow records, schedule and complete activities, use canned responses, chat in channels |
| Administrators | Maintain activity types and canned responses, configure mail servers and inbound aliases, protect the inbound webhook |
| Integrators | Call the model RPC methods and the inbound-email webhook |

## 2. Prerequisites and install

- Depends on `base` and `base-bus` (live delivery of messages and notifications goes through the bus).
- Not a core module: `installable: true`, `auto_install: false`. Install it from the Apps list.
- On install it seeds five activity types (see section 6) and adds the chatter panel to the Contact (`tbl.partner`) form.
- Outbound email only works after at least one mail server is configured (section 6).

## 3. Key concepts

| Model | Plain-words meaning |
| --- | --- |
| `discuss.message` | One chatter message on any record, identified by `res_model` + `res_id`. Has Subject, Body, Message Type (Comment, Notification, Email, Auto Comment), Author, optional parent message for threading |
| `discuss.message.subtype` | A tag on a message that decides which followers get notified |
| `discuss.followers` | A user subscribed to a record, optionally limited to some subtypes |
| `discuss.notification` | The per-recipient delivery and read row for a message (Inbox type) |
| `discuss.activity.type` | A reusable template for to-dos: delay, delay unit, delay from, chaining type, icon |
| `discuss.activity` | A scheduled to-do on a record, with Deadline, Assigned To, Summary, Note. Overdue / today / planned is calculated from the deadline, not stored |
| `discuss.canned.response` | A shortcut text (Source) that expands to a longer text (Substitution), optionally limited to groups |
| `discuss.channel` | A Chat (1:1), Group (private) or Channel (broadcast) conversation; its messages are normal `discuss.message` rows |
| `discuss.channel.member` | A user's membership in a channel, with last-seen message and mute-until time |
| `discuss.channel.rtc.session` | A member currently in a live call (camera, screen share, muted, deafened flags). Removed when they leave |
| `discuss.call.history` | Permanent start/end record of each call |
| `discuss.guest` | An external, non-user participant (Name, Email, Access Token) |
| `discuss.alias` | An inbound email alias: local part of the address, target model, defaults, contact policy (Everyone / Followers Only) |
| `discuss.mail.server` | An outbound SMTP server (host, port, credentials, TLS, sequence) |
| `discuss.mail` | The outbound email queue: one row per send, state Outgoing, Sending, Sent, Exception, Cancelled |

## 4. Where to find it

| Location | What it gives you |
| --- | --- |
| Main menu > Canned Responses (`/web/tbl.discuss_canned_response`) | List and form for shortcut texts |
| Main menu > Activity Types (`/web/tbl.discuss_activity_type`) | List and form for activity templates |
| Contact form (`tbl.partner`) | Chatter panel for messages, followers and activities |
| `POST /discuss/inbound_email` | Webhook for inbound email (not a screen) |

There are no menus or screens yet for channels, calls, mail servers, the mail queue or aliases. Those models are reachable only through the generic model RPC/API.

## 5. Step-by-step tasks

### Talk about a record
1. Open a Contact and use the chatter panel.
2. Write a message and post it. Followers of the record receive a notification live.
3. Reply to a message to thread it under its parent.

### Follow or unfollow a record
Subscribe from the chatter panel. Optionally limit to specific subtypes. Unsubscribe the same way.

### Schedule an activity
1. Pick an activity type (Email, Call, Meeting, To Do, Upload Document).
2. Set the Deadline, the person it is Assigned To, and a Summary or Note.
3. When finished, mark it done. The completion is logged as an automatic message, and a chained activity may be suggested or triggered depending on the type's Chaining Type.

### Create an activity type
1. Open Activity Types, then New.
2. Fill Name, Sequence, Resource Model (optional), Icon, Decoration Type (Warning or Danger).
3. Set Delay Count, Delay Unit (Days, Weeks, Months), Delay From (Current Date or previous activity deadline), Chaining Type, and the optional next type.

### Create a canned response
Open Canned Responses, then New. Enter Source (the shortcut) and Substitution (the expanded text). Use Groups to restrict who sees it. Last Used At is updated when it is used.

### Use channels (API only today)
Create a channel (Chat, Group or Channel), join or leave, post messages, mark messages as seen, mute until a time. Use the matching runtime helpers via API.

## 6. Admin and configuration

- Access: every model has CRUD access for `base.group_user` (ACL in `security/sys_model_access.csv`). There is no separate manager group.
- Seed data: activity types Email, Call, Meeting, To Do, Upload Document (all Delay 0 days, chaining Suggest). They are refreshed on update.
- Mail server: create a `discuss.mail.server` row with Host, Port (default 587), Username, Password, Use TLS (default on), Sequence (lower is tried first). With none configured, queued mails are marked Exception with reason "no mail server configured".
- Mail sweep: a `sys.cron` handler named `discuss.send_queued_mail` sends queued mail. No schedule is seeded, so an administrator must create a cron entry that uses this handler.
- Inbound webhook secret: set config `app.discussInboundWebhookSecret`. Callers must send it as the `X-Discuss-Webhook-Secret` header (the `secret` query parameter is no longer accepted). If unset, the webhook is disabled and returns 403.
- Aliases: create a `discuss.alias` with Alias Name (local part, unique), Target Model, optional Defaults (JSON) and Force Thread ID, and Contact Policy. Only members of `base.group_system` can create, edit or delete aliases. Target Model must have a form view with the chatter enabled; `base.users`, `tbl.groups` and `sys.*`/`ui.*` models are refused.

## 7. API and automation touchpoints

| Touchpoint | Purpose |
| --- | --- |
| `tbl.discuss_message` `post_message` (model-level) | The supported way to post chatter; handles followers, notifications and live publish. Do not use raw create |
| `tbl.discuss_activity` `schedule_activity` (model-level) | Schedule an activity |
| `tbl.discuss_activity` `mark_done` (by record id) | Complete an activity |
| Generic model CRUD/RPC | All other `tbl.discuss_*` models |
| `POST /discuss/inbound_email` | Accepts a parsed email (`message_id_header`, `in_reply_to`, `references`, `from_email`, `to_emails`, `cc_emails`, `subject`, `body_html`); returns `status`, `res_model`, `res_id`, `message_id` |
| Bus topics | `discuss:channel:<id>` for channel events, `discuss:user:<id>` for personal notifications and call signaling |

Inbound routing order: match an existing thread by Message-ID, In-Reply-To or References; otherwise match the recipient local part to an alias; otherwise return `no_route` (still HTTP 200).

## 8. Troubleshooting and FAQ

| Problem | Check |
| --- | --- |
| Emails stay Outgoing | No cron schedules `discuss.send_queued_mail`; or the cron is inactive |
| Email state is Exception, "no mail server configured" | Add an active `discuss.mail.server` |
| Inbound email returns 401 | Secret missing or wrong |
| Inbound email returns `no_route` | No thread match and no alias with that local part, or the alias target model is not allowed |
| Inbound email returns 403 | `app.discussInboundWebhookSecret` is unset, or the `X-Discuss-Webhook-Secret` header is missing or wrong |
| Message does not notify followers | Message was created by raw create instead of `post_message`; or the follower's subtype filter excludes it |
| No chatter on a form | The form view must have chatter enabled (only the Contact form does by default) |

## 9. Limits and notes

- Outbound email only; there are no screens for channels, calls, mail servers, queue or aliases yet.
- Alias matching uses the local part only; there is no domain concept.
- No TURN/STUN or media transport: calls only track state and relay signaling through the bus.
- Guests have an identity, a token and can post a message. Session handling, per-record authorization and portal pages are not included; the caller must decide what a guest may touch.
- Notifications are delivered in-app (Inbox) only.
