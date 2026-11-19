# Base HUB API guide

**Who this is for:** a developer who has never used this API. **What you get:** after 10 minutes you can log in,
list records, create one, change it and delete it, and you know what to do when a call fails.

The **Reference** tab lists every model and method. This guide explains the rules they all share, so read it once first.

## 1. General

Everything in Base HUB is a **model** (a kind of record, like `tbl.partner` = a customer or contact) and every model has
**methods** (things you can do to it: `search_read`, `create`, `write`, `unlink`).

To do anything, you send one HTTP `POST`:

```
POST {BASE_URL}/rpc/{model}/{method}
```

That is the whole API. The Reference tab tells you which models and methods exist and what to put in the body.

Three rules cover 90% of the work:

1. **Log in first** and send the token on every call.
2. **Every answer has the same shape:** `{"id", "result"}` when it worked, `{"id", "error"}` when it did not.
3. **Decide by `error.code`, never by `error.message`.** The code is a stable number; the message is only for humans.

## 2. Make your first call

You need `BASE_URL` (your server address) and a user account. Examples use `curl`; any HTTP client works.

### Step 1: log in and get a token

```bash
curl -s -X POST "$BASE_URL/oauth/token" \
  -H "Content-Type: application/json" \
  -d '{"grant_type":"password","username":"operator01","password":"<password>"}'
```

```json
{"access_token": "eyJ...", "refresh_token": "5f9d...", "expires_in": 900, "token_type": "Bearer"}
```

Save `access_token`: `TOKEN=eyJ...`. It works for `expires_in` seconds (900 = 15 minutes). Keep `refresh_token` too (section 5).

### Step 2: list records

```bash
curl -s -X POST "$BASE_URL/rpc/tbl.partner/search_read" \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"id": "my-first-call", "kwargs": {"fields": ["id", "name"], "limit": 5}}'
```

### Step 3: read the answer

```json
{"id": "my-first-call", "result": [{"id": 1, "name": "Acme"}]}
```

`id` is just your own label echoed back, so you can match answers to requests. The data is in `result`.

If something is wrong you get `error` instead of `result`:

```json
{"id": "my-first-call", "error": {"code": -32001, "message": "invalid or expired token"}}
```

`-32001` means the token is missing or expired: log in again (Step 1).

## 3. Create, read, update, delete (a full example)

Same pattern every time: `POST /rpc/tbl.partner/<method>`, token in the header, JSON body. The two things that change are
the **method name** and whether the **record id goes in the path**.

| I want to... | Method | URL | Body |
| --- | --- | --- | --- |
| List / search | `search_read` | `/rpc/tbl.partner/search_read` | `{"kwargs": {"filter": {"active": true}, "fields": ["id","name"], "limit": 20}}` |
| Create | `create` | `/rpc/tbl.partner/create` | `{"args": [{"name": "Acme"}]}` |
| Read one by id | `read` | `/rpc/tbl.partner/read/8` | `{"kwargs": {"fields": ["id","name"]}}` |
| Update | `write` | `/rpc/tbl.partner/write/8` | `{"args": [{"name": "Acme Ltd"}]}` |
| Delete | `unlink` | `/rpc/tbl.partner/unlink/8` | `{}` |

How to read the table:

* `args` is a list of values in order; `kwargs` is a set of named options. The Reference tab shows each method's signature,
  and the **Try it** panel gives you a body that already works. Copy it.
* **Id in the path:** `read`, `write` and `unlink` act on existing records, so the id goes in the URL (`/write/8`).
  Several ids can be comma separated (`/unlink/8,9`). Methods that create or search have no id in the path.
* After `create` you get the new record back, including its `id`. Use that `id` for the next calls.

```bash
# create
curl -s -X POST "$BASE_URL/rpc/tbl.partner/create" \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"args": [{"name": "Acme"}]}'
# -> {"id": "...", "result": {"id": 8, "name": "Acme"}}

# update record 8
curl -s -X POST "$BASE_URL/rpc/tbl.partner/write/8" \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"args": [{"name": "Acme Ltd"}]}'
# -> {"id": "...", "result": true}
```

## 4. Habits that prevent bugs

Treat this as a checklist before you ship.

1. **Always check `error` first.** HTTP status alone is not enough. Parse the body and look for `error`.
2. **Branch on `error.code`.** Never match on `message` text; it can change.
3. **Send `X-Idempotency-Key` on every call that creates something or moves money.** If the network drops and you
   retry, the server returns the first answer instead of doing it twice (kept for 30 minutes). Use a unique value per
   business action, for example `order-42-create`.
   ```bash
   -H "X-Idempotency-Key: order-42-create"
   ```
4. **Dates and times are integers:** UTC Unix seconds, for example `1791676800`. An unset time is `false`.
5. **Page your lists.** Pass `limit` and `offset`. REST list routes default to `limit=50` and cap at 500.
6. **Do not log tokens or passwords.**
7. **An empty list can mean "no permission".** A denied read (search, read, count) returns `200` with `[]`, `0`, `false`,
   `{}` or `null`, not an error. If a list is unexpectedly empty, check the user's permissions before blaming your code.

## 5. Staying logged in

* Access tokens last **15 minutes**. Send them as `Authorization: Bearer <access_token>`.
* When a call returns `-32001`, get a new token with the refresh token instead of asking for the password again:

```bash
curl -s -X POST "$BASE_URL/oauth/token" \
  -H "Content-Type: application/json" \
  -d '{"grant_type":"refresh_token","refresh_token":"<refresh_token>"}'
```

* Refresh tokens last **30 days** and **change on every use**. Always store the new `refresh_token` from the answer;
  the old one stops working.
* Too many wrong passwords return HTTP `429` with a `Retry-After` header (seconds to wait).
* Server-to-server callers may send `X-API-Key: <key>` instead (only if the server enables `app.supportApikey`).
* One server can hold several company databases. The database comes from the token (its `db` claim) or from the
  `X-Database-Name` header. A token only works on the database it was issued for.
* Every call is also checked against the user's permissions (ACL) and record rules.

## 6. When a call fails

Find your symptom first, then use the code table below.

| What you see | Likely cause | Fix |
| --- | --- | --- |
| `error.code` `-32001` / HTTP 401 | Token missing, wrong or expired | Refresh the token or log in again |
| `error.code` `-32003` / HTTP 403 | This user is not allowed | Use an account with access; retrying will not help |
| `error.code` `-32601` / 404 | Typo in model, method or URL | Compare with the Reference tab |
| `error.code` `-32602` / 400 | Missing or wrong argument | Read `error.message`; copy the body from **Try it** |
| `error.code` `-32004` / 404 | The record id does not exist | Check the id or serial |
| `error.code` `-32009` / 409 | The record is in the wrong state, or changed under you | Reload the record, then decide |
| `error.code` `-32603` / 500 | Server bug | Retry later; if it persists, report the response `id` |
| `result` is `[]` but data exists | Permission or filter issue | Check the user's access and your `filter` |

Error answers always look like this, for `/rpc` and REST routes alike (even unknown routes and wrong HTTP verbs):

```json
{"id": "my-request-1", "error": {"code": -32009, "message": "device is not active", "data": {"reason": "device_inactive"}}}
```

* The HTTP status follows the code, so proxies and monitoring keep working.
* `error.data` is optional. A module-specific text code without a registry number is kept in `data.reason`.

| code | HTTP | Meaning | What to do |
| --- | --- | --- | --- |
| -32700 | 400 | Body is not valid JSON | Fix the request body |
| -32600 | 400 | Invalid request envelope | Fix the request shape |
| -32601 | 404 | Unknown model, method or route | Check spelling and the Reference tab |
| -32602 | 400 | Validation failed (missing or invalid argument, bad path id) | Fix the arguments; read `message` |
| -32603 | 500 | Unexpected server error (message is always `an unexpected error occurred`) | Retry later; report the response `id` |
| -32000 | 400 | Generic method error (default for a plain error) | Read `message` |
| -32001 | 401 | Missing or invalid credentials | Refresh the token; if that fails, log in again |
| -32003 | 403 | Forbidden (ACL, or superadmin only) | Do not retry; use an account with access |
| -32004 | 404 | Record not found | Check the id or serial |
| -32009 | 409 | Conflict or invalid state change | Reload the record, then decide |
| -32010 | 409 | Idempotency conflict (reserved) | Not emitted yet |
| -32022 | 422 | Business rule violated (reserved) | Not emitted yet |
| -32029 | 429 | Rate limited (reserved; login throttling already sends `429` + `Retry-After`) | Wait `Retry-After` seconds |
| -32050 | 503 | Unavailable (also 502/504), for example broker or database down | Retry with backoff |

Numbers are permanent: a code is never reused or renumbered. Handle errors by code:

```js
const res = await fetch(`${host}/rpc/edc.device/get_by_serial`, { method: "POST", headers, body });
const body = await res.json();
if (body.error) {
  if (body.error.code === -32004) showNotFound();
  else if (body.error.code === -32001) await refreshToken();
  else throw new Error(`${body.error.code}: ${body.error.message} (request ${body.id})`);
}
```

## 7. Other entry points

Most work uses `POST /rpc/...` and returns the `{"id","result"}` / `{"id","error"}` envelope. Some surfaces are different:

| Surface | Route | Use it for | Response format |
| --- | --- | --- | --- |
| RPC | `POST /rpc/{model}/{method}` and `/rpc/{model}/{method}/{id}` | Every registered model method | Envelope above |
| REST alias | `GET/POST/PUT/DELETE /base/...` | Resource-style paths some modules offer (EDC admin, users). Deletes return `{"deleted": true, "id": N}` | Envelope above |
| DB and metadata | `/rpc/db/{method}`, `/rpc_meta` | Database list/init and metadata | `{"id","result"}` or `{"id","error":{"code","message"}}` with their historical HTTP statuses |
| OAuth | `/oauth/token`, `/oauth/authorize`, `/oauth/revoke`, `/oauth/me` (also under `/auth/*`) | Get and revoke tokens | Standard OAuth (RFC 6749): token object, or `{"error": "invalid_grant", "error_description": "..."}` |
| MCP | `POST /mcp` | AI tool access | JSON-RPC 2.0 with `result.content`; tool errors read `"<code>: <message>"` |
| API docs | `GET /doc/index.json`, `/doc/{model}.json`, `/doc/openapi.json` | What this guide is built from | JSON documents |
| EDC device callbacks | `POST /base/transaction/{init,result,settle}`, `GET /base/devices/config/:serial`, `POST /base/device/log/upload`, `POST /device/provision` | Calls made by payment terminals | Fixed bodies terminals already parse; each page shows its own format |

* Anything marked `Public` in the Reference tab can be called through RPC.
* MQTT and Lua outcomes use `{"ok": true}` or `{"ok": false, "reason": "..."}`.

## 8. Glossary

| Term | Plain meaning |
| --- | --- |
| Model | A kind of record, for example `tbl.partner` or `edc.device`. |
| Method | An action on a model: `search_read`, `create`, `write`, `unlink`, or a custom one. |
| RPC | "Remote procedure call": you call a method by name with `POST /rpc/{model}/{method}`. |
| REST alias | A friendlier path such as `GET /base/devices` that runs one RPC method for you. |
| Envelope | The JSON wrapper around every answer: `{"id", "result"}` or `{"id", "error"}`. |
| Bearer token | A temporary password string from `/oauth/token`, sent as `Authorization: Bearer <token>`. |
| Refresh token | A longer-lived string that gets a new access token without the password. |
| ACL | "Access control list": the permission check on what a user may read or change. |
| Idempotency key | A unique string in `X-Idempotency-Key`; a retry returns the first answer instead of repeating the action. |
| Serial | The unique number of an EDC terminal (payment device), for example `SN0001`. |
| Activation code | An 8-digit one-time code an operator creates so a terminal can register itself. |
| Provisioning | The step where a terminal trades its serial and activation code for its own permanent secret. |
| MQTT | A lightweight messaging system the server uses to push commands to terminals. |

For the full terminal flow, open the EDC guide in the Reference tab.

## 9. For module authors: documenting a method

Files under `internal/documents/apidoc/<lang>/` (`en`, `vn`, sub-folders included) are embedded in the binary at build time.
Point to one with `DescriptionFile` on `orm.ModelSpec` or `orm.RuntimeMethod`, relative to the language folder.
Add the same file in every language; a missing translation falls back to `en`.

```go
{Name: "ping", Description: "Short summary.", DescriptionFile: "x/ping.md"}
```

* `Description` is plain text. `DescriptionFile` renders behind **Show Description** in the Reference tab.
* This README is served as the Guide tab through `GET /doc/guide.md`.
* Return a typed error (`orm.NotFoundf`, `orm.Forbiddenf`, `orm.Conflictf`, `orm.InvalidParamsf`) instead of a plain `error`
  so callers get a precise code. A plain `error` becomes `-32000`.
