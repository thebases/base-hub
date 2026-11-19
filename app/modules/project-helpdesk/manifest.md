---
name: project-helpdesk
version: 0.0.1
category: Services
installable: true
depends: [project, helpdesk]
auto_install: true
frontend_widgets: {}
---

# Project Helpdesk

Bridge module converting between `tbl.helpdesk_ticket` and `tbl.project_task`
(`project_helpdesk` parity with the reference module; see the project
module's implementation plan under `.agents/planning/`, §3). Deliberately
its own module rather than code inside `project` or
`helpdesk` themselves: `depends: [project, helpdesk]` +
`auto_install: true` means this repo's existing install-lifecycle (a module
only installs once every entry in `Depends` is installed, and
`ExpandAutoInstall` cascades an `auto_install` module in automatically the
moment its dependencies are all satisfied) makes the conversion feature
appear the instant BOTH `project` and `helpdesk` are installed, and disappear
if either is removed — with no `queries.ModuleInstalled` checks scattered
inside either module's own code.

Owns two Fiber endpoints (`internal/modules.HTTPRouteRegistrar`, not generic
model RPC, since converting between two different models is a cross-model
business action rather than plain CRUD on one model):

- `POST /project-helpdesk/tickets/:id/convert-to-task` — body
  `{"project_id": <id>, "stage_id": <id, optional>}`. Copies
  `name`(=ticket subject)/`description`/`partner_id` onto a new
  `tbl.project_task`, archives the source ticket, and cross-posts a chatter
  note on both records via `helpdesk`'s `PostTicketMessage`/`project`'s
  `PostTaskMessage`.
- `POST /project-helpdesk/tasks/:id/convert-to-ticket` — body
  `{"team_id": <id>, "stage_id": <id, optional>}`. The inverse.

Every model touch goes through `orm.ExecuteRuntimeMethod`/
`orm.RuntimeRepository*` — this module never imports `modules/project` or
`modules/helpdesk`'s Go packages directly, keeping both sides fully
independent/uninstallable per the Decoupled Plugin Module Pattern.

Not a core module (`base`/`base-*`): `installable: true`. It is, however,
`auto_install: true` (unlike `project`/`helpdesk` themselves), since its only
purpose is to activate automatically once both are present.

**UI**: `data/01_master_project_helpdesk_actions.json` binds a "Convert to Task"
action on the ticket form and a "Convert to Ticket" action on the task form
(bound client actions opening the `project_helpdesk_convert` dialog widget),
so neither module's own views are touched.
