# base-auth: User and Administrator Guide

## 1. Purpose and who uses it

`base-auth` is the built-in OAuth 2.0 authorization server of Base HUB. It issues signed access tokens (RS256 JWT) and rotating refresh tokens so that people, services, AI agents and third-party apps can call `/rpc` and `/mcp` safely. If an external OIDC provider (Casdoor, Keycloak, ...) is enabled through `AuthService.Enable`, the same token contract is delegated to that provider.

| Audience | What they do here |
| --- | --- |
| System administrator | Registers OAuth clients, rotates client secrets, reviews and revokes tokens |
| Integrator / developer | Obtains tokens through `/oauth/token` and `/oauth/authorize` |
| End user | Signs in on the browser login page when an external app asks for access |

## 2. Prerequisites and installation

- Depends on `base`. The module is `auto_install`: it is installed automatically and is part of the kernel-adjacent set.
- No manual setup is needed to start using password login. The signing key is generated on first use and stored in the system parameters.
- Only users in the `base.group_system` group can manage the data screens.
- Token lifetimes come from server configuration: default access token 15 minutes, default refresh token 30 days (settings `AccessTokenTTLMinutes` and `RefreshTokenTTLDays`).

## 3. Key concepts

| Concept | Model | Plain-words meaning |
| --- | --- | --- |
| OAuth Client | `sys.oauth_client` | A registered caller (service, automation, GPT Action, AI agent) with its own client ID and secret. Replaces the old shared static API key |
| OAuth Token | `sys.oauth_refresh_token` | A refresh token issued with each access token. Stored hashed, rotated on every use. Rows sharing one Family ID belong to the same login chain |
| Authorization code | `sys.oauth_auth_code` | Short-lived, single-use code created by `/oauth/authorize`, exchanged once for tokens. Internal, no screen |
| Activation code | `sys.activation_code` | Generic one-time, short-lived code that other modules (for example device activation) attach to a record. Internal, no screen |
| Access token | - | Short-lived Bearer token sent as `Authorization: Bearer <token>` |
| Role / Is Superadmin | - | The role a client token is bound to; Is Superadmin bypasses role checks entirely |

## 4. Where to find it

| Menu | Path | Shows |
| --- | --- | --- |
| Admin > OAuth Clients | `/web/sys.oauth_client` | Registered clients |
| Admin > OAuth Tokens | `/web/sys.oauth_refresh_token` | Issued refresh tokens |

OAuth Clients list columns: Client ID, Service Name, Role, Is Superadmin, Active. Filters: Active, Archived; group by Role.
OAuth Tokens list columns: User, Database Name, Family ID, Issued At, Expires At, Revoked At. Filter: Revoked; group by Database or User.

## 5. Step-by-step tasks

### Register an OAuth client
1. Open Admin > OAuth Clients and create a new record.
2. Enter a Client ID (a public identifier) and a Service Name.
3. Choose a Role, or tick Is Superadmin only if the caller needs full access.
4. Under the Redirect URIs tab, add the exact callback URLs if the client will use the browser authorization code flow.
5. Save, then click Generate Client Secret. Copy the secret immediately: it is shown once and only its hash is stored.

### Rotate a client secret
Open the client and click Generate Client Secret again. The old secret stops working immediately; update the calling system.

### Disable or remove a client
Untick Active (the record shows an Archived ribbon) to block it without losing history, or delete the record to remove it.

### Get a token (integrator)
1. Service-to-service: `POST /oauth/token` with `grant_type=client_credentials`, `client_id`, `client_secret`.
2. User login: `grant_type=password` with `username`, `password`, optional `db`.
3. Renew: `grant_type=refresh_token` with the refresh token. The old one is rotated out.
4. Browser apps: send the user to `GET /oauth/authorize` (`response_type=code`, optionally PKCE `code_challenge`), then exchange the code with `grant_type=authorization_code`.
5. Call the API with `Authorization: Bearer <access_token>`.

### Investigate or revoke tokens
Open Admin > OAuth Tokens, filter Revoked or group by User. Rows are read-only; use `POST /oauth/revoke` to revoke a token. You can delete rows if needed.

## 6. Administration and configuration

| Item | Detail |
| --- | --- |
| Access | Group `base.group_system` has full rights on OAuth Clients; on OAuth Tokens it can read, write and delete but not create (tokens are only issued by the server) |
| Seeds | None |
| Secrets | Client secrets are bcrypt-hashed; refresh tokens and codes are SHA-256 hashed; activation codes use a server-only HMAC |
| Login throttling | Password login is rate limited: backoff after 5 failures (30 seconds growing to 15 minutes) and 30 attempts per minute per IP. The state is in memory (single instance) |
| External provider | Set `AuthService.Enable` to delegate to Casdoor, Keycloak or another OIDC provider |

## 7. API and automation touchpoints

| Touchpoint | Purpose |
| --- | --- |
| `POST /oauth/token` (also `/auth/token`) | Grants: `password`, `refresh_token`, `client_credentials`, `authorization_code`; form or JSON body |
| `GET`/`POST /oauth/authorize` | Browser login and authorization (`response_type=code` or `token`) |
| `POST /oauth/revoke` | Revoke a token |
| `GET /oauth/me` | Current session information |
| Method `generate_client_secret` on `sys.oauth_client` | Rotate a secret; returns the plaintext once |
| MCP `auth.record.search_read`, `read`, `create`, `write`, `unlink` | Manage `sys.oauth_client`; secrets never returned except at creation; `create` needs an `idempotency_key` |

Errors from `/oauth/token` follow RFC 6749: `{"error": "...", "error_description": "..."}`.

## 8. Troubleshooting and FAQ

| Problem | Likely cause and action |
| --- | --- |
| `invalid_client` | Wrong Client ID or secret, or the client is not Active. Regenerate the secret |
| `invalid_grant` | Expired, already used or revoked code or refresh token. Log in again |
| `unsupported_grant_type` | Use one of the four supported grant types |
| Redirect error at `/oauth/authorize` | The `redirect_uri` is not in the client's Redirect URIs (exact match) |
| Login blocked temporarily | Too many failed attempts; wait for the retry period |
| Lost the client secret | It cannot be recovered; generate a new one |

## 9. Limits and notes

- `scope` is accepted and echoed back, but not enforced; permissions come from the client's Role or Is Superadmin.
- PKCE is optional: verified only when the authorize request included a challenge.
- The implicit flow (`response_type=token`) never issues a refresh token.
- Legacy opaque `authtoken` and `mcp_api_key` credentials remain valid during migration alongside Bearer tokens.
- Rate limiting is per process; multi-instance deployments need a shared store.
