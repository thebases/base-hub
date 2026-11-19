# CHANGELOG

All notable changes to **Base Hub** are documented in this file. The format follows [Keep a Changelog](https://keepachangelog.com/). New releases go on top as dated sections.

## [v1.0.0] - 2026-11-19

This is the first major release. It brings `master` up to the consolidated `base-erp` line, which carries the `dev` kernel plus the project/helpdesk business modules. Highlights are a security-hardening pass across the kernel and modules, the business modules (discuss, project, helpdesk, website-blog), a native website builder, full i18n, a raw TCP workflow trigger, and the move of the admin UI to NodexUI. Summarized from the commits since v0.0.4 (2026-08-20 → 2026-11-19, ~370 commits). This entry also covers the interim `base-erp` releases v0.1.0 and v0.2.0, which were never tagged on `master`.

### Upgrade notes (breaking)

- **Repository layout**: `app/backend/` moved to `app/`. Build, CI, `go.work` and docs paths now point at `app/`.
- **Toolchain**: Go 1.25 and Fiber v3. Binaries are built with `CGO_ENABLED=0`.
- **Model naming**: business models use the `tbl.` prefix (for example website, discuss, knowledge and helpdesk models), and many2many relation tables use the `tbl_` prefix. Clients that call models by their old names must be updated.
- **Runtime alias routes** moved to `/base/<route>`.
- **Dashboards** are now `ui.view` records with `type="dashboard"`. `sys.module.dashboard` was removed, and existing external ids are re-owned during upgrade.
- **Removed**: the React admin SPA (`frontend-admin`, `/admin`), the vendored `base-web-editor`/Studio editor, and the unauthenticated Kafka `/publish/:topic` route.
- **Frontend UI library**: moved from `@thebase/ui` to `nodexui` (pinned at 1.3.5), and the import map changed from `@base/*` to `nodexui/*`. Module `static/` widgets that import `@base/*` must be updated.
- **Configuration**: `app.encryptKey` is file-only (rotate it with `rotate-encrypt-key`). `postgresql.templateDbName` is read from config at boot instead of `sys.config_param`. A new `tcp` section has been added and is off by default.
- **Seeds**: default user passwords were removed. Set the admin password with `reset-admin-password` after `init-db`.
- **Lua**: workflow `lua_script` steps now reference saved automation actions. Inline Lua steps are migrated on startup.
- **View files** are named `<ui type>-<ui name>.xml`, and `ui.menu` seed data moved to XML views.

### Added

- **Discuss module**: chatter, followers, activities, canned responses, mail with a sealed SMTP password, and an inbound mail webhook. Views opt in with the `ui.view` `use_chatter` flag.
- **Project**, **Helpdesk** and **Project-Helpdesk** modules: tasks and stages (To Do / New), recurring tasks, KPI dashboards, ticket numbers from the generic `use_sequence` rule, sample data, and a ticket-to-task bridge.
- **Website-blog module**: blogs, posts, tags, SEO, public `/blog` pages, RSS, a sitemap and MCP tools.
- **Native website builder**: replaces the Studio editor. Adds page templates with a New-page popup, header/footer chrome editing, logged-in-only pages, contact requests, a seeded home page, and site files served under `/_site`.
- **New view types**: cohort, gantt, grid, hierarchy and map, plus list `<dashboard>` stat tiles, kanban column reorder, a many2one relation record dialog, and a `text_editor` form widget.
- **i18n**: canonical language codes (`vi_vn`), a shell language switcher, `.po` catalogs per module, `t()` across the core and modules, and API docs in English and Vietnamese (`?lang=`). The UI follows the user's `lang_id`.
- **Raw TCP server and workflow TCP trigger**: framers, TLS/mTLS, the PROXY protocol, limits, and synchronous raw replies.
- **Bus SSE fallback** (`/bus/sse`) for when `/bus/ws` keeps failing.
- **`base.kvt`** key/value/type model with public `GET /kvt/:key`, used for the timezone list and the menu cache.
- **Generic `use_sequence` field rule** and `sys.sequence.peek_by_code`.
- **Company switcher and user menu** in the navbar. Multi-company scoping across the auth, automation, calendar, credential, workflow and helpdesk modules.
- **CLI commands**: `init-db`, `reset-admin-password`, `rotate-encrypt-key`, `repair-attnum-slots`, `model <module> <entity>` (scaffold), `model check`, and `i18n extract`.
- **PWA**: `/manifest.webmanifest` is served from `sys.config_param` `pwa.*` rows, with an offline-first service worker.
- **API docs**: an Alias API tab (build-time OpenAPI 3.1), descriptions per model and method, sample request bodies, and an in-app Instruction page with helper docs per module.
- **Live collaborative editing** in the Knowledge markdown editor (Yjs + y-webrtc).
- **MQTT supervisor**: self-healing resubscribe, bounded first connect, QoS 1 enforced on every publish and subscribe, per-topic metrics, and an MQTT Monitor page in workflow.
- **CI**: a `check:frontend` job (UI pin and i18n msgid checks), the `tag:build-and-release` job, and `sync-to-github` for `v*` tags.

### Changed

- **ORM model registration** now uses `orm.Define` with a `ModelSet` for each module, together with typed runtime-method adapters (`orm.Typed`).
- **Record-rule and company guards** now run before model create/write/unlink overrides. `sys.rule` denies access when no group rule matches.
- **Workflow engine**: a shared attempt loop, an outbox sweeper, Kafka commits after each offset, run leases, atomic canvas saves, synchronous runs, and trigger registries that are tenant-aware.
- **Frontend performance**: lazy-loaded view types, dialogs and the Shell, a flat signed-in preload, fewer data-load round trips on `/web`, and embedded assets served with real `Last-Modified`/ETag headers.
- **Responsive UI**: a drawer below 992px and card layouts for mobile lists. The form Save button stays hidden until the record changes.
- **Module lifecycle**: install-guarded routers, `auto_install` cascades in both directions, manually installed modules are kept on restart, and runtime manifest discovery prefers `./modules`.
- **Datetimes** are displayed in the user's address timezone.
- Odoo branding was removed from code, comments and docs.

### Security

- **Client overrides**: `/rpc` and MCP strip client-supplied `sudo`, `uid` and context overrides, and `RuntimeMethod.Internal` methods cannot be called from client transports.
- **Secrets**: secret field metadata is never returned, filtered or sorted on. Credential secrets are encrypted under `app.encryptKey` and stripped from reads, and `/bus/transport_config` no longer returns broker credentials.
- **Privilege escalation**: paths from `group_system` to superadmin through OAuth clients, config params and constraint DDL were closed. Writes to `base.users` and `tbl.groups` are guarded, and `base.database` actions require superadmin.
- **Multi-database**: request database names are validated against the tenant registry, and the template database is rejected.
- **OAuth**: `client_credentials` no longer issues refresh tokens. CSRF and grant checks were tightened, and login is rate-limited.
- **Workflow**: SSRF guards on HTTP steps. `/wh` serves webhook workflows only. Credential refs resolve only from authored step params, and only `base.group_system` can author workflows.
- **Website**: deploy paths are server-managed and confined to the tenant public root, and custom head/footer HTML is superadmin-only.
- **Bus, discuss and attachments**: bus channels are authorized on every transport, chatter access follows the target record, and the inbound mail webhook rejects requests on any failure.
- **Frontend**: an HTML sanitizer was added, the service worker is bypassed for sensitive routes, and a Casdoor `app.conf` that held plaintext credentials was removed.

### Fixed

- **Postgres 1600-column cap**: a guard was added on `CREATE TABLE` as well as `ALTER`, and `sys_model_field` creation is deduplicated and capped per model. Creation without these limits caused a production incident.
- Startup no longer crashes when the workflow trigger sync races the schema auto-migrate. Interrupted seed imports on a fresh database now retry.
- False-positive "database not available" 403s during reconnects. Cron skips tenant databases that no longer exist.
- `ExpandAutoInstall` recursed forever through the MCP cache resync.
- Master data now adopts existing rows by natural key when an external id is stale.
- ORM fixes: bare `YYYY-MM-DD` date fields, `False` domain values now map to `NULL` on non-boolean fields, non-stored related fields are excluded from `INSERT`, `-field` order parsing works, and user `company_ids` are read from the relation table.
- Frontend fixes: import map ordering, relation options on first render, `KnowledgeTree` this-binding, the collaborative editor `editorState` race, and the delete-confirm dialog close.
- Website deploy paths now resolve against an absolute shared folder.

---

## [v0.0.4] - 2026-08-20

Full admin frontend rewrite onto Owl 3 + a new pure-Owl BaseUI component library, replacing the previous React admin. Ships the workflow canvas-layout view, real-time bus notifications over WebSocket/MQTT, desktop sidebar collapse, PWA install support, and the BaseUI `Shell` compound component. Also adds multi-tenant database routing via header/query param and several MCP/knowledge fixes. Summarized from the full commit history (2026-08-09 → 2026-08-20, 24 commits).

### Added

- **Owl 3 admin frontend**: full parity rewrite of the admin SPA (dashboard, model views, field-widget catalog, x2many editing, base-view dialogs) on Owl components, replacing the prior React frontend.
- **BaseUI component library**: pure-Owl static/dynamic component set (`Sidebar`, `Shell` compound — `ShellSidebar`/`ShellNav`/`ShellMain`/`ShellHeader`/`ShellContent`/`ShellCollapseTrigger` — `Toast`, dashboard blocks) with vendor bundles (`baseui.esm.js`/`.iife.js`/`.css`) and icon set upgrade.
- **Workflow canvas-layout view**: pan/zoom/drag DAG layout editor with persisted node positions, plus AI action dialog and additional DAG param support.
- **Real-time bus notifications**: WebSocket/MQTT-based live notification/toast delivery to the shell, plus a health-check surface.
- **Desktop sidebar collapse** and **PWA install drawer**, both persisted across reloads.
- **Multi-tenant database routing** via request header or query param, with import-filter and self-heal fixes for module/masterdata provisioning.
- `knowledge.doc.find` MCP lookup tool; stringified MCP tool argument acceptance.

### Changed

- **Shell layout**: migrated from a hand-wired `Sidebar`/`SidebarContent` grid to BaseUI's native `Shell` compound, which now owns grid/sidebar/collapse mechanics.
- Moved control-plane provisioning into the database module; reordered tenant DB-name resolution and deduped cross-tenant routing checks.
- Website module: dropped page versioning in favor of draft + published only; added SPA/PWA manifest field for `base-web-editor` sync.
- `base-mcp`: added Streamable HTTP transport lifecycle (session id, protocol-version, Accept header) and `knowledge.*`/`workflow.*` tool groups.

### Fixed

- Stray `<templates>` DOM wrapper breaking the Owl ESM build.
- Template DB provisioning gate and website runtime path reservation.
- Module-data import filter dropping `base-workflow`/`base-mcp` seed data.
- `/oauth/authorize` falling back correctly to the `X-Database-Name` header when the `db` param is absent.

---

## [v0.0.3] - 2026-08-09

OAuth 2.0/OIDC rework (authorization code flow, RBAC migration, external-IdP pilot), a real-time WebSocket bus replacing HTTP polling, an extracted `base-bus` module with browser MQTT transport, and a module-lifecycle rework (install/reinit UI, uninstall/enable fixes). Summarized from the full commit history (2026-08-04 → 2026-08-09, 19 commits).

### Added

- **OAuth 2.0 Authorization Code flow** for GPT Actions with per-user `oauth_client` auto-provisioning; `/auth` OAuth authorize aliases preserved.
- **OIDC RBAC migration**, including external-IdP pilot bug fixes.
- **base-bus module**: extracted from the bus notification code, with a browser MQTT transport and `action_client` dispatch.
- Real-time WebSocket push replacing the previous HTTP polling for bus updates.
- `base-mcp`: `knowledge.*` and `workflow.*` MCP tool groups; Streamable HTTP transport groundwork.

### Changed

- Reworked module lifecycle: install/reinit UI, uninstall/enable fixes, masterdata reimport safety.
- Refactored OAuth clients and config namespaces.
- Website module aligned with the `base-web-editor` migration; removed page versioning (draft + published only); added SPA/PWA manifest field.
- Renamed `Config.Markdown` to `Config.Knowledge`; tenant-scoped the knowledge docs viewer.

### Fixed

- `base.users` view-arch attribute error.
- Auth route split and view renames after the OAuth refactor.

---

## [v0.0.2] - 2026-08-03

Adds a full website-building module (GrapesJS-based builder, static publishing, versioned pages), a generic calendar module, a module lifecycle manager, and a substantially hardened MCP gateway. Also ships a sys.model constraint/catalog system, an identifier rename across the codebase, and an Odoo-reference cleanup pass. Summarized from the full commit history (2026-07-27 → 2026-08-03, 53 commits).

### Added

- **Website module**: page builder on GrapesJS OSS, runtime persistence, Bootstrap-based deployment, per-page static asset publishing with `DeployPath`, single-draft-per-page model, and Studio document uploads.
- **base-calendar module**: generic calendar view with a recurrence engine and day/week/month/year/schedule frontend views.
- **sys.module.module lifecycle**: manual module scan plus install/uninstall/reinit actions.
- **sys.model.constraint**: PostgreSQL check/unique/unique_index constraints and a business-rule catalog.
- **base.database lifecycle**: stop terminates all connections, unlink drops the database, plus a re-init action.
- **MCP gateway** (`base-mcp`): user-scoped bearer auth with generic model/record tools, model-definition CRUD tools (`base.model.create_custom`, `base.model.add_field`), relation metadata plus delete/rename field tools with a publish-guard, response trimming, pagination, batch/atomic writes, idempotency, chunked HTML upload, and name normalization.
- **base-attachment module**: scoped file uploads with a form-arch widget (later merged into `sys.attachment`).
- Fourth workflow trigger node supporting run-by-record-event in `base-workflow`.
- `hasUi` field on `sys.model`, list-view action button area, and a createUI button for `sys.model`.
- Admin auto-logout on own password change; Security tab hidden while editing.
- `sys_model_name` combobox widget for admin search.

### Changed

- **Identifier rename**: `ResID`/`tbl_id`/`XMLID` → `ModelId`/`model_id`/`BID` across backend and frontend.
- Removed `xml_id` entirely; fixed related model-publish bugs.
- `sys.model.*` catalog readonly enforcement and operation registry; form pager now works for direct-linked/readonly records.
- Promoted metadata-only `sys.model` rows into live runtime models.
- Reworked worker pool handling; added control-plane tenant statistics.
- Completed Odoo-reference wording cleanup across backend, frontend, and docs.
- Rewrote the base-mcp user guide as a task-focused prompting guide; documented Base MCP Custom GPT setup.
- General admin UI refactor.

### Fixed

- `base.model.publish` returning zero field rows for custom models.
- `base.users` password/api_key exposed on read — now masked.
- `ui.view` `search_read` ignoring `ilike`/domain operators.
- Admin SPA refresh 404 on dotted model routes; per-app build-version prefixes.
- `website.page.version` partial writes; MCP test isolation.
- Auto-create-custom-model and `sys.model` view bugs.
- General build script failures.

---

## [v0.0.1] - 2026-07-27

Initial release. This version establishes the core workflow automation platform: a visual DAG-based workflow builder (canvas), a backend execution engine, credential management, and supporting infrastructure. Summarized from the full commit history (2026-07-15 → 2026-07-27, 32 commits).

### Added

- Initial project scaffolding, configuration, and menu UI.
- **Workflow module**: visual canvas for building workflows, node CRUD, JSON import/export.
- **Kahn's Algorithm DAG execution mode** — hybrid backend/frontend implementation for dependency-ordered step execution.
- **Live step status via service bus**, replacing the previous 5-second polling mechanism.
- **Manual "Run Now"** test execution for workflows.
- **base-automation module** — centralizes cronjob and automation management for both workflows and credentials.
- **Credential management module**, including secure change-password support.
- **"Post to Object" action** — HTTP action node with configurable headers and retry logic.
- New mobile UI action button and a dedicated user-facing (`user-fe`) page.
- Database module for persistence of workflow/automation state.
- LUA scripting support.
- Landing page.

### Changed

- Refactored and enhanced the workflow module (canvas rendering, action pipeline).
- General UI enhancements across workflow and mobile views.
- Workflow actions now support customized headers.

### Fixed

- Workflow canvas not refreshing after creating a new workflow.
- Deleting a workflow no longer leaves the user on a stale page — now auto-redirects to workflow ID 1.
- Node CRUD issue where the model dialog failed to display.
- Auto-migration issue on startup.
- Canvas save bug and workflow run display bug.
- Retry logic and UI for the "Post to Object" action.
- Frontend and general build script failures.
- On-change logic bug in credential management.

---
