---
name: base-auth
version: 0.0.1
category: System
installable: true
depends: [base]
auto_install: true
frontend_widgets: {}
---

# Auth

Kernel-adjacent module that provides a built-in OAuth 2.0 authorization
server (self-issued RS256 JWT access tokens + rotating opaque refresh
tokens) and, when `AuthService.Enable` is set, delegates the same
`TokenIssuer` contract to an external OIDC-compliant provider (Casdoor,
Keycloak, ...).

This module owns:

- `sys.oauth_client` — registered service/automation callers authenticated
  via `grant_type=client_credentials` (the replacement for the static
  `X-API-Key` service credential).
- `sys.oauth_refresh_token` — hashed, rotate-on-use refresh tokens issued
  alongside every access token.

It exposes `POST /oauth/token` (RFC 6749 §4, `grant_type` in
`{password, refresh_token, client_credentials, authorization_code}`),
`GET`/`POST /oauth/authorize` (`response_type=code` or `token`), and
`POST /oauth/revoke`. `/rpc` and `/mcp` validate `Authorization: Bearer
<token>` against `baseauth.CurrentProvider().Validate` as an additional
accepted credential alongside the legacy opaque `authtoken`/`mcp_api_key`
paths, which remain live during the migration window described in the
RBAC/OAuth backend plan.

The builtin browser grants follow RFC 6749 §4.1/§4.2:

- **PKCE (RFC 7636), optional-if-present**: `code_challenge`/
  `code_challenge_method` (S256 or plain) at `/oauth/authorize`, verified
  against `code_verifier` at exchange time if — and only if — the authorize
  request included a challenge. Not required, so existing confidential
  clients (client_secret, no PKCE — e.g. the ChatGPT Custom GPT Action
  integration) keep working unchanged.
- **Spec-shaped errors**: once `client_id`/`redirect_uri` are validated,
  further request-shape failures (bad `response_type`, malformed PKCE
  params) redirect to `redirect_uri` with `error`/`error_description`
  (RFC 6749 §4.1.2.1) instead of an inline 400. `POST /oauth/token` returns
  `{"error": "<code>", "error_description": "..."}` (RFC 6749 §5.2) instead
  of Fiber's default `{"error": "<message>"}` shape.
- **Scope round-trip**: `scope` from the authorize request is stored with
  the code and echoed back in the token response's `scope` field — still not
  enforced, no scoped-permission model exists yet.
- **Implicit flow**: `response_type=token` reuses the same login UI and
  redirects with `access_token`, `token_type`, `expires_in`, `scope`, and
  `state` in the URI fragment. It never issues a refresh token.
