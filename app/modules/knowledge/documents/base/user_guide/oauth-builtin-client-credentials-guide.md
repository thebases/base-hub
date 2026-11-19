# Operator Guide: Builtin OAuth 2.0 — Admin-Issued Client Credentials

| Field | Value |
|---|---|
| Version | 1.0 |
| Last updated | 2026-08-09 |
| Applies to | `modules/base-auth` builtin OAuth 2.0 server (`AuthService.Enable: false`, the shipped default) |
| Audience | Backend operators registering automation/service callers (scripts, integrations, MCP clients) |

---

## Overview

Base HUB's inbound authentication is OAuth 2.0 only — there is no `X-API-Key`
header, no `mcp_api_key`, and no `Authorization: Basic` on `/rpc`/`/mcp`
anymore (RBAC/OAuth backend plan, §1/§6). Every caller — human user or
automation/service — authenticates through `POST /oauth/token` and sends the
resulting JWT as `Authorization: Bearer <access_token>`.

This guide covers the **service/automation caller** path: registering a
`sys.oauth_client` row so a script, cron job, or external integration can
obtain its own token via `grant_type=client_credentials`, without ever
handling a human user's password. This is the direct successor to the old
shared static `X-API-Key` — the key difference is that **every caller now
gets its own revocable credential** instead of one shared secret trusted by
everything.

This guide assumes the builtin issuer is active, i.e. `AuthService.Enable:
false` in `config.yaml`/`config_dev.yaml` (the shipped default — see
[External IdP setup guide](./oauth-external-idp-setup-guide.md) for the
`Enable: true` path). `POST /oauth/token` and `POST /oauth/revoke` behave
identically from the caller's point of view regardless of which provider is
active.

### Human users vs. service clients

`sys.oauth_client` is an isolated model for third-party callers: MCP clients,
GPT Actions, AI agents, scripts, and cron jobs. It is not a 1:1 extension of
`base.users`, and rows are not auto-created when users are created.

- **Human user:** use the password grant with the user's own Base HUB
  login/password when the action should run as that person.
- **Third-party/service caller:** create a dedicated `sys.oauth_client` row
  bound to a `role_id` (or `is_superadmin`) when the caller should have its
  own independently revocable identity.

If a client secret is lost, rotate it from the OAuth Clients admin screen with
**Generate Client Secret**. The plaintext is shown once; only its hash is
stored afterward.

---

## Prerequisites

- [ ] A superadmin (or otherwise privileged) Base HUB user account able to
      call `sys.oauth_client` RPC methods.
- [ ] The tenant database name (`db`) the client should be scoped to.
- [ ] A decision on the client's trust level: bound to one RBAC role
      (`role_id`), or `is_superadmin: true` (bypasses RBAC entirely — same
      trust level the old static API key had; reserve for infrastructure-level
      automation, not ordinary integrations).

---

## Step 1 — Create the client credential

Call `sys.oauth_client`'s `create` RPC method (`POST
/rpc/sys.oauth_client/create`) as a privileged user:

```json
{
  "args": [{
    "client_id": "billing-sync-bot",
    "service_name": "Nightly billing reconciliation job",
    "role_id": 4,
    "is_superadmin": false
  }],
  "kwargs": {}
}
```

- `client_id` — a stable, human-readable public identifier for this caller.
  Pick something that says what it is (`billing-sync-bot`, not `client-1`).
- `client_secret` — **omit this field** to have the server generate a
  high-entropy random secret for you (recommended). If you supply your own,
  it must be high-entropy; it is bcrypt-hashed at rest either way.
- `role_id` — the `tbl_groups` row this client's tokens are bound to. Ignored
  when `is_superadmin` is `true`.
- `is_superadmin` — `true` bypasses RBAC entirely for this client's tokens.
  Defaults to `false`. Treat `true` the same way you treated the old shared
  API key: sparingly, and only for infrastructure automation you fully trust.

### One-time secret reveal

The response's `client_secret` field contains the **plaintext secret exactly
once** — the same one-time-reveal convention the removed
`action_generate_mcp_api_key` used. Copy it into your secrets manager
immediately:

```json
{
  "id": 12,
  "client_id": "billing-sync-bot",
  "client_secret": "9f1c2e...<64 hex chars>...",
  "service_name": "Nightly billing reconciliation job",
  "role_id": 4,
  "is_superadmin": false
}
```

After this response, only `client_secret_hash` is stored — there is no way
to retrieve the plaintext again. If it's lost, rotate the credential (Step 4)
rather than trying to recover it.

---

## Step 2 — Have the caller request a token

The automation caller exchanges `client_id`/`client_secret` for an access
token via `POST /oauth/token` (form-encoded, RFC 6749 §4.4):

```bash
curl -X POST https://<your-host>/oauth/token \
  -d grant_type=client_credentials \
  -d client_id=billing-sync-bot \
  -d client_secret=9f1c2e...<64 hex chars>... \
  -d db=<tenant-db-name>
```

Response (RFC 6749 §5.1):

```json
{
  "access_token": "eyJhbGciOiJSUzI1NiJ9...",
  "expires_in": 900,
  "token_type": "Bearer"
}
```

Note there is **no `refresh_token`** on the `client_credentials` grant —
unlike a human password login, a service caller is expected to re-request a
new access token (repeat the same call) once the short-lived one expires,
rather than maintain a refresh chain.

## Step 3 — Use the token

Attach it as a bearer token on every `/rpc` or `/mcp` call:

```bash
curl https://<your-host>/rpc/tbl.partner/search_read \
  -H "Authorization: Bearer eyJhbGciOiJSUzI1NiJ9..." \
  -H "X-Database-Name: <tenant-db-name>" \
  -d '{"args": [], "kwargs": {"domain": [], "fields": ["id", "name"]}}'
```

The access token TTL defaults to 15 minutes
(`server.accessTokenTtlMinutes` in `config.yaml`); the caller must re-request
a token before it expires, catching a `401` and re-authenticating is the
simplest integration pattern.

---

## Step 4 — Rotate or revoke a credential

- **Rotate the secret** (keep the same `client_id`, issue a new secret): call
  `write` with a new `client_secret` value, or omit it to have the server
  generate one — either way the response does **not** echo the new secret
  back (only `create` does the one-time reveal). If you need the plaintext
  back, delete and re-create the client instead.
- **Deactivate without deleting** (temporarily stop a caller without losing
  its config/history): `write` with `{"active": false}`.
- **Delete permanently**: call `unlink` with the record's numeric `id`.
  Any access tokens already issued to that client remain valid until their
  short TTL naturally expires — `client_credentials` tokens are not
  individually revocable the way refresh tokens are (there's no long-lived
  credential to revoke, only the short-lived access token itself).

---

## Step 5 — Register a redirect URI for browser-based clients

A client that only uses `grant_type=client_credentials` (this guide's main
path) never needs this step. Register a redirect URI only if the same
`sys.oauth_client` will also be used with a browser-redirect flow:
Authorization Code (`response_type=code`, then `grant_type=authorization_code`)
or Implicit (`response_type=token`, access token returned in the redirect
fragment). See the "OAuth" panel setup in
[base-mcp-user-guide.md](./base-mcp-user-guide.md), for example a ChatGPT
Custom GPT Action configured with Authentication Type: OAuth.

Set `redirect_uris` on `create` or `write`:

```json
{
  "args": [{
    "client_id": "chatgpt-billing-gpt",
    "role_id": 4,
    "redirect_uris": ["https://chat.openai.com/aip/g-<your-gpt-id>/oauth/callback"]
  }],
  "kwargs": {}
}
```

- `redirect_uris` — an **exact-match** allow-list. `GET /oauth/authorize`
  rejects any request whose `redirect_uri` isn't a byte-for-byte match
  against one of these entries — no prefix or wildcard matching. Add both
  `chat.openai.com` and `chatgpt.com` callback variants if you're not sure
  which one ChatGPT will use for your GPT.
- `allowed_grants` — optional list of the grants this client may use:
  `authorization_code`, `implicit`, `client_credentials`. Leave it empty to
  allow all of them (the behavior before this field existed). A grant outside
  the list is refused.
- `is_public` — set it for a client that cannot keep its secret (single-page
  app, mobile app, CLI). Its `authorization_code` requests must send
  `code_challenge` with `code_challenge_method=S256`, and `client_credentials`
  and `implicit` are refused for it.
- Authorization Code re-checks `redirect_uri` when exchanging the code at
  `POST /oauth/token`; Implicit returns the short-lived access token directly
  in the redirect URI fragment. In both cases, getting `redirect_uri` wrong
  fails closed instead of silently misrouting a credential.

---

## Security notes

- Client secrets are bcrypt-hashed at rest, verified with a constant-time
  compare, and never logged.
- Give each caller its **own** `sys.oauth_client` row. Do not share one
  credential across multiple unrelated integrations — the entire point of
  this design over the old shared static key is per-caller revocability and
  audit trail.
- Treat `is_superadmin: true` clients as equivalent to root-level
  infrastructure credentials: minimize how many exist, and audit them
  periodically (`sys.oauth_client` `search_read` with `domain:
  [["is_superadmin", "=", true]]`).
- `redirect_uris` is only meaningful for browser flows
  (`response_type=code` or `response_type=token`). Leave it empty for pure
  `client_credentials` callers — an empty allow-list means
  `GET /oauth/authorize` will reject every request for that client, which is
  the correct fail-closed default.
