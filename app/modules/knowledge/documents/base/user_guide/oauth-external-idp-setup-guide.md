# Operator Guide: Registering Base HUB Against an External OIDC Provider

| Field | Value |
|---|---|
| Version | 1.0 |
| Last updated | 2026-08-08 |
| Applies to | `modules/base-auth` external OIDC path (`AuthService.Enable: true`) |
| Audience | Backend operators piloting or running Base HUB against Casdoor, Keycloak, or any spec-compliant OIDC/OAuth 2.0 provider |
| Status | Code path implemented and unit/integration-clean in isolation; **no live Casdoor/Keycloak pilot has been run yet** (RBAC/OAuth backend plan §4/§10.4). Treat this as the setup procedure to follow *when* that pilot happens, not a confirmation that it already has. |

---

## Overview

`AuthServiceConfig.Enable` selects which token issuer backs `POST
/oauth/token` and bearer-token validation on `/rpc` and `/mcp`:

- `false` (shipped default, every environment today) — the builtin OAuth 2.0
  server in `modules/base-auth` issues and validates its own self-signed
  JWTs. See [Builtin client-credentials guide](./oauth-builtin-client-credentials-guide.md).
- `true` — Base HUB proxies token requests to, and validates tokens against,
  an external OIDC-compliant IdP (Casdoor, Keycloak, or equivalent).

Either way, the caller-facing contract is identical: `POST /oauth/token`
with the same grant types, and `Authorization: Bearer <token>` on `/rpc` and
`/mcp`. Callers never need to know which provider is behind the flag.

**This plan does not authorize flipping the shipped default.** `Enable`
stays `false` in every checked-in `config.yaml`/`config_dev.yaml` until an
explicit follow-up decision is made (RBAC/OAuth backend plan §10 step 6). Use
this guide to set up and pilot the external path in your own
`config_dev.yaml` (or an environment-specific override) — do not commit
`Enable: true` to the shared default config files as a side effect.

---

## What this path does and does not cover

- **Covered**: `grant_type=password`/`client_credentials`/`refresh_token`
  proxied to the IdP's own RFC-compliant token endpoint, and
  signature/`iss`/`aud`/`exp` verification of IdP-issued access tokens via
  JWKS.
- **Not covered (explicitly out of scope)**: an interactive Authorization
  Code + PKCE browser login flow. That's a frontend redirect flow against the
  IdP directly and needs its own separate plan once this backend piece is
  actually piloted — this guide only gets the backend talking to the IdP's
  token/JWKS endpoints.

---

## Prerequisites

- [ ] A running Casdoor or Keycloak instance (or any OIDC-compliant IdP) you
      control, reachable from the Base HUB backend.
- [ ] Admin access to that IdP to register an OAuth application/client.
- [ ] Backend users you intend to authenticate via the IdP already exist as
      `base_users` rows in Base HUB — **`AutoProvision` is not implemented**
      (`oidc_provider.go`'s `Validate` returns an explicit error if
      `AutoProvision` is true, since auto-provisioning was deliberately left
      unbuilt — see Security notes below). An IdP login for an unknown local
      account is rejected, not auto-created.

---

## Step 1 — Register Base HUB as a client on the IdP

In your IdP's admin console, create a new OAuth 2.0 / OIDC client/application:

- **Grant types**: enable Resource Owner Password Credentials (`password`)
  and Client Credentials, matching what `POST /oauth/token` proxies. Enable
  refresh tokens if you want silent refresh to work end-to-end.
- **Redirect URI**: not required for this pilot — the interactive
  Authorization Code flow is out of scope (see above).
- Note down: **Client ID**, **Client Secret**, the **token endpoint URL**,
  and the **JWKS URL** (usually `.../.well-known/jwks.json` or discoverable
  from the IdP's OIDC discovery document).

## Step 2 — Configure `AuthServiceConfig` in Base HUB

Set these fields under `authService:` in your environment's config (pilot in
`config_dev.yaml`, per plan §10 step 4 — not the shared default):

```yaml
authService:
  enable: true
  endpoint: "https://<your-idp>/api/login/oauth/access_token"   # token endpoint
  clientId: "<idp-client-id>"
  clientSecret: "<idp-client-secret>"
  scopes: "openid profile email"
  serviceName: "base-hub"
  issuer: "https://<your-idp>"          # must match the IdP's "iss" claim exactly
  audience: "<idp-client-id-or-configured-audience>"
  jwksUrl: "https://<your-idp>/.well-known/jwks.json"
  roleClaim: ""                          # reserved for future role mapping; unused today
  loginClaim: "preferred_username"       # defaults to this if left empty
  tenantClaim: ""                        # empty = tokens valid on the home DB only
  autoProvision: false                   # keep false — see Security notes
  clockSkewSecs: 30
```

Field reference (`internal/config/config.go:AuthServiceConfig`):

| Field | Purpose |
|---|---|
| `endpoint` | IdP token endpoint that `POST /oauth/token` proxies `password`/`client_credentials`/`refresh_token` grants to. |
| `clientId` / `clientSecret` | Base HUB's own registered credentials on the IdP (Step 1). |
| `scopes` | Space-separated scopes sent on every proxied token request. |
| `issuer` / `audience` | Verified against the token's `iss`/`aud` claims on every `Validate` call — must match the IdP's actual values exactly, or every token is rejected. |
| `jwksUrl` | Where Base HUB fetches signing keys to verify token signatures. Cached 10 minutes; a token with an unknown key id forces at most one refetch per minute; if this URL is wrong or unreachable, **all token validation fails closed** (see Security notes). |
| `loginClaim` | Which JWT claim maps to a Base HUB `base_users.login`. Defaults to `preferred_username` if empty. |
| `tenantClaim` | Which JWT claim names the tenant database a token is valid for. The login is looked up in that database and the token is refused on every other one. Empty (default): IdP tokens are valid on the home database only. Multi-tenant deployments must set it and have the IdP emit the database name in that claim. |
| `autoProvision` | Must stay `false`. See Security notes. |
| `clockSkewSecs` | Leeway applied to `exp`/`nbf` checks, for minor clock drift between Base HUB and the IdP. |

## Step 3 — Restart / reload

`AuthService.Enable` is read live per call
(`baseauth.CurrentProvider()`), so a running server picks up the flag flip
without a restart if it's changed via `sys_config_param` at runtime. If
you're only editing the YAML file directly, a restart is required to load
it.

## Step 4 — Verify

1. `POST /oauth/token` with `grant_type=password` and a login/password valid
   on the IdP, for a user that also exists as a `base_users` row locally.
   Confirm you get back a token pair.
2. Call `/rpc` with `Authorization: Bearer <that access_token>` and confirm
   the request succeeds and resolves to the expected local identity/role.
3. Deliberately break the JWKS URL (typo it) and confirm `/rpc` calls now
   fail closed (401/unauthorized), never silently falling back to "trust the
   token" or to the builtin issuer.
4. Confirm a token issued for a login with **no** matching `base_users` row
   is rejected, not auto-provisioned.

---

## Security notes

- **JWKS/IdP outage fails closed.** If the JWKS endpoint is unreachable or
  returns an error, `Validate` returns an error — it never falls back to
  trusting an unverified token or to the builtin issuer. Plan for what
  happens to your users if the IdP goes down: this is a hard dependency once
  `Enable: true`, not a soft one.
- **`alg` is pinned to RS256.** Tokens signed with any other algorithm
  (including `none`) are rejected.
- **`AutoProvision` stays `false`.** Setting it `true` does not currently
  work — `Validate` returns an explicit "auto-provisioning is not
  implemented" error rather than silently creating an account. This is a
  deliberate trust-boundary decision (an IdP token proving identity at the
  IdP is not the same as authorizing a new local account), not a bug to work
  around by flipping the flag.
- **Revocation is best-effort/no-op for the external path.**
  `OIDCProvider.Revoke` is a no-op today — external IdPs aren't guaranteed to
  expose a standard revocation endpoint this provider can rely on. If you
  need to force-revoke a session against an external IdP, do it on the IdP
  side directly.
