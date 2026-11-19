# Base MCP: user and administrator guide

## 1. Purpose and audience

Base MCP exposes a Base HUB instance to AI agents through the Model Context Protocol (MCP). It provides one authenticated endpoint, `/mcp`, where an agent can discover tools and call them to read and change data, always with the permissions of the user who authenticated.

| Audience | What they do with it |
| --- | --- |
| End users | Connect an AI assistant (Claude, ChatGPT/OpenAI Responses, Codex or any MCP client) to their Base HUB account. |
| Administrators | Install the module, create OAuth clients or users for agents, control what agents may do through groups and ACL. |
| Developers | Add tool groups from their own modules and integrate custom clients. |

## 2. Prerequisites and installation

| Item | Detail |
| --- | --- |
| Depends on | `base` |
| Category | System |
| Install | Installable but not automatic: install `base-mcp` from the Apps/Modules screen. |
| Credential | An OAuth 2.0 bearer access token (for a user login or a `sys.oauth_client` service client). |
| Protocol | Streamable HTTP, protocol version `2025-06-18`, single JSON responses. |

## 3. Key concepts

| Concept | Plain-words meaning |
| --- | --- |
| Tool | One action an agent can call, for example `base.record.search_read`. |
| Tool group | Tools sharing a prefix: `base.*`, `website.*`, `workflow.*`, `knowledge.*`, `auth.*`, `automation.*`, `bus.*`, `calendar.*`, `credential.*` (each is supplied by its own module). |
| Tenant | The database the caller works in. Tool groups of a module appear only if that module is installed there. |
| Session | Optional `Mcp-Session-Id` issued by `initialize`, valid 30 minutes of inactivity. It does not grant rights; permissions are re-checked on every call. |
| Idempotency key | A caller-chosen `idempotency_key` that makes a create call safe to retry (kept 24 hours). |
| Bridge | A small Node.js script for clients that only launch local stdio servers. |

## 4. Where to find it

The module has no menus. It serves these HTTP routes.

| Route | Method | Auth | Purpose |
| --- | --- | --- | --- |
| `/mcp` | POST | Bearer | JSON-RPC: `initialize`, `tools/list`, `tools/call`, notifications. |
| `/mcp` | GET | Bearer | Always 405 (no server-initiated stream). |
| `/mcp` | DELETE | Bearer | Ends a session (needs `Mcp-Session-Id`). |
| `/mcp/tools` | GET | Bearer | Lists the tools available to the caller. |
| `/mcp/health` | GET | None | Readiness probe. |
| `/mcp/bridge` | GET | None | Downloads `base-hub-mcp-bridge.js`. |

## 5. Step-by-step tasks

### Connect an MCP client by remote URL
1. In the client, add a remote MCP server with URL `https://<your-host>/mcp`.
2. Supply the header `Authorization: Bearer <access token>`. For multi-database installations also send the database name header (`X-Database-Name`).
3. The client runs `initialize`, then `tools/list`. You should see the `base.*` tools plus those of installed modules.

### Connect through the stdio bridge
1. Download `/mcp/bridge` and save it. Node.js 18 or newer is required.
2. In the client configure command `node` with the script path.
3. Set environment variables `BASE_HUB_URL`, `BASE_HUB_MCP_KEY` (the bearer token) and optionally `BASE_HUB_DB_NAME`.

### Safely create records from an agent
1. Include `arguments.idempotency_key` in every create-type call.
2. For `base.record.create` and `write`, pass the field values in `arguments.values`.
3. Use `base.batch.execute` to run several steps in one transaction.

### Verify the gateway
1. Open `/mcp/health`; expect status `healthy`.
2. Call `/mcp/tools` with a token to confirm the tool list.

## 6. Administration and configuration

| Topic | Detail |
| --- | --- |
| Access control | Agents have exactly the same groups, ACL and record rules as the authenticated user. There are no separate MCP scopes. To limit an agent, give it a dedicated user or OAuth client with a restricted role. |
| API keys | `X-API-Key` is not accepted on `/mcp`. Use OAuth 2.0 bearer tokens only. |
| Rotating access | Revoke or recreate the user or `sys.oauth_client` used by the agent. |
| Caching | The tool list is cached per database for 30 seconds and refreshed on module install, uninstall or reinit. |
| Audit | Every tool call is logged (user, database, tool, model, method, outcome, latency), never the arguments or token. |
| Seeds | None. |

## 7. API and automation touchpoints

| Tool group | Typical use |
| --- | --- |
| `base.model.*`, `base.record.*` | Discover models, search, read, create, write, delete, call public methods (`base.record.call_method`). |
| `base.model.create_custom` and related | Create, extend, publish custom models. |
| `base.batch.execute` | Several steps in one transaction (cannot nest). Requires `idempotency_key`; a retry with the same key replays the whole result. |
| `website.*` | Sites, pages, SEO, publish. |
| `workflow.*` | Workflow CRUD, export and import. |
| `knowledge.*` | Markdown knowledge notes (user namespace writable). |
| `auth.*`, `automation.*`, `bus.*`, `calendar.*`, `credential.*` | Per-module record access and pinned tools. |

Rules worth knowing: `search_read` returns 20 rows by default and at most 200; large text or JSON fields over 4096 bytes are replaced by a `<field>_bytes` size; `arguments` may be an object or a JSON string.

## 8. Troubleshooting and FAQ

| Symptom | Cause and fix |
| --- | --- |
| 401 "authorization is required" | No `Authorization` header. |
| 401 "must use the Bearer scheme" or "invalid bearer token" | Wrong scheme, expired or wrong-database token. Obtain a new token. |
| 400 on database | Database name missing or unknown in a multi-database setup. |
| Tool group missing from the list | The owning module is not installed in that database. |
| `model_not_found` or `tool_not_found` | Name is wrong or disabled; the reply may include a "see also" hint. |
| Unsupported `MCP-Protocol-Version` | Client must use `2025-06-18` or omit the header. |
| Duplicate create after retry | Add `idempotency_key`; without it retries create duplicates. |
| 405 on GET `/mcp` | Expected. Use POST. |

## 9. Limits and notes

- Only single JSON responses; no server-pushed streaming. DeepSeek and Kimi need an MCP-capable agent host in front.
- No OAuth auto-discovery (RFC 8414) or dynamic client registration: clients must be given a token manually.
- `search_read` has no cursor pagination.
- Custom models are live only in the process that published them until a restart; check this in multi-replica setups.
- Secret-generating actions (such as client secrets) are available only through `base.record.call_method`; confirm with the user first.
