# Runtime conformance suite (SRS §4.6, CONF-01..04)

End-to-end tests for the platform kernel's cross-cutting guarantees, driven
through the production RPC transport (`internal/routes.RegisterRPCRoutes`)
against a real seeded PostgreSQL. Unit tests per package do not exercise the
composed matrix — this suite does.

## Running

```bash
# single entrypoint (also available as `make conformance` from app)
go test -tags conformance ./tests/conformance/... -count=1
```

The suite is excluded from normal builds by the `conformance` build tag.

## Database convention (DSN)

Connection parameters come from the same env vars the existing integration
tests use (`internal/test.EnvPostgresConfig`), with local-dev defaults:

| Variable | Default |
|---|---|
| `RUNTIME_GRAPH_TEST_DB_HOST` | `localhost` |
| `RUNTIME_GRAPH_TEST_DB_PORT` | `5432` |
| `RUNTIME_GRAPH_TEST_DB_USER` | `thebase` |
| `RUNTIME_GRAPH_TEST_DB_PASSWORD` | (local dev password) |
| `RUNTIME_GRAPH_TEST_DB_NAME` | `goetl` |
| `RUNTIME_GRAPH_TEST_DB_SSLMODE` | `disable` |

If the database is unreachable the suite **skips** (so plain `go test ./...`
runs stay green without PostgreSQL). Set `CONF_REQUIRE_DB=1` in CI to turn an
unreachable database into a hard failure instead of a silent skip.

Fixtures use a reserved ID range (`92xxxx`) plus `conf02 `/`conf03 ` name
prefixes and clean up after themselves, so the suite can run repeatedly
against a shared development database.

## Coverage map

| File | Requirement | What it locks down |
|---|---|---|
| `conf01_serialization_test.go` | CONF-01 | Generic CRUD + `search_read` + `toggle_active` + `onchange` per model kind (plain / archive-aware / company-scoped / metadata); SRS-F-004 serialization: many2one `[id, name]` tuples, x2many tuple arrays, empty relation → `false`, boolean `false` stays `false`. |
| `conf02_access_test.go` | CONF-02 | ACL × record rule × company scope × auth mode (basic / bearer / api-key / anonymous), sudo bypass, shared-row (`company_id = NULL`) mutation rule. |
| `conf03_multidb_test.go` | CONF-03 | Header vs cookie DB selection through the production middleware (`cmd.BindRequestDatabase`), unknown-DB 400 (header on RPC) and 403 (cookie page) paths, home-DB-only visibility of `base.database` menus. |

The conformance models (`conf.*`) are registered with the real runtime
executors (`orm.NewHandlerWithModel`), never stubs — a regression in the
generic runtime fails here even when package unit tests still pass.

## Bugs this suite has already caught

- `sys.rule` rows were loaded through raw Bun inference (`sys_rule_records`)
  instead of the registered `sys_rule` table, so record rules silently never
  applied (fixed in `internal/models/sys_rule.go`).
- The pgx search/count WHERE builder joined the compiled record-rule domain
  with `AND` without parenthesizing it, so an OR-domain rebound precedence and
  broke record-rule enforcement (fixed in `internal/orm/runtime_search_pgx.go`).
