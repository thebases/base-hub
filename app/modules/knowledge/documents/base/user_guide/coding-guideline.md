# Base HUB — Backend Coding Guideline

| Field | Value |
| --- | --- |
| Author | Base HUB Team |
| Status | Active |
| Version | 1.0 |
| Date | 2026-05-18 |
| Applies to | `app/` — all Go code in this repository |
| Audience | Backend engineers contributing to Base HUB |

---

## 1. Purpose and scope

This guideline defines how Go code is written, organized, and reviewed inside the Base HUB backend. It exists so that engineers across the team make the same decisions without debating them each time, and so that new contributors can learn the project's conventions quickly.

**In scope:**

- Go source code under `app/` (modules, internal packages, routes, services, ORM)
- Database interaction patterns (Bun ORM, pgx, transactions)
- API/payload contracts and JSON normalization
- Logging, error handling, and context propagation
- Module registration, RBAC, and multi-database scoping
- Workflow module rules (idempotency, retry, resume)

**Out of scope:**

- Frontend code under `app/frontend-admin/`
- Infrastructure configuration (Docker, CI/CD, deploy scripts)
- Third-party service integration specifics (Kafka broker config, MQTT broker config)

---

## 2. Background and rationale

Base HUB is a multi-tenant financial operations platform. Mistakes here carry real cost: duplicate payments, missed transactions, broken audit trails, and stale company-scoped data leaking between tenants. The rules in this guideline exist because these failure modes are real and have been encountered before — not because they are theoretical.

The architecture has two layers:

| Layer | Path | Role |
| --- | --- | --- |
| **Kernel / runtime** | `app/internal/` | Shared infrastructure: ORM, RBAC, runtime, routes, services |
| **Modules** | `app/modules/*/` | Domain logic scoped to one feature area (base, workflow, mcp) |

Modules must not directly import each other. Cross-module communication goes through the kernel layer.

---

## 3. Definitions

| Term | Definition |
| --- | --- |
| `ModelSpec` | Metadata descriptor that drives runtime model registration, field mapping, and SQL generation |
| `ResolveStorageName()` | Canonical method for getting a model's DB table name — never hardcode table names |
| `meta:"..."` | Canonical struct tag for model field metadata; `opt:"..."` is a transitional alias |
| `BunDB` | Bun ORM connection owned by `internal/orm` — the single source of truth for DB access |
| `Environment` | Request-scoped struct carrying `database_name`, `company_id`, `allowed_company_ids`, and RBAC identity |
| `WorkflowRun` | A single execution record for one workflow invocation, keyed by `name` (the request ID) |
| `external_id` | Canonical seed key for master-data upsert; `id` and `forceUpdate` are compatibility aliases |

---

## 4. Guidelines

### 4.1 Project structure

**Rule 4.1.1 — Keep kernel and module code separate**

Do not import one module from another. Cross-module behavior belongs in `internal/` as a shared service or runtime method.

Why: Circular imports break compilation; hidden cross-module dependencies make refactoring dangerous.

```go
// Good — module calls kernel runtime
import "git.thebase.vn/base_hub/internal/modelruntime"

// Bad — module imports another module
import "git.thebase.vn/base_hub/modules/base-workflow"  // from inside modules/base
```

---

**Rule 4.1.2 — Register module HTTP routes through `HTTPRouteRegistrar`**

Modules that expose Fiber routes must implement the `HTTPRouteRegistrar` interface and register through `internal/modules.RegisterHTTPRoutes(app)`.

Why: Routes registered directly on the Fiber app outside this mechanism bypass middleware ordering and are invisible to the router inventory.

---

**Rule 4.1.3 — Keep transport concerns at the route boundary**

Route handlers parse requests, validate inputs, extract auth context, and delegate to services or runtime. They must not contain business logic or direct SQL.

Why: Business logic in route handlers cannot be tested without an HTTP layer and cannot be reused by other callers (Kafka consumers, internal services).

```go
// Good
func handleCreatePartner(c *fiber.Ctx) error {
    env, err := extractEnvironment(c)
    if err != nil {
        return c.Status(fiber.StatusUnauthorized).JSON(...)
    }
    result, err := partnerService.Create(c.UserContext(), env, payload)
    ...
}

// Bad — SQL directly in a route handler
func handleCreatePartner(c *fiber.Ctx) error {
    _, err := db.NewInsert().Model(&partner).Exec(ctx)
    ...
}
```

---

### 4.2 Error handling

**Rule 4.2.1 — Return errors explicitly; do not panic**

Always check and return errors. Panics are only acceptable in `init()` or startup code where the process cannot meaningfully continue.

Why: Panics in request paths crash Fiber workers and leave in-flight requests in an unknown state.

---

**Rule 4.2.2 — Log immediately before returning an error**

When an error is returned to the caller, log it with `internal/services/log` at the point where it is first observed, not where it is eventually handled.

Why: Errors wrapped and re-returned lose call-site context. Logging at the origin gives the correct file/line.

```go
// Good
if err != nil {
    log.Errorf("failed to resolve storage name for model %s: %v", modelName, err)
    return fmt.Errorf("resolve storage name: %v", err)
}

// Bad — silent return, no log
if err != nil {
    return err
}
```

---

**Rule 4.2.3 — Use `%v` in format strings, not `%w`**

Log format strings use `%v`. Use `%w` only when wrapping an error that the caller will `errors.Is` or `errors.As` against.

Why: This is the established pattern in this codebase. Mixing `%v` and `%w` unpredictably suppresses or surfaces root causes at log time.

---

**Rule 4.2.4 — Do not swallow errors in deferred cleanups**

Log or at least capture errors from deferred `rows.Close()`, `tx.Rollback()`, and similar calls.

```go
// Good
defer func() {
    if err := rows.Close(); err != nil {
        log.Errorf("rows.Close: %v", err)
    }
}()

// Bad
defer rows.Close()
```

---

### 4.3 Context propagation

**Rule 4.3.1 — Thread `context.Context` through all I/O calls**

Every function that touches the DB, makes an HTTP call, or waits on I/O must accept and forward a `context.Context` as its first parameter.

Why: Context carries cancellation signals, deadline propagation, and the request-scoped `Environment` (DB name, company, RBAC). Dropping it breaks tenant isolation and prevents graceful shutdown.

---

**Rule 4.3.2 — Extract the ORM environment from context; do not build it inline**

Use the helpers in `internal/orm` to read `Environment` from context. Do not construct an `Environment` inline inside a handler or service.

Why: Inline construction bypasses the middleware layer that sets `database_name` and validates company scope.

---

**Rule 4.3.3 — Never use `context.Background()` inside a request path**

Use `c.UserContext()` (Fiber) or the `context.Context` argument passed to your function. `context.Background()` is only valid in top-level startup code.

---

### 4.4 Database access

**Rule 4.4.1 — Use `internal/orm.BunDB` as the single DB entry point**

Never open a new database connection inside a handler, service, or model. Always obtain the connection through `orm.GetBunDB()` or the environment's DB accessor.

Why: `BunDB` is the single pool. Creating extra connections exhausts pool slots under load and bypasses connection-level telemetry.

---

**Rule 4.4.2 — Use pgx for non-transactional reads; use Bun inside `bun.Tx` when read-your-own-write matters**

- Simple reads with no write dependency: use pgx directly for performance.
- Any read that must see a write made earlier in the same logical operation: use Bun inside the same `bun.Tx`.

Why: pgx reads outside a transaction can miss rows written milliseconds earlier on a different connection.

---

**Rule 4.4.3 — Compile runtime SQL against the active alias, not raw storage names**

Use `ResolveStorageName()` to get the table name at runtime. Do not hardcode table names in queries.

Why: Model storage names are determined by `ModelSpec` at registration time and can differ from the struct name. Hardcoded names break when a model is renamed or aliased.

```go
// Good
tableName := modelSpec.ResolveStorageName()
q := db.NewSelect().TableExpr(tableName)

// Bad
q := db.NewSelect().TableExpr("tbl_users")
```

Exception: Legacy workflow tables (`base_workflow_run`, `base_outbound_delivery`) use explicit names as documented in `backend-context.md`.

---

**Rule 4.4.4 — Normalize update payloads to storage column names before SQL**

Call `normalizePayload(...)` before passing a map to `RuntimeWrite` or any raw `UPDATE`. Never pass field display names directly to SQL.

Why: Display names differ from column names. The ORM's normalization layer is the single canonical mapping.

---

**Rule 4.4.5 — Retry-claim DB work must use the request-bound DB or transaction**

Inside workflow retry and resume paths, never call `orm.GetBunDB()` directly. Use the environment DB or the transaction passed by the caller.

Why: Using the global pool inside a retry-claim path breaks isolation and can claim a run on one DB while writing its result to another.

---

### 4.5 Model and struct metadata

**Rule 4.5.1 — Use `meta:"..."` tags; avoid `opt:"..."` for new code**

Use the canonical `meta:"..."` struct tag for all new model fields. `opt:"..."` is a transitional alias retained for compatibility and must not appear in new code.

---

**Rule 4.5.2 — Get `ModelSpec` identity from `ResolveStorageName()`**

Do not pass an explicit `.WithStorageName(...)` to `ModelSpec` unless you have a documented reason. Let the registry derive it.

Why: Explicit overrides drift out of sync when the registry changes. The derived name is always authoritative.

---

**Rule 4.5.3 — Exclude non-stored, computed, and x2many-only fields from projections**

When building a `SELECT` column list for a model, omit fields marked as computed, non-stored, or x2many-only. Include only fields that map directly to DB columns.

Why: Including non-stored fields in a `SELECT` produces SQL errors or incorrect NULL fills that corrupt the response.

---

**Rule 4.5.4 — Use `internal/orm.BaseModel` for archive semantics; use `MetadataBaseModel` for metadata models**

Any model that exposes archive (active/inactive) must embed `orm.BaseModel`. Metadata catalog models (`sys.model`, `sys.model.field`, etc.) that must not be archived embed `orm.MetadataBaseModel` instead.

---

### 4.6 API and payload contracts

**Rule 4.6.1 — Pass all JSON responses through `internal/base.JSONMarshal`**

Do not use `json.Marshal` or `c.JSON()` with raw structs in response paths. Use `base.JSONMarshal`, which rewrites serialized `null` to `false` per the ORM contract.

Why: Frontend clients expect `false` for empty/null fields. A raw `null` breaks field type detection in the generic renderer.

---

**Rule 4.6.2 — Keep `RuntimeCreate` and `RuntimeWrite` normalization in the ORM, not in route helpers**

Payload normalization (unknown field dropping, false value handling, field name canonicalization) lives in `orm.RuntimeCreate` and `orm.RuntimeWrite`. Route helpers must not duplicate this logic.

Why: Duplicate normalization creates divergence. The ORM path is the contract; routes just deliver payloads.

---

**Rule 4.6.3 — Apply false-normalization rules consistently**

When reading a payload before writing to the ORM:

| Field type | `false` means | `null` means |
| --- | --- | --- |
| Relation (many2one) | `null` (clear the relation) | unchanged |
| Boolean | omit (do not update) | unchanged |
| Other scalar | `null` (set to null) | unchanged |

---

**Rule 4.6.4 — Return `400` for unrecognized DB names in RPC and workflow requests**

An explicit non-empty `X-Database-Name` header or workflow DB name that does not match a registered logical DB must return HTTP `400`. Never silently fall back to the default DB.

Why: Silent fallback creates a class of bugs where requests execute on the wrong tenant DB with no error signal.

---

### 4.7 RBAC and multi-company scoping

**Rule 4.7.1 — Pin `company_id` from the environment on create, not from the payload**

When creating a record for a model that exposes `company_id`, take the company from the request `Environment.CompanyID()`, not from the caller-supplied payload.

Why: Callers can supply a forged `company_id`. The environment value is derived from the authenticated session.

```go
// Good
payload["company_id"] = env.CompanyID()

// Bad
// company_id comes straight from the request body
```

---

**Rule 4.7.2 — Shared rows use `company_id = NULL`; only `company.main` (ID 1) may write them**

A row with `company_id = NULL` is readable by all companies but may only be created, updated, or deleted by the active `company.main` identity.

Why: This is the multi-tenancy boundary. Violating it allows one tenant to corrupt shared reference data.

---

**Rule 4.7.3 — A user belongs to exactly one group; do not introduce `group_ids`**

`res.groups` membership is direct via `group_id` plus implied groups from `tbl_group_implied_rel`. There is no `group_ids` JSONB array. Do not add one.

---

**Rule 4.7.4 — Bind `X-Database-Name` at middleware, not in each handler**

Database selection is done once in the Fiber middleware layer and stored in `c.UserContext()`. Route handlers read the DB from context. Never re-parse `X-Database-Name` inside a handler.

---

### 4.8 Workflow module

**Rule 4.8.1 — Require idempotency for all externally triggered workflow runs**

Every workflow run has a `name` field that carries the unique request ID. Use this as the idempotency key. Duplicate deliveries of the same `name` must not create duplicate runs.

Why: External systems (Kafka, MQTT brokers, HTTP gateways) deliver at-least-once. Processing a payment twice creates a real financial error.

---

**Rule 4.8.2 — Use `ResumeDueRuns` and `ResumeRun` for retry and resume; never call `ExecuteTopic` for this**

Retry and resume logic is centralized in `modules/base-workflow/service`. Call `ResumeDueRuns` and `ResumeRun`. Do not call `ExecuteTopic` for retry paths.

Why: `ExecuteTopic` creates a new run. Retrying via `ExecuteTopic` produces duplicate runs with different IDs, breaking the run history.

---

**Rule 4.8.3 — Create one request ID at `/wh/*` route entry and reuse it through retries**

The workflow request ID is created once when the inbound HTTP, Kafka, or MQTT message is first received. All retries and resume calls for that delivery use the same ID.

---

**Rule 4.8.4 — Persist `trigger_type` and `input_payload` on run creation**

Store the originating trigger source (`webhook`, `kafka`, `mqtt`) in `base.workflow.run.trigger_type` and store the raw inbound payload in `input_payload` before any step mutation.

Why: The original payload and source are required for auditability and for manual re-processing when a run fails.

---

**Rule 4.8.5 — A skipped step must record `status="skipped"` without mutating the payload**

When `extra_params.condition` evaluates to false, record `step_runs[].status="skipped"` and pass the payload unchanged to the next step.

---

**Rule 4.8.6 — `base.workflow.run.name` carries the unique constraint; use it as the canonical run ID**

Do not use `workflow_request_id` or `request_id` as the canonical field. Write them into `name`. The unique constraint is on `name`.

---

### 4.9 Logging

**Rule 4.9.1 — Use `internal/services/log` for all backend output**

Do not use `fmt.Print*`, `log.Print*` (stdlib), or any other output path. Use `log.Infof`, `log.Errorf`, `log.Warnf` from `internal/services/log`.

Why: The project logger controls format, destination, and structured fields. Bypassing it makes log parsing and alerting unreliable.

---

**Rule 4.9.2 — Do not log and re-return the same error up the stack**

Log the error once at the observation point. Callers that receive a returned error must not log it again unless they are adding new context.

Why: Double-logging the same error clutters logs and makes root cause tracing harder.

---

### 4.10 Seed data and master data

**Rule 4.10.1 — Use `external_id` as the canonical seed key**

Master data seed files must use `external_id` as the upsert key. `id` and `forceUpdate` are compatibility aliases and must not appear in new seed files.

---

**Rule 4.10.2 — Create `company.main` explicitly in seed files; do not rely on auto-creation**

Initial seed data must create `company.main` explicitly and set `res.users.company_id` and `company_ids` on the initial user record.

Why: The bootstrap process does not create `company.main` implicitly. Missing it leaves the tenant in a broken state with no active company.

---

**Rule 4.10.3 — Config keys are `snake_case`; camelCase aliases are normalized on read only**

`sys.config_param` keys and `config.yml` keys must use `snake_case`. CamelCase aliases exist for backward compatibility and are normalized on read; they are not canonical and must not appear in new seed files or config entries.

---

## 5. Anti-patterns

| Anti-pattern | Why it is wrong | Correct approach |
| --- | --- | --- |
| Hardcoding table names in SQL queries | Breaks when `ModelSpec` changes the storage alias | Use `ResolveStorageName()` |
| Opening a new DB connection in a handler | Exhausts the connection pool and bypasses telemetry | Use `orm.GetBunDB()` or the environment DB |
| Calling `orm.GetBunDB()` inside a retry-claim path | Executes on the wrong DB when multi-DB is active | Use the request-bound environment DB |
| Importing one module from another | Creates compile cycles and hidden coupling | Route cross-module behavior through `internal/` |
| Building `Environment` inline in a handler | Bypasses tenant isolation middleware | Extract from `c.UserContext()` |
| Using `context.Background()` in request paths | Drops cancellation and auth scope | Use `c.UserContext()` or the passed `ctx` |
| Using `%w` in log format strings | Inconsistent with codebase convention | Use `%v` for logs; `%w` only for `errors.Is` chains |
| Calling `ExecuteTopic` for retries | Creates duplicate runs with new IDs | Use `ResumeDueRuns` / `ResumeRun` |
| Setting `group_ids` as a JSONB array | The schema uses direct `group_id`; JSONB doesn't exist | Use `group_id` (direct) + implied group relations |
| Using `fmt.Print*` or stdlib `log.Print*` | Bypasses the project logger | Use `internal/services/log` |

---

## 6. Examples

### Good example — route handler with proper context and delegation

```go
// routes/partner_route.go

func handleCreatePartner(c *fiber.Ctx) error {
    env, err := orm.EnvironmentFromContext(c.UserContext())
    if err != nil {
        log.Errorf("handleCreatePartner: missing environment: %v", err)
        return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "unauthorized"})
    }

    var payload map[string]any
    if err := c.BodyParser(&payload); err != nil {
        log.Errorf("handleCreatePartner: body parse: %v", err)
        return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid body"})
    }

    result, err := modelruntime.RuntimeCreate(c.UserContext(), env, "res.partner", payload)
    if err != nil {
        log.Errorf("handleCreatePartner: RuntimeCreate: %v", err)
        return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
    }

    return c.JSON(base.JSONMarshal(result))
}
```

### Bad example — SQL in route, hardcoded table, no context

```go
// Bad: SQL directly in route, hardcoded table, no context, stdlib log
func handleCreatePartner(c *fiber.Ctx) error {
    var partner ResPartner
    c.BodyParser(&partner)
    _, err := orm.GetBunDB().NewInsert().TableExpr("tbl_partner").Model(&partner).Exec(context.Background())
    if err != nil {
        log.Println("error:", err)  // stdlib log
        return c.Status(500).SendString(err.Error())
    }
    return c.JSON(partner)
}
```

---

## 7. Enforcement and tooling

| Enforcement method | Tool / Config | Mandatory? |
| --- | --- | --- |
| Code review against this guideline | PR checklist — cite rule number when flagging | Yes |
| Go vet + staticcheck | Run in CI before merge | Yes |
| `go build ./...` must pass | CI gate | Yes |
| Logging convention | Convention only (no linter rule yet) | Recommended |
| Table name hardcoding | Convention only — grep for string literals in query calls | Recommended |

---

## 8. Exceptions and escalation

When following a rule in this guideline would produce a demonstrably worse outcome:

1. Document the exception in the PR description with the rule number (e.g., "Exception to Rule 4.4.3: ...").
2. Get sign-off from the backend tech lead or a senior engineer.
3. Record the rationale in the PR description or an ADR file under `.agents/memory/planning/`.

Do not merge an exception silently — undocumented exceptions become the new (wrong) convention.

---

## 9. Changelog

| Version | Date | Author | Changes |
| --- | --- | --- | --- |
| 1.0 | 2026-05-18 | Base HUB Team | Initial version covering structure, DB, context, API, RBAC, workflow, logging, and seed data |
