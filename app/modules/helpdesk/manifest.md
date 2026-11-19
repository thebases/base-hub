---
name: helpdesk
version: 0.0.1
category: Support
installable: true
depends: [base,base-auth,base-calendar,base-mcp,base-automation,base-bus,discuss]
auto_install: false
frontend_widgets: {}
---

# Helpdesk

Full-featured helpdesk ticketing module for Base Hub — tickets, teams, stages,
SLA policies, chatter, cron sweeps, team KPIs, and analytics (see
`.agents/memory/context/helpdesk-module-context.md` for the implementation history).

This module owns:

- `tbl.helpdesk_ticket` — a support ticket: `subject`/`description`/
  `stage_id`/`priority`/`assignee_id` (original scaffold fields) plus team,
  stage, tags, ticket type, category, channel, partner, kanban color,
  close/assign dates, SLA deadline, sequence number, last-stage-update,
  unattended flag, duplicate tracking, symmetric related tickets, and the
  polymorphic source-document fields used by `perform_action`.
- `tbl.helpdesk_ticket_comment` — legacy comment model, kept as-is
  alongside chatter (see `.agents/memory/context/helpdesk-module-context.md`
  for why it was not migrated onto chatter).
- `tbl.helpdesk_team` — support team: visibility, assignment method, SLA/
  rating/auto-close toggles, membership, ticket-type restriction, partner-
  response autoupdate, close-inactive sweep config, activity-linked stage.
- `tbl.helpdesk_stage`, `tbl.helpdesk_tag`, `tbl.helpdesk_ticket_type`,
  `tbl.helpdesk_ticket_category`, `tbl.helpdesk_ticket_channel`.
- `tbl.helpdesk_sla` / `tbl.helpdesk_sla_status` — SLA policy matching and
  per-ticket deadline tracking (fixed-duration, no working-calendar).
- `tbl.helpdesk_rating` — customer satisfaction rating, posts a chatter note.
  `Label` (`top`/`ok`/`ko`, compute) drives the list view's decoration
  colors in place of a bespoke smiley-badge widget.
- `tbl.helpdesk_ticket_activity_link` — join row between a ticket and the
  `tbl.discuss_activity` it scheduled via `perform_action`.
- `tbl.helpdesk_digest_log` — one row per daily digest cron run per team:
  a durable snapshot of tickets-closed/SLA-success-rate/avg-rating over the
  prior 24 hours (see `helpdesk.digest` below). Historical/audit trail only —
  the live numbers are `tbl.helpdesk_team`'s own KPI fields.

`tbl.helpdesk_team` additionally carries seven read-only KPI compute fields
(`open_ticket_count`, `unassigned_ticket_count`, `urgent_ticket_count`,
`sla_failed_count`, `ticket_closed_7d`, `success_rate_7d` /
`avg_rating_7d` — the last two use a `-1` sentinel for "no SLA/rating data
this window") backed by `team_kpi_service.go`'s raw-Bun aggregates, plus
three plain editable per-team targets (`target_closed_weekly`,
`target_success_rate`, `target_rating`) a manager sets as a goal — a
per-team deviation from a per-user target, since `base.users` is a shared
core model outside this module's ownership.

`tbl.helpdesk_ticket` also ships a `type: cohort` `ui.view`
(`date_start=created_at`, `date_stop=close_date`) for ticket-lifecycle
retention/churn analysis — the first real consumer of the repo's cohort
view type (backend RPC + frontend renderer already existed, unused). No
graph/pivot analysis views were added: neither exists anywhere in this repo
yet (no chart library wired in), and building them would be new core
frontend infrastructure, not a helpdesk-scoped change — team/stage/assignee/
priority breakdowns are available via the existing list view's generic
group-by control instead.

The install-level dashboard (`views/dashboard-helpdesk.xml`) gained three
more static-domain count cards (Unassigned/Urgent/SLA Failed) alongside the
existing Open Tickets card — `search_count`-based cards can't express a
relative-date filter, so "closed today/7 days" style numbers live on the
team KPI fields above instead.

RuntimeMethods: `tbl.helpdesk_ticket.perform_action` (schedule a linked
activity + advance stage), `tbl.helpdesk_ticket.complete_source_activity`
(mark that activity done + advance stage). Chatter is reached via
`PostTicketMessage` (Go-level wrapper over `tbl.discuss_message.post_message`
through `orm.ExecuteRuntimeMethod`) — never a direct create on
`tbl.discuss_message`.

Cron handlers (`helpdesk.sla_sweep`, `helpdesk.auto_close`,
`helpdesk.close_inactive_tickets`, `helpdesk.digest`) are registered at boot
as `base-automation` go_handlers (never a direct kernel `sys.cron`
registration) and are scheduled out of the box via seeded
`automation.action`/`automation.cron` rows
(`data/13_automation_actions_helpdesk.json`,
`data/14_automation_crons_helpdesk.json`). `helpdesk.digest` runs daily,
snapshotting every team's 24-hour KPIs into `tbl.helpdesk_digest_log` — no
outbound email is sent (no mail-template renderer exists in this repo, same
simplification already applied to the close-inactive sweep's chatter-note
notifications). Ticket numbering
(`tbl.helpdesk_ticket.number`) is issued from the kernel's generic
`sys.sequence` service (code `helpdesk.ticket`, seeded by
`data/05_sys_sequence_ticket_number.json`), not a bespoke per-module counter.

`helpdesk` is not a core module (`base`/`base-*`): `installable: true`,
`auto_install: false`. It now depends on `discuss` for chatter/activities in
addition to `base`/`base-auth`/`base-calendar`/`base-mcp`/`base-automation`/
`base-bus`. No custom Fiber routes/handlers — generic model-registry CRUD/RPC
plus the RuntimeMethods above cover this module's surface.
