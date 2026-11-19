---
name: project
version: 0.0.1
category: Services
installable: true
depends: [base, base-calendar, discuss]
auto_install: false
frontend_widgets: {}
---

# Project

Full-parity project management module for Base Hub (see the project module's
implementation plan under `.agents/planning/`).

This module owns:

- `tbl.project` — a project header: sequence/customer/manager/color/labels,
  visibility, start/end dates, task-dependency/milestone/recurring-task
  toggles, stage, tags, template flag, favorites, and last-status-update
  pointer.
- `tbl.project_stage` — project kanban stage.
- `tbl.project_task_type` — **task** kanban stage (shared across projects via
  a many2many, not owned by exactly one project); a row with `user_id` set is
  a personal-stage template instead of a shared project stage.
- `tbl.project_task_stage_personal` — per-(task,user) personal Kanban column
  join row, auto-populated for a task's assignees.
- `tbl.project_tag`, `tbl.project_role` — free-form labels / placeholder
  task roles.
- `tbl.project_milestone` — a deliverable/checkpoint with a deadline and
  computed task counts.
- `tbl.project_update` — a status-update feed entry; creating one mirrors its
  status back onto the parent project's `last_update_status`/`last_update_id`.
- `tbl.project_collaborator` — an internal project-sharing grant (no portal
  mechanics — this repo has no portal concept).
- `tbl.project_task` — the task itself: stage, tags, kanban state (computed
  from task-dependency blocking, reusing the exact grey/blocked/ready
  convention `helpdesk`'s ticket model already established), dates,
  nullable `project_id` (a task with no project is a private to-do —
  `project_todo` parity falls out for free), assignees, roles, subtasks
  (parent/child with computed counts), milestone link, a directional
  dependency graph (`depend_on_ids` stored, `dependent_ids` computed inverse
  via a raw-Bun pivot query, never a second recursive Write), and recurrence.

**Recurrence** delegates to `base-calendar`'s generic `base.calendar.recurrence`
model instead of a bespoke recurrence table — a recurring task's
`recurrence_id` points at a `base.calendar.recurrence` row
(`sys_model="tbl.project_task"`), created via `orm.ExecuteRuntimeMethod` (no
direct Go import of `modules/base-calendar`). This module's own
`recurrence_service.go` only owns the "spawn the next task occurrence when
this one closes" business logic.

RuntimeMethods: `tbl.project_milestone.toggle_is_reached`,
`tbl.project_stage.unlink_with_reassign` /
`tbl.project_task_type.unlink_with_reassign` (reassign-then-delete, folding
the reference's separate wizard models into one RuntimeMethod parameter).
Chatter is reached via `PostProjectMessage`/`PostTaskMessage` (Go-level
wrappers over `tbl.discuss_message.post_message` through
`orm.ExecuteRuntimeMethod`) — never a direct create on `tbl.discuss_message`.

MCP tools (`project.record.*`) cover every model above through one
model-enum-scoped tool group, mirroring `base-credential`'s MCP layer shape.

**Explicitly out of scope** (see the plan's §1/§4 for full reasoning): the
reference-only enterprise/portal/accounting surface — `project_account` (no
accounting module), `project_enterprise` (Gantt/Map enterprise webclient
assets, this repo's frontend is a from-scratch generic `ModelView`),
`project_forecast` (no resource-planning app), `project_mail_plugin` (zero
backend models), portal sharing wizards/tokens/URLs, the dynamic "Properties"
custom-fields framework, and project/task templates (deferred to a future
phase).

**Project ↔ Helpdesk integration** (ticket/task conversion) is NOT owned by
this module — it lives in a separate `project-helpdesk` bridge module
(`depends: [project, helpdesk]`, `auto_install: true`) so the feature only
ever activates once both modules are installed, per this repo's
Decoupled Plugin Module Pattern. See
`app/modules/project-helpdesk/manifest.md`.

`project` is not a core module (`base`/`base-*`): `installable: true`,
`auto_install: false`. No custom Fiber routes/handlers — generic
model-registry CRUD/RPC plus the RuntimeMethods above cover this module's
surface.
