---
name: base-calendar
version: 0.0.1
category: System
installable: true
depends: [base, base-automation]
auto_install: true
frontend_widgets: {}
---

# Base Calendar

Generic, model-agnostic calendar/recurrence engine backing the frontend-admin
calendar view (`.agents/knowledge/SRS/2026-08-03-frontend-admin-calendar-view-srs.md`).

This module owns:

- `base.calendar.recurrence` — a recurrence rule referencing any registered
  model's record by `(sys_model, record_id)`.
- `base.calendar.recurrence.exception` — a per-occurrence override/skip.

Background maintenance (orphaned-rule cleanup) is not a bespoke cron job —
it runs as a `base-automation` `go_handler` dispatched through the seeded
`automation.action`/`automation.cron` rows in `data/`, so this module reuses
the kernel scheduler + automation dispatch bridge `base-automation` already
provides instead of registering its own `sys.cron` row.

Like `base-credential`/`base-workflow`, this module depends on `base` and
`base-automation`.
