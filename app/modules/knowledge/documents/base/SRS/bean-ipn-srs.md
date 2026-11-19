# SRS: Bean IPN — Software Requirements Specification

| Field | Value |
|---|---|
| Author | thebase-tech-guy |
| Status | Draft |
| Version | 1.1 |
| Date | 2026-05-18 |
| Related PRD | — |
| Reviewers | Engineering Lead, QA Lead |

---

## 1. Introduction

### 1.1 Purpose

This document specifies the functional and non-functional requirements for the **Bean IPN** platform (branch: `Group_task_A_postgresql`). It is the primary reference for engineers implementing new features, QA engineers writing test plans, and architects reviewing system design decisions.

This document covers the current state of the system as of 2026-05-18 and the immediate development backlog. Version 1.1 incorporates changes from the following shipped commits: multi-database `db_filter` auto-discovery, fresh-install auto-bootstrap, workflow trigger type `http → webhook` rename, workflow action handler catalog consolidation, outbound delivery model refactor, and system-model metadata `active`-field removal.

It does **not** cover future addon modules outside the `base` and `workflow` kernels.

### 1.2 Scope

Bean IPN is a Go-based backend platform that provides:

- A metadata-driven model registry and ORM runtime (the "kernel").
- An IPN (Instant Payment Notification) processing pipeline for financial partner integrations (Napas, MB Bank).
- A Server-Driven UI (SDUI) layer with XML view arches, React frontend, and RPC transport.
- A durable workflow engine (`base.workflow`) backed by persistent metadata, with canvas-based node editing.
- A durable service bus for real-time frontend polling and inter-service pub/sub.
- An outbound HTTP delivery service (`base.workflow.outbound`) with retry semantics.
- A module system for packaging and loading domain-specific addons.
- Multi-database and multi-company scoping, including `db_filter` regex auto-discovery.

**Excluded from this document:**
- CI/CD pipeline configuration.
- Infrastructure provisioning (Kubernetes, Docker).
- Third-party partner API specifications (Napas, MB Bank schemas are treated as external contracts).
- Admin UI screens that are planned but not yet seeded into `ui.view`.

### 1.3 Definitions and abbreviations

| Term | Definition |
|---|---|
| IPN | Instant Payment Notification — a server-to-server callback from a payment gateway to report transaction status |
| ORM | Object-Relational Mapper — library mapping Go structs to PostgreSQL tables |
| SDUI | Server-Driven UI — architecture where the backend returns UI layout descriptors instead of data only |
| RBAC | Role-Based Access Control |
| ETL | Extract, Transform, Load — data processing pipeline |
| Bun | Go SQL ORM library used as the primary persistence driver |
| Fiber | Go HTTP framework used for routing and middleware |
| RPC | Remote Procedure Call — the JSON-RPC-style transport used between the frontend and backend |
| AOM | Adaptive Object Model — the design pattern for storing model structure as runtime metadata |
| sys.* | System metadata tables (sys.model, sys.model.field, etc.) |
| res.* | Resource models (res.users, res.partner, res.country, res.lang) |
| ui.* | UI metadata models (ui.view, ui.menu, ui.actions.window) |
| base.* | Business domain models in the base module |
| XML ID | External identifier for a named record — used in seed data and cross-reference resolution |
| Kafka | Distributed message broker used for inbound IPN event delivery |
| MQTT | Message-queue telemetry protocol used for lightweight IoT/device pub/sub |
| ERP | External ERP system receiving callback data from the IPN pipeline |
| noupdate | Seed record flag that prevents re-import from overwriting manual edits |
| TOCTOU | Time-of-Check to Time-of-Use — a class of race condition in read-then-write logic |
| db_filter | PostgreSQL-POSIX regex applied against `pg_database.datname` at startup to auto-register matching databases |
| webhook | HTTP POST endpoint trigger type for `base.workflow.trigger` (replaces the legacy `http` trigger type) |
| canvas | Visual node-and-edge editor in the frontend used to configure `base.workflow` pipelines |
| node spec | `base.workflow.node.spec` — the JSON-serialized parameter schema for a single workflow action node |

### 1.4 References

- AI Context snapshots: `.agents/memory/context/frontend-context.md`, `.agents/memory/context/backend-context.md`
- Repo change log: `.agents/memory/log/`
- Metadata-driven pattern research: `.agents/knowledge/metadata-drive-and-sdui/metadata-driven-model-registry-pattern.md`
- SDUI research: `.agents/knowledge/metadata-drive-and-sdui/sdui-research.md`
- Base module manifest: `app/modules/base/manifest.md`
- Workflow module manifest: `app/modules/base-workflow/manifest.md`

---

## 2. System Overview

Bean IPN is a modular-monolith Go service deployed as a single binary that embeds its frontend bundle, module assets, and base documentation. It presents three distinct surfaces to external clients:

1. **HTTP/RPC surface** — A Fiber web server handles browser sessions, RPC method dispatch, service-bus polling, workflow webhooks, and static asset serving.
2. **Kafka surface** — A Kafka consumer receives inbound payment notifications, routes them through the ETL pipeline, and triggers outbound callbacks.
3. **MQTT surface** — An MQTT client handles lightweight device/IoT pub/sub.

### Execution path

```
HTTP or Kafka or MQTT
  → route / consumer / subscriber
    → service layer (banketl, etlflow, modelruntime, outbounddelivery)
      → internal/orm (runtime dispatch, RBAC, domain compilation)
        → Bun repository
          → PostgreSQL
```

### Primary subsystems

| Subsystem | Package | Responsibility |
|---|---|---|
| Metadata Registry | `internal/orm` | Stores and resolves model definitions, fields, relations, and access rules at runtime |
| ORM Runtime | `internal/orm` | Executes CRUD, search, domain filtering, relation writes, and RBAC for any registered model |
| Model Bootstrap | `internal/models`, `modules/base/models`, `modules/base-workflow/models` | Registers Go-backed kernel and addon models into the runtime registry |
| Master-data Import | `internal/services/masterdata` | Seeds and verifies JSON/CSV/XML data from module directories on startup |
| RPC Transport | `internal/routes/rpc_route.go` | Translates HTTP JSON-RPC calls into ORM runtime dispatches; enforces auth |
| Workflow Webhooks | `modules/base-workflow/routes/workflow_route.go` | Receives `/wh/*` payloads and dispatches them to active workflow triggers of type `webhook` |
| Workflow Node Spec | `modules/base-workflow/routes/workflow_node_spec_route.go` | Serves action node parameter schemas for the canvas editor |
| ETL Engine | `modules/base-workflow/service/service.go` | Executes active `base.workflow` pipelines step-by-step with audit logging |
| Bank ETL | `internal/services/banketl` | Handles Napas and MB Bank-specific routing and ERP callback logic |
| Outbound Delivery | `modules/base-workflow/service/outbound_delivery.go` | Asynchronously delivers HTTP/MQTT callbacks to external targets; model `base.workflow.outbound` |
| Service Bus | `internal/routes/service_bus_route.go` | Durable pub/sub channel for frontend real-time notifications |
| SDUI / View System | `internal/models/ui_view.go`, `internal/routes` | Resolves `ui.view` XML arches into frontend-renderable payloads |
| Module System | `internal/modules`, `modules/` | Loads, registers, and seeds addon modules in dependency order |
| Config Runtime | `internal/config/config.go` | Loads, syncs, and persists config from YAML file and `sys_config_param` table |
| Frontend | `app/frontend-admin/` | Vite + React SPA embedded into the Go binary; renders SDUI payloads including workflow canvas |

### Data stores

| Store | Purpose |
|---|---|
| PostgreSQL (primary) | All persistent data — models, users, views, ETL metadata, IPN records, callbacks |
| Kafka | Inbound IPN message queue |
| MQTT broker | Device / IoT pub/sub |

---

## 3. Functional Requirements

---

### SRS-F-001: User Authentication

**Description:** The system must authenticate users via username/password submitted to `/oauth/token`. Successful authentication establishes a session and returns an API key for subsequent calls.

**Inputs:**

| Input | Source | Type | Validation rules |
|---|---|---|---|
| `db` | Request body | string | Must be a registered database name |
| `login` | Request body | string | Non-empty |
| `password` | Request body | string | Non-empty |

**Processing:**
1. Resolve the named database from the multi-db registry.
2. Look up the user record in `users` table by `login`.
3. Verify the stored password using bcrypt comparison (`$2*` prefix), then refuse plaintext and SHA-1 fallbacks (see SRS-F-002).
4. Load the user's group membership from `userroles`.
5. Generate or retrieve a stored `authtoken` for the user.
6. Return session data.

**Outputs:**

| Output | Destination | Type | Description |
|---|---|---|---|
| `uid` | Response body | int64 | Authenticated user ID (0 if unauthenticated) |
| `api_key` | Response body | string | Stored `authtoken` — never the raw password |
| `session_id` | Response body | string | Session identifier |
| `valid` | Response body | bool | True only when identity is verified |

**Error conditions:**

| Condition | System behavior | Error response |
|---|---|---|
| Database not found | Reject | 400 with `db not found` |
| User not found | Reject | 401 with `Authentication failed` |
| Password mismatch | Reject | 401 with `Authentication failed` |
| No stored authtoken | Return `api_key: ""` | 200 with empty `api_key` (do **not** echo password) |

**Acceptance criteria:**
- [ ] Bcrypt password accepted; login succeeds.
- [ ] Plaintext password stored in DB is rejected after the plaintext branch is removed.
- [ ] SHA-1 password triggers a forced bcrypt migration on next successful login.
- [ ] `api_key` in response is never equal to the submitted plaintext password.
- [ ] Unauthenticated `get_session_info` returns `uid: 0`, `valid: false`.

**Priority:** Must

---

### SRS-F-002: Password Storage Security

**Description:** The system must store all user passwords as bcrypt hashes. Plaintext and SHA-1 passwords must not be accepted as valid credentials. SHA-1 passwords must trigger migration to bcrypt on the next successful login.

**Inputs:**

| Input | Source | Type | Validation rules |
|---|---|---|---|
| Stored password | `users.password` | string | Must be `$2*` bcrypt format |
| Submitted password | Request body | string | Plaintext candidate |

**Processing:**
1. Detect bcrypt by `$2*` prefix — compare with `bcrypt.CompareHashAndPassword`.
2. If stored password does not start with `$2*`, reject authentication and emit a security warning log.
3. If the stored password is SHA-1 format and was previously accepted, rehash with bcrypt and update the row in a single transaction, then return success.

**Error conditions:**

| Condition | System behavior | Error response |
|---|---|---|
| Plaintext match | Reject; log security warning | 401 `Authentication failed` |
| SHA-1 format detected | Trigger migration; accept this once | 200 after migration write |

**Acceptance criteria:**
- [ ] `matchesStoredPassword` has no plaintext comparison branch.
- [ ] SHA-1 passwords trigger exactly one migration write and then succeed.
- [ ] All bcrypt comparisons use `bcrypt.CompareHashAndPassword` (constant-time).

**Priority:** Must

---

### SRS-F-003: API Key Authentication (Constant-Time Comparison)

**Description:** All API key comparisons in middleware and RBAC must use constant-time byte comparison to prevent timing-based enumeration attacks.

**Inputs:**

| Input | Source | Type | Validation rules |
|---|---|---|---|
| `X-API-Key` header | HTTP request | string | Compared against configured key |

**Processing:**
1. Extract the API key from the `X-API-Key` header.
2. Compare using `subtle.ConstantTimeCompare([]byte(candidate), []byte(configured))`.
3. Reject if comparison returns 0.

**Acceptance criteria:**
- [ ] `middleware.go` uses `subtle.ConstantTimeCompare` for API key check.
- [ ] `runtime_rbac.go` uses `subtle.ConstantTimeCompare` for API key check.
- [ ] No variable-time `!=` string comparison exists for API key validation.

**Priority:** Must

---

### SRS-F-004: RBAC Enforcement on RPC Calls

**Description:** Every authenticated RPC call to a model method must check whether the authenticated user's group has the required permission (perm_read, perm_write, perm_create, perm_unlink) in `sys_model_access`.

**Inputs:**

| Input | Source | Type | Validation rules |
|---|---|---|---|
| Model name | URL path `/{model}/{method}` | string | Must be a registered model |
| Method name | URL path | string | Must be a valid runtime method |
| Auth identity | Bearer / Basic / X-API-Key | struct | Must pass auth check |

**Processing:**
1. Resolve auth identity from request headers.
2. Determine required permission from method name (`read` → `perm_read`, `create` → `perm_create`, etc.).
3. Query `sys_model_access` WHERE `active = true AND model_name = ? AND (group_id = user.group_id OR group_id IS NULL)`.
4. Deny if no matching row is found.
5. Superadmin (`/rpc_meta/*`) and `X-API-Key` sudo callers bypass ACL.

**Outputs:**

| Output | Destination | Type | Description |
|---|---|---|---|
| Allowed | Pass to handler | — | RBAC check passed |
| Denied | Response | 403 | Access denied with model and method name |

**Error conditions:**

| Condition | System behavior | Error response |
|---|---|---|
| No ACL row for model+group | Deny | 403 `Access denied` |
| `sys_model_access.active = false` | Row ignored | 403 |
| Unauthenticated request | Deny | 401 |

**Acceptance criteria:**
- [ ] `sys_model_access` index `idx_sys_model_access_lookup` on `(model_name, group_id, active)` exists.
- [ ] RBAC check runs for every authenticated, non-superadmin RPC call.
- [ ] `active = false` ACL rows are not enforced.

**Priority:** Must

---

### SRS-F-005: Model Registry — Runtime Model Registration

**Description:** The kernel must register Go-backed models (their fields, relations, storage name, and access rules) into the in-memory registry at startup, before any RPC traffic is accepted.

**Inputs:**

| Input | Source | Type | Validation rules |
|---|---|---|---|
| Model definition | Go struct + `RegisterRuntimeBootstrap(...)` call | `ModelDefinition` | Must specify unique logical name |

**Processing:**
1. At startup, call `InitRuntimeModels()` (kernel) and each module's `RegisterRuntimeBootstrap(...)`.
2. Validate uniqueness of logical model names.
3. Sync runtime metadata into `sys.model`, `sys.model.field`, and `sys.model.relation` tables.
4. Run auto-migration for new or altered columns.
5. Fail fast (non-zero exit) if schema migration fails due to DB permission errors.

**Notes:**
- System metadata models (`sys.model`, `sys.model.field`, `sys.model.relation`) do **not** carry an `active` field in their own metadata registrations. The `active` field is a business-model-only convention; system models rely on presence/absence semantics instead.

**Error conditions:**

| Condition | System behavior | Error response |
|---|---|---|
| Duplicate logical name | Panic at startup | Process exits |
| Schema migration fails | Non-zero exit | Error logged; server does not start |
| DB permission denied | Non-zero exit | Error message identifies the table |

**Acceptance criteria:**
- [ ] All models in the active model surface table (see §2) are accessible via RPC after startup.
- [ ] A migration failure prevents the server from accepting traffic.
- [ ] `ResolveStorageName()` is used everywhere; no `WithStorageName(...)` calls exist unless the model needs a legacy table mapping.
- [ ] `sys.model`, `sys.model.field`, and `sys.model.relation` records are not registered with an `active` field.

**Priority:** Must

---

### SRS-F-006: Generic Runtime CRUD Operations

**Description:** The ORM runtime must support `search`, `search_count`, `read`, `search_read`, `create`, `write`, `unlink`, `copy`, `name_search`, `name_get`, `read_group`, and `onchange` for any registered model via the RPC transport.

**Inputs (for `create` / `write`):**

| Input | Source | Type | Validation rules |
|---|---|---|---|
| Model name | URL path | string | Registered model |
| Values map | Request body `params` | `map[string]any` | Keys must match registered field names |
| Record IDs (write/unlink) | URL path `/{id}` or body | int64 / []int64 | Must exist |

**Processing:**
1. Validate keys against registered field metadata; reject unknown keys with 400.
2. Normalize relational field values (many2one `[id, label]` → stored FK id).
3. Exclude non-stored, computed, and x2many-only fields from SQL SELECT.
4. Apply company scoping for models with `company_id` field.
5. Run inside an explicit Bun transaction for `create`, `write`, `unlink`.
6. Enriched virtual/computed fields are set after SQL scan, not during.
7. Normalize update payload to storage-column names before SQL to prevent duplicate column assignment.

**Error conditions:**

| Condition | System behavior | Error response |
|---|---|---|
| Unknown field key | Reject | 400 with field name |
| ID not found | Reject | 404 |
| Transaction conflict | Rollback + retry signal | 500 with retry hint |
| Non-stored field in SELECT | Silently excluded | — (no error) |

**Acceptance criteria:**
- [ ] `create` with an unregistered field key returns 400.
- [ ] `write` with `updatedAt` and `updated_at` in the same payload does not produce `multiple assignments to same column` SQL error.
- [ ] `read` requires explicit IDs; calling with empty IDs returns 400.
- [ ] `search` pushes domain, ordering, paging, and count to SQL (not in-memory).

**Priority:** Must

---

### SRS-F-007: Domain Filter Compilation

**Description:** Runtime domain filters ( `[field, operator, value]` triples) must compile to SQL predicates anchored to the active table alias, not the raw storage name.

**Processing:**
1. Accept domain as a list of `[field, operator, value]` triples or logical operators `AND`, `OR`, `NOT`.
2. Resolve each field name against registered field metadata.
3. Generate SQL using the `AS alias` in scope (e.g., `ui_view_record`), not the raw storage name (`ui_view`).
4. Support operators: `=`, `!=`, `<`, `<=`, `>`, `>=`, `like`, `ilike`, `in`, `not in`, `is null`, `is not null`.

**Acceptance criteria:**
- [ ] Domain on `ui.view` with `model = ?` generates SQL against the correct alias.
- [ ] Unsupported operator returns 400 with the operator name.

**Priority:** Must

---

### SRS-F-008: XML View Arch Resolution (SDUI)

**Description:** The system must resolve a `ui.view` record's XML arch — including inheritance merges — into an SDUI-ready payload returned by `get_view` and `get_views`.

**Inputs:**

| Input | Source | Type | Validation rules |
|---|---|---|---|
| View ID or external ID | RPC call | int64 / string | Must exist in `ui_view` |
| Model name | Request context | string | Optional; used for default view lookup |
| View type | Request context | string | `form`, `list`, `kanban`, `search` |

**Processing:**
1. Load the base `ui.view` record.
2. Resolve inherited arch patches (XPath-based `position` attributes) in declaration order.
3. Widen `arch`, `arch_base`, `arch_db`, `arch_fs`, `arch_prev` columns to PostgreSQL `text` on startup if they are still varchar-like.
4. Return the fully resolved `arch` string as part of the SDUI descriptor.
5. Persist `arch_prev` and update `arch_updated` timestamp on every `write`.
6. Clear `ui.view.custom` overrides on `write`.

**Error conditions:**

| Condition | System behavior | Error response |
|---|---|---|
| View not found | Reject | 404 |
| XPath resolution fails | Return base arch; log warning | 200 with degraded arch |
| Arch column too narrow | Auto-widen on startup | Server log warning if widen fails |

**Acceptance criteria:**
- [ ] `get_combined_arch` accepts legacy positional payloads `[[id]]`.
- [ ] Inherited views are merged before return.
- [ ] `statusbar_visible` is returned as a non-stored compatibility field without causing SQL errors.

**Priority:** Must

---

### SRS-F-009: Menu Resolution and Loading

**Description:** `ui.menu` must support `get_user_roots`, `load_menus_root`, and `load_menus` to return only the menu entries visible to the authenticated user.

**Processing:**
1. Filter menus by user group membership via `ui_menu.group_ids`.
2. Return nested menu tree for `load_menus`.
3. Menu state is URL-driven; the frontend resolves active menu from the current URL path.
4. Detail routes `/web/<model>/<id>` inherit the matched `ui.menu` window action using longest-prefix path matching.

**Acceptance criteria:**
- [ ] User without `base.group_system` cannot see admin menu entries.
- [ ] `/web/<model>/<id>` route inherits the correct window action.

**Priority:** Must

---

### SRS-F-010: Master-Data Seed Import

**Description:** On startup and on `import-data` command, the system must import JSON, CSV, and XML seed files from module `data/`, `security/`, and `views/` directories in stage order: `security → data → views`.

**Processing:**
1. Scan module directories for `security/*.csv`, `data/*.json`, `data/*.csv`, `views/*.xml`.
2. Compare each seed file's expected external IDs against `sys.model.data`; skip files where all records are present and `noupdate = true`.
3. Import files in fixed stage order within each module.
4. Import modules in dependency order (dependencies first).
5. Resolve `ref(...)` expressions in XML views to live record IDs.
6. Normalize stale legacy model aliases (`base.country` → `res.country`) during verification.
7. Treat stale legacy aliases as drift; re-import to repair.
8. Require external IDs on all import records.

**Error conditions:**

| Condition | System behavior | Error response |
|---|---|---|
| External ID missing | Skip record; log error | Warning logged |
| DB permission error | Fail fast | Non-zero exit |
| Unknown model in seed | Log and skip | Warning logged |
| `ref(...)` unresolvable | Log and substitute zero | Warning logged |

**Acceptance criteria:**
- [ ] Admin user is seeded with `login=admin`, bcrypt-hashed password, and `base.group_system` membership.
- [ ] Re-import repairs stale legacy country model alias rows.
- [ ] `noupdate=true` records are not overwritten on re-import.
- [ ] Import order within a module is `security → data → views`.

**Priority:** Must

---

### SRS-F-011: Workflow Execution Engine

**Description:** The system must execute `base.workflow` pipelines triggered by webhook, Kafka topic, or MQTT message. Each step resolves a registered action handler (`action_code`), executes it with the current payload, and writes a structured audit row to `base.workflow.run`.

**Inputs:**

| Input | Source | Type | Validation rules |
|---|---|---|---|
| Webhook POST | HTTP `POST /wh/<topic>` | JSON body | Must match an active `base.workflow.trigger` with `trigger_type = webhook` |
| Kafka message | Kafka topic | JSON | Must match an active `base.workflow.trigger` with `trigger_type = kafka` |
| MQTT message | MQTT topic | JSON | Must match an active `base.workflow.trigger` with `trigger_type = mqtt` |

**Processing:**
1. Resolve the latest active `base.workflow` for the matched trigger.
2. For each action node in declared order, resolve the `action_code` from the `base.workflow.action` record.
3. Invoke the registered handler from the action handler registry (see §3 handler catalog below).
4. On handler error: log, mark retry per `retry_count` and `backoff_seconds`, write `base.workflow.run` row with status `failed`.
5. On success: write `base.workflow.run` row with status `completed`.
6. Thread the request context through every step; respect timeout from flow metadata.
7. Each `base.workflow.run` row stores a `request_id` for correlation across steps.

**Built-in action handler catalog:**

| Handler `action_code` | Description |
|---|---|
| `parse_payload` | Parses the raw inbound payload into a typed map for downstream steps |
| `crud_model` | Executes a generic model CRUD operation (create / search / write / delete) on any registered model |
| `notify` | Publishes a service-bus notification to a channel |
| `flatten_json` | Flattens a nested JSON object into a dot-notation key map |
| `transform_data` | Applies a `{{ field }}` template expression map to transform the current payload |
| `post_output_object` | POSTs the current payload (or a subset) to an outbound delivery target |
| `hmac_action` | Computes an HMAC signature over selected payload fields |
| `aes_action` | Encrypts selected payload fields with AES |
| `aes_decrypt_action` | Decrypts an AES-encrypted field back to plaintext |
| `condition` | Evaluates a boolean expression against the current payload; branches execution |

**Error conditions:**

| Condition | System behavior | Error response |
|---|---|---|
| No active workflow for trigger | Log and discard message | Warning logged |
| Step handler not registered | Mark step failed; continue or abort per flow config | Error logged |
| Timeout exceeded | Cancel step; mark workflow run `timeout` | Audit row written |

**Acceptance criteria:**
- [ ] Every flow execution writes at least one `base.workflow.run` row with a `request_id`.
- [ ] Context cancellation during a step propagates to HTTP and DB calls.
- [ ] Retry count and backoff are respected; a handler is not called more than `retry_count + 1` times per message.
- [ ] `crud_model` handler requires `params.model` to be a non-empty registered model name.
- [ ] Webhook trigger (`trigger_type = webhook`) is registered by `base.workflow.trigger` CRUD, not a static route.

**Priority:** Must

---

### SRS-F-012: IPN Ingest — Bank ETL Routing

**Description:** The IPN pipeline must receive Napas and MB Bank notifications, normalize the payload, and route them to the ERP via outbound callback.

**Inputs:**

| Input | Source | Type | Validation rules |
|---|---|---|---|
| Napas message | Kafka topic | JSON DTO | `SourceAccount`, `CustomerName`, `Amount` must be non-empty |
| MB message | Kafka topic | JSON DTO | Same structural requirement |

**Processing:**
1. Parse raw Kafka message into the partner-specific DTO.
2. Load bank account routing data from `res.partner.bank` by proxy value.
3. **Napas**: Map `SourceAccount → FromAccNo`, `CustomerName → FromAccName` (not reversed).
4. **MB**: If `loadBankAccount` returns a DB error, return the error and halt — do not proceed with empty routing fields.
5. Create an ERP record via JSON-RPC.
6. Dispatch outbound HTTP callback to the registered callback URL.
7. Close the HTTP response body after every callback attempt.
8. Thread the request context through ERP JSON-RPC and callback HTTP calls.

**Error conditions:**

| Condition | System behavior | Error response |
|---|---|---|
| Bank account load fails (DB error) | Halt; Kafka consumer retries | Error logged; no ERP record created |
| Bank account not found (nil, no error) | Proceed with empty routing; log warning | ERP record created with partial data |
| Callback HTTP error | Log; mark delivery for retry | Retry scheduled |
| ERP JSON-RPC fails | Log; mark workflow run failed | Error logged |

**Acceptance criteria:**
- [ ] `FromAccNo` contains the account number, not the customer name.
- [ ] `FromAccName` contains the customer name, not the account number.
- [ ] A DB error in `loadBankAccount` returns early; no ERP record is created.
- [ ] Every HTTP response body is closed after the call, whether it succeeded or failed.

**Priority:** Must

---

### SRS-F-013: Bank Account Uniqueness

**Description:** Bank account writes through runtime, import, or RPC paths must guarantee uniqueness of `proxy_value` without relying on route-level read-then-write logic.

**Processing:**
1. Add a `UNIQUE` constraint on `tbl_partner_bank(proxy_value)`.
2. Keep write paths on the canonical runtime model surface after removing the legacy bank CRUD routes.
3. Use database-backed uniqueness as the source of truth when concurrent writes target the same `proxy_value`.

**Acceptance criteria:**
- [ ] `UNIQUE` constraint exists on `tbl_partner_bank.proxy_value`.
- [ ] Concurrent writes with the same `proxy_value` produce exactly one row.
- [ ] No legacy bank CRUD route handler is required for uniqueness.

**Priority:** Must

---

### SRS-F-014: Outbound HTTP/MQTT Delivery with Retry

**Description:** The outbound delivery service must enqueue, dispatch, and retry HTTP or MQTT delivery attempts for the `base.workflow.outbound` model. Failed deliveries are retried up to `max_attempts` times using an exponential or fixed delay, driven by a background scheduler that re-dispatches `retryable_failed` records whose `next_attempt_at` epoch has elapsed.

**Model:** `base.workflow.outbound` (storage table: `base_outbound_delivery`)

**Processing:**
1. On first delivery attempt, create a `base.workflow.outbound` record with `status = queued`.
2. Transition to `status = sending` before the outbound HTTP/MQTT call.
3. On success, set `status = delivered`.
4. On retryable failure (HTTP 5xx, network error), set `status = retryable_failed`, increment `attempt_count`, compute `next_attempt_at = now() + retryDelay`.
5. A background ticker (interval ≤ retryDelay) queries `status = retryable_failed AND next_attempt_at <= now()` and re-submits each record to the worker pool.
6. After `max_attempts` failed attempts, set `status = permanent_failed` (terminal).
7. Record the raw HTTP/MQTT response in `response_body` on every attempt.
8. The `name` field (unique) stores the request ID for idempotent retry tracking.

**Error conditions:**

| Condition | System behavior | Error response |
|---|---|---|
| HTTP 5xx or network error | Set `retryable_failed`; schedule retry | Error logged |
| `max_attempts` exceeded | Set `permanent_failed` (terminal) | Error logged |
| Worker pool errors channel full | Log; do not drop silently | Error counter incremented |

**Acceptance criteria:**
- [ ] A record with `status = retryable_failed` is retried without manual intervention.
- [ ] After `max_attempts` failed attempts the record status is `permanent_failed` (not `retryable_failed`).
- [ ] `outbounddelivery` drains the worker pool `Errors()` channel in its startup goroutine.
- [ ] `response_body` is populated on every attempt, success or failure.
- [ ] `name` field is unique; duplicate `base.workflow.outbound` records with the same `name` are rejected.

**Priority:** Must

---

### SRS-F-015: Service Bus — Pub/Sub

**Description:** The service bus must accept published events and serve them to polling clients, scoped by database and user ID.

**Inputs:**

| Input | Source | Type | Validation rules |
|---|---|---|---|
| `channel` | Publish call | string | Non-empty; default: `servicebus:global` or `servicebus:user:<uid>` |
| `event_type` | Publish call | string | Non-empty |
| `payload` | Publish call | JSON | Any valid JSON |

**Processing (`poll`):**
1. Authenticate the caller.
2. Accept `last_id` cursor from the client.
3. Return all `base.service.bus` rows WHERE `id > last_id` AND `channel IN (authenticated channels)`.
4. Frontend persists the cursor under a key scoped by `db + uid` in localStorage.

**Error conditions:**

| Condition | System behavior | Error response |
|---|---|---|
| Unauthenticated poll | Reject | 401 |
| `last_id` references a deleted row | Return current rows from earliest available | 200 with `BUS:OUTDATED` event |

**Acceptance criteria:**
- [ ] Two users polling from the same browser receive independent cursors.
- [ ] `BUS:OUTDATED` fires only when the cursor row is genuinely gone, not across login sessions.
- [ ] Channels `servicebus:global` and `servicebus:user:<uid>` are always included for authenticated callers.

**Priority:** Must

---

### SRS-F-016: Module System — Registration and Loading

**Description:** The module system must discover, register, seed, and optionally hot-reload domain addon modules from `app/modules/<name>/` or embedded FS in dependency order.

**Processing:**
1. On startup, scan `modules/` for directories containing `manifest.md`.
2. Parse manifests; resolve dependency order.
3. Sync module metadata into `sys.module.module` (name, version, `installable`, `frontend_widgets`, `static_*_assets`).
4. Import each module's seeds in order: `security → data → views`.
5. Register each module's Go models via `RegisterRuntimeBootstrap(...)`.
6. Frontend late-loads manifest-declared module widgets as ESM bundles from `static/` without rebuilding the main frontend bundle.
7. The scaffold CLI command `module <name>` creates the module skeleton and regenerates `modules/all/modules_gen.go`.

**Error conditions:**

| Condition | System behavior | Error response |
|---|---|---|
| Circular dependency | Reject at startup | Error message listing the cycle |
| Missing dependency module | Reject at startup | Error message naming the missing dep |
| Manifest parse error | Skip module; log error | Warning logged |

**Acceptance criteria:**
- [ ] A new module created with `module <name>` is automatically included in the generated registry after `go generate ./modules/all`.
- [ ] Module seeds import in `security → data → views` order.
- [ ] `sys.module.module` row is synced on every startup.

**Priority:** Should

---

### SRS-F-017: Multi-Database and Multi-Company Scoping

**Description:** The system must support multiple named PostgreSQL databases, including an explicit home/control-plane database via `home_db_name`, optional service-database registration via `config.postgresql.databases`, fallback regex discovery via `db_filter`, and company scoping on authenticated RPC calls.

**Processing:**
1. Resolve `config.postgresql.home_db_name`; if absent, fall back to `config.postgresql.db_name`.
2. Open the home DB first and register it as the default logical database.
3. Register any explicit `config.postgresql.databases` entries as additional named Bun pools.
4. After home DB migration, query the home DB `base_database` registry for service DB rows where `state = 'running'` and connect any not yet registered.
5. If the `base_database` table is absent and `config.postgresql.db_filter` is non-empty, query `pg_database` on the home/admin connection using the regex as a POSIX filter (`datname ~ $1`) and register any matching databases not already registered.
6. Resolve `X-Database-Name` request header against the registry; bind the selected DB into the ORM environment.
7. Models with a `company_id` field are filtered to records where `company_id IS NULL OR company_id IN (allowed_companies)`.
8. `base.company` is restricted to the caller's allowed companies.
9. `allowed_company_ids` and `authorized_company_ids` are set in the ORM environment from the auth context.

**Configuration:**
```yaml
postgresql:
  db_name: "goetl"           # admin / compatibility database
  home_db_name: "home"       # control-plane database
  databases:                  # optional named extras
    secondary_db:
      db_name: "secondary"
  db_filter: '\.thebase\.vn$' # optional POSIX regex fallback for auto-discovery
```

**Error conditions:**

| Condition | System behavior | Error response |
|---|---|---|
| `base_database` query fails because the table is missing | Fall back to `db_filter` if configured | Warning logged |
| `db_filter` query fails (network/perm) | Log error; continue with explicitly configured DBs | Warning logged |
| Regex-discovered DB fails to open | Log error; skip that DB; continue | Warning logged |
| `X-Database-Name` not registered | Reject | 400 |

**Acceptance criteria:**
- [ ] RPC call with `X-Database-Name: secondary_db` executes against the secondary DB.
- [ ] Record in a company-scoped model is invisible to a user whose allowed companies do not include that company.
- [ ] `home_db_name` is used as the default logical database when configured, and the system falls back to `db_name` when it is absent.
- [ ] `db_filter: '\.thebase\.vn$'` registers all `*.thebase.vn` databases discovered in `pg_database`.
- [ ] If the default DB is matched by `db_filter`, it is not double-registered.

**Priority:** Should

---

### SRS-F-025: Multi-Database Control Plane / Data Plane

**Description:** The home DB acts as the control plane for service-database lifecycle management. The `database` module exposes the `base.database` model only for home-DB operators, while service DBs run only the `base`, `workflow`, and `mcp` modules.

**Processing:**
1. The home DB installs `base`, `workflow`, `mcp`, and `database`.
2. Service DBs are provisioned from `base.database.create` and install only `base`, `workflow`, and `mcp`.
3. `base.database` runtime methods must call a home-DB guard first and reject execution from any non-home DB context with HTTP `403`.
4. During service DB setup, `sys.module.module` for module `database` is forced to `enabled = false` and `state = 'not_installed'`.
5. Startup reconnect uses home DB `base_database` rows with `state = 'running'` as the authoritative service registry.

**Acceptance criteria:**
- [ ] Calling `base.database.create` from the home DB provisions a service DB, sets the home row to `state = 'running'`, and records `enabled_modules = ["base","workflow","mcp"]`.
- [ ] Calling `base.database.create` from a service DB returns `403`.
- [ ] A service DB stores `sys.module.module.name = 'database'` with `enabled = false` and `state = 'not_installed'`.
- [ ] On restart, service DBs with `base_database.state = 'running'` reconnect automatically from the home DB registry.

**Priority:** Must

---

### SRS-F-018: Worker Pool Safety

**Description:** The worker pool must be safe against double-close panics and must not silently drop errors when the errors channel is full.

**Processing:**
1. `Shutdown()` and `ShutdownNow()` must use `sync.Once` to guard channel close operations.
2. `outbounddelivery` must drain the `Errors()` channel in a dedicated goroutine started at initialization.
3. When the errors channel is full, log an explicit warning and increment a metric counter; do not silently drop.

**Acceptance criteria:**
- [ ] Calling `Shutdown()` twice does not panic.
- [ ] `outbounddelivery` startup goroutine drains `pool.Errors()`.

**Priority:** Must

---

### SRS-F-019: MQTT Global Client Synchronization

**Description:** The global `MqttClient` variable must be guarded against concurrent read/write data races.

**Processing:**
1. Wrap `MqttClient` access with a `sync.RWMutex`, or return the client from `InitMQTT` and inject it at call sites.
2. `MqttPublish` must acquire read lock; `InitMQTT` must acquire write lock.

**Acceptance criteria:**
- [ ] Running `go test -race ./...` produces no data race reports on the MQTT client path.

**Priority:** Should

---

### SRS-F-020: HTTP Client Context Propagation

**Description:** `CallApi` in `pkg/services/http_client.go` must accept and thread a `context.Context` so parent cancellation propagates to in-flight HTTP calls.

**Processing:**
1. Change the signature to `CallApi(ctx context.Context, action, urlStr string, headers map[string]string, body any) (*http.Response, error)`.
2. Use `http.NewRequestWithContext(ctx, action, urlStr, requestBody)` internally.
3. Update all call sites to pass the available context.

**Acceptance criteria:**
- [ ] Cancelling the parent context during a `CallApi` call causes the HTTP request to abort.
- [ ] All call sites pass a non-nil context.

**Priority:** Should

---

### SRS-F-021: Fresh-Install Auto-Bootstrap

**Description:** When startup creates or connects `postgresql.home_db_name` or `postgresql.db_name` and either target database is brand new or has no user tables, the system must automatically import initial master data for that target database on first boot without requiring `init_import=true`.

**Processing:**
1. During startup bootstrap, determine the target set `postgresql.home_db_name` plus `postgresql.db_name`, deduplicated when both names are the same.
2. Open and register both target databases during startup even when `postgresql.db_name` is not repeated under `postgresql.databases`.
3. During `migrateConfiguredDatabases`, query `information_schema.tables WHERE table_schema = current_schema() AND table_type = 'BASE TABLE'` for each target database before migration.
4. If `count = 0`, mark that target database as a fresh install.
5. Run schema migrations normally (auto-migrate, sys.model.relation sync, config sync).
6. During the startup master-data checkpoint, import `security → data → views` for any fresh target database even when `init_import=false`.
7. Do not require a second restart for the fresh target database to receive its initial master data.

**Error conditions:**

| Condition | System behavior | Error response |
|---|---|---|
| `information_schema` query fails | Log error; abort bootstrap for that DB | Non-zero exit |
| Master-data import fails for a fresh target database | Log error; abort bootstrap for that DB | Non-zero exit |

**Acceptance criteria:**
- [ ] A completely empty `postgresql.home_db_name` bootstraps and seeds base records on first `server` start without passing `--force-master-data`.
- [ ] A completely empty `postgresql.db_name` bootstraps and seeds base records on first `server` start without passing `--force-master-data`.
- [ ] `postgresql.db_name` receives the same fresh-install bootstrap behavior even when it is configured only as `postgresql.db_name` and not duplicated under `postgresql.databases`.
- [ ] A target database with existing tables is not re-seeded by the fresh-install path when `init_import=false`.
- [ ] Fresh-install auto-bootstrap does not require a second restart after migration.

**Priority:** Must

---

### SRS-F-022: Runtime Config — `sys_config_param` Sync

**Description:** The system must persist, sync, and recover operational configuration from `sys_config_param` (canonical) with backfill from the legacy `sys_config` table. The in-memory `Config` struct is always derived from database truth after first sync, not from the YAML file directly.

**Processing:**
1. On first sync (empty table): seed all YAML file settings as flattened `name = canonical_snake_case_key, value = jsonb` rows.
2. On subsequent syncs with `forceFileOverride = false`: load from DB; backfill any file keys absent from DB without overwriting existing DB values.
3. On subsequent syncs with `forceFileOverride = true`: overwrite DB rows from file.
4. Backfill `sys_config` → `sys_config_param` once on startup using `INSERT … ON CONFLICT DO NOTHING`.
5. Deduplicate legacy camelCase aliases (e.g., `initImport`) by deleting them and re-inserting under the canonical `init_import` key in the same transaction.

**Acceptance criteria:**
- [ ] `sys_config_param` table is created if absent; `sys_config` rows are backfilled.
- [ ] `init_import` key is always resolved from `sys_config_param`, regardless of whether it was stored as `init_import` or `initImport`.
- [ ] `forceFileOverride = true` overwrites DB config from YAML.

**Priority:** Must

---

### SRS-F-023: Workflow Trigger Registration

**Description:** `base.workflow.trigger` records are the authoritative source of active trigger registrations. CRUD operations on `base.workflow.trigger` must synchronously update the in-memory trigger registry so the change takes effect without service restart.

**Trigger types:** `webhook`, `kafka`, `mqtt`

**Processing:**
1. `create`: Validate `workflow_id`, `trigger_type`, and `topic`. Normalize per-type config (`kafka_config.consumer_group`, `mqtt_config.qos`). Persist the record. If `is_active = true`, call `RegisterTrigger` hook to register the trigger in-memory.
2. `write`: Read current state before update. Persist. Unregister old trigger ID. Re-read updated record. If `is_active = true`, re-register.
3. `unlink`: Read current state. Delete. Unregister trigger ID from in-memory registry.

**Field validation:**

| Field | Rule |
|---|---|
| `trigger_type` | Must be one of: `webhook`, `kafka`, `mqtt` |
| `topic` | Non-empty string; used as Kafka topic, MQTT pattern, or webhook path segment |
| `workflow_id` | Non-zero FK to an existing `base.workflow` record |
| `config` | Merged with per-type defaults; extra keys silently ignored |

**Acceptance criteria:**
- [ ] Creating a `webhook` trigger immediately registers the `/wh/<topic>` route without restart.
- [ ] Setting `is_active = false` via `write` unregisters the trigger.
- [ ] `trigger_type = http` is rejected with a validation error; only `webhook` is valid.

**Priority:** Must

---

### SRS-F-024: Workflow Node Spec API

**Description:** The system must expose `base.workflow.node.spec` parameter schemas over HTTP so the canvas editor can dynamically render parameter panels for each action node without a frontend rebuild.

**Endpoint:** `GET /wh/node_spec/{action_code}` and `POST /wh/node_spec` (list)

**Processing:**
1. Resolve the `action_code` against `base.workflow.action` records.
2. Return the structured `node_spec` JSON describing each parameter (name, type, label, options, default).
3. The canvas editor reads these specs to render `NodeParamPanel` input controls.

**Acceptance criteria:**
- [ ] `GET /wh/node_spec/crud_model` returns the parameter schema for the `crud_model` action.
- [ ] Unknown `action_code` returns 404.

**Priority:** Should

---

## 4. Non-Functional Requirements

### 4.1 Performance

| Metric | Threshold | Measurement conditions |
|---|---|---|
| RPC response time (p95) | < 200ms | Single-model CRUD on a healthy 10k-row table |
| RPC response time (p99) | < 500ms | Same conditions |
| RBAC check latency | < 5ms | With `idx_sys_model_access_lookup` index present |
| Kafka message end-to-end | < 2s | From message receipt to `base.workflow.run` write |
| Outbound delivery attempt | < 10s | HTTP call timeout per attempt |
| Service bus poll | < 100ms | Under 1000 pending messages per user channel |

### 4.2 Reliability

| Attribute | Target |
|---|---|
| Uptime | 99.5% monthly |
| Error rate (RPC) | < 0.1% (excluding client 4xx) |
| RTO (Recovery Time Objective) | < 5 minutes (process restart) |
| RPO (Recovery Point Objective) | < 1 minute (committed PostgreSQL transactions) |
| Outbound delivery guarantee | At-least-once (up to `max_attempts` attempts) |
| Kafka consumer | At-least-once; idempotent handlers required |

### 4.3 Security

| Requirement | Detail |
|---|---|
| Authentication | Session auth (cookie + API key) and X-API-Key header for service accounts |
| Authorization / RBAC | `sys_model_access` checked per model per method per authenticated user group |
| Data encryption at rest | PostgreSQL-level encryption (responsibility of infrastructure) |
| Data encryption in transit | TLS on all public endpoints (responsibility of infrastructure) |
| Audit logging | `sys.model.log` records all write/unlink operations; `base.workflow.run` records all ETL executions |
| Password storage | bcrypt only; plaintext and SHA-1 forbidden |
| API key comparison | `crypto/subtle.ConstantTimeCompare` required |
| PII handling | Partner and user data must not appear in structured log lines |
| Session token | `api_key` returned in authenticate response must be a stored token, never the raw password |

### 4.4 Scalability

| Dimension | Current | 12-month target |
|---|---|---|
| PostgreSQL data volume | < 10 GB | < 100 GB |
| Concurrent RPC users | < 100 | < 500 |
| Kafka message throughput | < 100 msg/s | < 1000 msg/s |
| Outbound deliveries per day | < 10,000 | < 100,000 |
| Module count | 2 (base, workflow) | < 20 addons |
| Named databases (multi-db) | 1–5 | < 50 (db_filter auto-discovery) |

### 4.5 Accessibility

- Frontend target: WCAG 2.1 Level AA for core navigation, login, and form screens.
- Keyboard navigation must be functional for form read and write operations.

---

## 5. Data Requirements

### 5.1 Key model definitions

**Table: users (res.users / tbl_users via legacy table `users`)**

| Field | Type | Nullable | Default | Validation | Description |
|---|---|---|---|---|---|
| id | bigint | No | autoincrement | PK | User ID |
| login | varchar | No | — | Unique | Username / email |
| password | varchar | No | — | Must be bcrypt `$2*` | Hashed password |
| name | varchar | No | — | Non-empty | Display name |
| authtoken | varchar | Yes | NULL | — | Stored API token |
| userroleid | bigint | Yes | NULL | FK → userroles.id | Primary group |
| active | boolean | No | true | — | Soft delete flag |
| created_at | timestamptz | No | now() | — | Creation timestamp |
| updated_at | timestamptz | No | now() | — | Last update timestamp |

**Table: userroles (res.groups / legacy table)**

| Field | Type | Nullable | Default | Validation | Description |
|---|---|---|---|---|---|
| id | bigint | No | autoincrement | PK | Group ID |
| name | varchar | No | — | Unique | Group logical name |
| full_name | varchar | Yes | NULL | — | Display name |
| category_id | bigint | Yes | NULL | FK → group category | — |

**Table: ui_view**

| Field | Type | Nullable | Default | Validation | Description |
|---|---|---|---|---|---|
| id | bigint | No | autoincrement | PK | View ID |
| name | varchar | No | — | Non-empty | View name |
| model | varchar | Yes | NULL | — | Target model logical name |
| type | varchar | No | — | `form`, `list`, `kanban`, `search` | View type |
| arch | text | Yes | NULL | — | Full resolved XML arch |
| arch_base | text | Yes | NULL | — | Original unmodified arch |
| arch_db | text | Yes | NULL | — | Database-stored arch |
| arch_fs | text | Yes | NULL | — | Filesystem-loaded arch |
| arch_prev | text | Yes | NULL | — | Previous arch before last write |
| arch_updated | timestamptz | Yes | NULL | — | Last arch write timestamp |
| priority | integer | No | 16 | — | Resolution priority |
| active | boolean | No | true | — | Soft delete |
| created_at | timestamptz | No | now() | — | — |
| updated_at | timestamptz | No | now() | — | — |

**Table: sys_model_access**

| Field | Type | Nullable | Default | Validation | Description |
|---|---|---|---|---|---|
| id | bigint | No | autoincrement | PK | — |
| name | varchar | No | — | — | Access rule name |
| model_name | varchar | No | — | FK → sys_model.model | Target model |
| group_id | bigint | Yes | NULL | FK → userroles.id | Null = applies to all groups |
| perm_read | boolean | No | false | — | Read permission |
| perm_write | boolean | No | false | — | Write permission |
| perm_create | boolean | No | false | — | Create permission |
| perm_unlink | boolean | No | false | — | Delete permission |
| active | boolean | No | true | — | Whether this rule is enforced |

**Table: base_etl_flow (base.workflow)**

| Field | Type | Nullable | Default | Validation | Description |
|---|---|---|---|---|---|
| id | bigint | No | autoincrement | PK | — |
| name | varchar | No | — | — | Flow name |
| active | boolean | No | true | — | Enabled flag |
| retry_count | integer | No | 0 | ≥ 0 | Max retries per step |
| backoff_seconds | integer | No | 60 | ≥ 0 | Retry delay |
| timeout_seconds | integer | No | 30 | > 0 | Per-step timeout |
| created_at | timestamptz | No | now() | — | — |

**Table: base_workflow_trigger (base.workflow.trigger)**

| Field | Type | Nullable | Default | Validation | Description |
|---|---|---|---|---|---|
| id | bigint | No | autoincrement | PK | — |
| workflow_id | bigint | No | — | FK → base_etl_flow, CASCADE | Parent workflow |
| trigger_type | varchar | No | — | `webhook`, `kafka`, `mqtt` | Trigger protocol |
| topic | varchar | No | — | Non-empty, indexed | Kafka topic, MQTT pattern, or webhook path segment |
| is_active | boolean | No | true | — | Whether trigger is registered |
| config | jsonb | Yes | NULL | — | Per-type extra config (`consumer_group`, `qos`, etc.) |
| created_at | timestamptz | No | now() | — | — |
| updated_at | timestamptz | No | now() | — | — |

**Table: base_workflow_run**

| Field | Type | Nullable | Default | Validation | Description |
|---|---|---|---|---|---|
| id | bigint | No | autoincrement | PK | — |
| flow_id | bigint | No | — | FK → base_etl_flow | — |
| request_id | varchar | Yes | NULL | — | Correlation ID across steps |
| step | varchar | No | — | — | Step name |
| status | varchar | No | — | `completed`, `failed`, `timeout` | — |
| payload | jsonb | Yes | NULL | — | Input payload snapshot |
| error | text | Yes | NULL | — | Error message if failed |
| started_at | timestamptz | No | now() | — | — |
| finished_at | timestamptz | Yes | NULL | — | — |

**Table: base_outbound_delivery (base.workflow.outbound)**

| Field | Type | Nullable | Default | Validation | Description |
|---|---|---|---|---|---|
| id | bigint | No | autoincrement | PK | — |
| name | varchar | No | — | Unique | Request ID (idempotency key) |
| topic | varchar | Yes | NULL | — | Originating trigger topic |
| delivery_type | varchar | Yes | NULL | — | `http`, `mqtt`, or custom |
| target_ref | varchar | Yes | NULL | — | Target URL or MQTT topic |
| headers | jsonb | Yes | NULL | — | HTTP request headers |
| request_body | jsonb | Yes | NULL | — | Outbound payload |
| response_body | text | Yes | NULL | — | Raw response body from target |
| status | varchar | No | `queued` | `queued`, `sending`, `delivered`, `retryable_failed`, `permanent_failed` | Delivery lifecycle state |
| attempt_count | integer | No | 0 | ≥ 0 | Delivery attempts made |
| max_attempts | integer | No | 3 | ≥ 1 | Max attempts before `permanent_failed` |
| last_error | text | Yes | NULL | — | Last error message |
| next_attempt_at | bigint | Yes | NULL | — | Unix epoch ms when retry is due |
| last_attempt_at | bigint | Yes | NULL | — | Unix epoch ms of last attempt |
| created_at | timestamptz | No | now() | — | — |
| updated_at | timestamptz | No | now() | — | — |

**Table: base_service_bus**

| Field | Type | Nullable | Default | Validation | Description |
|---|---|---|---|---|---|
| id | bigint | No | autoincrement | PK | Monotonic cursor |
| channel | varchar | No | — | Non-empty | Pub/sub channel name |
| event_type | varchar | No | — | Non-empty | Event category |
| payload | jsonb | Yes | NULL | — | Event data |
| created_by | bigint | Yes | NULL | FK → users.id | Publishing user |
| created_at | timestamptz | No | now() | — | — |

**Table: sys_config_param**

| Field | Type | Nullable | Default | Validation | Description |
|---|---|---|---|---|---|
| name | varchar | No | — | Unique, canonical snake_case | Config key path (e.g. `postgresql.db_name`) |
| value | jsonb | No | — | — | Config value as JSON scalar or object |

### 5.2 Data retention and access

| Data type | Retention period | Who can access | Deletion method |
|---|---|---|---|
| `base.workflow.run` | Indefinite (archive after 90 days) | `base.group_system` | Soft archive; hard delete by admin |
| `base.workflow.outbound` | 30 days after terminal status | System service account | Hard delete by scheduled job |
| `base.service.bus` | 7 days | Authenticated users (own channels) | Hard delete by scheduled job |
| User passwords | N/A (stored as hash) | System only | On user delete |
| Audit log (`sys.model.log`) | Indefinite | `base.group_system` | Hard delete by admin |
| `sys_config_param` | Persistent | `base.group_system` | Manual admin operation |

---

## 6. API and Interface Contracts

### 6.1 POST /oauth/token

| Attribute | Value |
|---|---|
| Method | POST |
| Path | `/oauth/token` |
| Auth | None (public) |
| Rate limit | 10 req/min per IP (recommended) |

**Request:**
```json
{
  "params": {
    "db": "goetl",
    "login": "admin",
    "password": "admin",
    "context": {}
  }
}
```

**Response (200):**
```json
{
  "result": {
    "uid": 1,
    "session_id": "abc123",
    "api_key": "<stored-authtoken>",
    "valid": true,
    "db": "goetl"
  }
}
```

**Error responses:**

| Status | Code | When |
|---|---|---|
| 401 | `unauthorized` | Invalid credentials |
| 400 | `validation_error` | Missing `db`, `login`, or `password` |

---

### 6.2 POST /rpc/{model}/{method}

| Attribute | Value |
|---|---|
| Method | POST |
| Path | `/rpc/{model}/{method}` or `/rpc/{model}/{method}/{id}` |
| Auth | Required — Bearer token, Basic auth, or X-API-Key |
| Rate limit | — |

**Request (create):**
```json
{
  "params": {
    "values": {
      "name": "Test Partner",
      "email": "test@example.com"
    }
  }
}
```

**Response (200):**
```json
{
  "result": {
    "id": 42,
    "name": "Test Partner"
  }
}
```

**Error responses:**

| Status | Code | When |
|---|---|---|
| 400 | `validation_error` | Unknown field key; missing required field |
| 401 | `unauthorized` | No valid auth |
| 403 | `forbidden` | RBAC check failed |
| 404 | `not_found` | Record not found |
| 500 | `internal_error` | Unexpected server error |

---

### 6.3 GET /rpc/{model}/search_read

**Request:**
```json
{
  "params": {
    "domain": [["active", "=", true]],
    "fields": ["id", "name", "email"],
    "limit": 20,
    "offset": 0,
    "order": "name asc"
  }
}
```

**Response (200):**
```json
{
  "result": {
    "records": [
      { "id": 1, "name": "Admin", "email": "admin@example.com" }
    ],
    "count": 1
  }
}
```

---

### 6.4 GET /oauth/me

| Attribute | Value |
|---|---|
| Method | GET |
| Path | `/oauth/me` |
| Auth | Optional |

**Response (200 — authenticated):**
```json
{
  "result": {
    "uid": 1,
    "valid": true,
    "db": "goetl"
  }
}
```

**Response (200 — unauthenticated):**
```json
{
  "result": {
    "uid": 0,
    "valid": false
  }
}
```

---

### 6.5 POST /rpc/ui.view/get_view

**Request:**
```json
{
  "params": {
    "view_id": 117,
    "view_type": "form"
  }
}
```

**Response (200):**
```json
{
  "result": {
    "id": 117,
    "model": "res.partner",
    "type": "form",
    "arch": "<form>...</form>",
    "fields": { "name": { "type": "char", "string": "Name" } }
  }
}
```

---

### 6.6 GET /doc and /doc/openapi.json

| Attribute | Value |
|---|---|
| Method | GET |
| Path | `/doc`, `/doc/index.json`, `/doc/{model}.json`, `/doc/openapi.json` |
| Auth | None (public discovery) |

Note: Swagger/Swag-generated docs (`/apidocs/*`) are **not** served. The maintained documentation surface is this internal `/doc` service only.

---

### 6.7 Service Bus — POST /websocket/peek_notifications

| Attribute | Value |
|---|---|
| Method | POST |
| Path | `/websocket/peek_notifications` |
| Auth | Required — Bearer token |

**Request:**
```json
{
  "params": {
    "last": 42
  }
}
```

**Response (200):**
```json
{
  "result": [
    {
      "id": 43,
      "channel": "servicebus:global",
      "event_type": "model_updated",
      "payload": {}
    }
  ]
}
```

---

### 6.8 Workflow Webhook — POST /wh/{topic}

| Attribute | Value |
|---|---|
| Method | POST |
| Path | `/wh/{topic}` |
| Auth | Optional (configured per workflow trigger) |

**Request:** Any JSON body; passed as raw payload to the first workflow step.

**Response (200):**
```json
{
  "result": {
    "request_id": "abc-123",
    "status": "accepted"
  }
}
```

**Error responses:**

| Status | Code | When |
|---|---|---|
| 404 | `not_found` | No active `base.workflow.trigger` matches `topic` with `trigger_type = webhook` |
| 400 | `validation_error` | Malformed JSON body |

---

### 6.9 Workflow Node Spec — GET /wh/node_spec/{action_code}

| Attribute | Value |
|---|---|
| Method | GET |
| Path | `/wh/node_spec/{action_code}` |
| Auth | Required |

**Response (200):**
```json
{
  "result": {
    "action_code": "crud_model",
    "params": [
      { "name": "model", "type": "model_name", "label": "Model", "required": true },
      { "name": "operation", "type": "selection", "label": "Operation", "options": ["create","search","write","delete"] }
    ]
  }
}
```

---

## 7. Constraints

| Constraint | Description |
|---|---|
| Technology stack | Go 1.24, Fiber v2, Bun ORM, PostgreSQL 14+, Vite + React 18/19, Kafka, MQTT |
| Backward compatibility | Legacy table names (`users`, `userroles`, `tbl_country`, `tbl_lang`) must remain; model logical names can differ |
| Regulatory / compliance | Password storage must comply with OWASP guidelines (bcrypt minimum cost 12) |
| Deployment environment | Single binary embedding frontend, modules, and base documents; Linux x86-64 |
| Documentation surface | `/doc` routes only; no `swag`/`/apidocs` re-introduction |
| Module scaffold | `go generate ./modules/all` must succeed before `go build` |
| Frontend constraints | No static import of Node-oriented formatter bundles into Vite client entry path |
| Embedding | `main.go` embeds `modules/`, `documents/base`, and `public/` — all output documents under `documents/base/` are included in the binary |
| Trigger type values | `base.workflow.trigger.trigger_type` must be one of `webhook`, `kafka`, `mqtt`; the legacy value `http` is invalid and must be rejected |
| Config table | `sys_config_param` is the canonical config table; `sys_config` is the legacy table from which rows are backfilled once on startup |
| System model metadata | `sys.model`, `sys.model.field`, `sys.model.relation` do not carry an `active` field in their own metadata registration |
| Outbound delivery model name | The canonical model name is `base.workflow.outbound`; the storage table is `base_outbound_delivery` |
| db_filter | The `db_filter` value is a PostgreSQL POSIX regex; Go-side `regexp` syntax is not used |

---

## 8. Assumptions and Dependencies

### 8.1 Assumptions

- PostgreSQL user has CREATE/ALTER/DROP TABLE privileges on the target database during bootstrap.
- Kafka brokers are available and configured before the service starts (if Kafka is enabled).
- MQTT broker is available and configured when MQTT features are enabled.
- The `base` module is always the first registered module and its seeds are always imported.
- The `workflow` module depends on `base` and is registered after it.
- All money amounts in IPN payloads are treated as integer cents; conversion responsibility lies with the caller.
- Frontend SPA is rebuilt and mirrored into `app/frontend/dist` before `go build`.
- `db_filter` regex is evaluated by PostgreSQL POSIX syntax; the default DB connection must be healthy for `db_filter` auto-discovery to run.

### 8.2 Dependencies

| Dependency | Type | Owner | Status |
|---|---|---|---|
| PostgreSQL 14+ | Infrastructure | Platform team | Active |
| Kafka (topic config) | Infrastructure | Platform team | Active (feature-flagged) |
| MQTT broker | Infrastructure | Platform team | Active (feature-flagged) |
| Napas partner API | External | Napas | Active |
| MB Bank partner API | External | MB Bank | Active |
| ERP JSON-RPC endpoint | External | ERP team | Active |
| `github.com/gofiber/fiber/v2` | Go library | Open source | v2 pinned |
| `github.com/uptrace/bun` | Go library | Open source | Pinned in go.sum |
| `golang.org/x/crypto` | Go library | Open source | Required for bcrypt |

---

## 9. Open Questions

| # | Question | Owner | Due date | Status |
|---|---|---|---|---|
| 1 | Should SHA-1 passwords trigger a successful login with forced migration, or always reject? | Security lead | 2026-05-09 | Open |
| 2 | What is the correct retry behavior for Kafka consumers when an ETL step fails — commit offset (discard) or seek back (retry)? | Platform team | 2026-05-09 | Open |
| 3 | What is the target WCAG compliance level for the admin interface vs the end-user interface? | Product | 2026-05-23 | Open |
| 4 | Should `base.workflow.outbound` records older than 30 days be hard-deleted by a scheduled job or archived to cold storage? | Data team | 2026-05-23 | Open |
| 5 | Is the `CallApi` 10-second timeout sufficient for all partner endpoints, or do individual workflow actions need per-action timeout config? | Bank ETL team | 2026-05-23 | Open |
| 6 | Should the module system support hot-reload of manifests without restart, or is restart acceptable for module changes? | Engineering lead | 2026-05-30 | Open |
| 7 | What is the required audit retention period for `base.workflow.run` under the current compliance scope? | Compliance | 2026-05-30 | Open |
| 8 | Should `db_filter` failures (cannot query `pg_database`) be fatal or continue-with-warning? Currently: continue-with-warning. | Platform team | 2026-05-30 | Open |
| 9 | Should the `condition` workflow action support nested boolean expressions (AND/OR of multiple predicates), or only single expressions? | Engineering lead | 2026-05-30 | Open |
| 10 | Should `base.workflow.outbound` `next_attempt_at` and `last_attempt_at` be stored as Unix epoch milliseconds (current) or as `timestamptz` for readability? | Engineering lead | 2026-05-30 | Open |
