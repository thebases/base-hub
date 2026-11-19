## Get Access Token

Signs in with a username and password and returns a bearer token (a text key you send in the `Authorization` header). Every protected route needs this token. This is usually the first call you make.

### Request

```http
POST /oauth/token
Content-Type: application/json
```

`POST /auth/token` is the same route under another name. It is public, so it needs no token. The body can be JSON or `application/x-www-form-urlencoded` with the same field names.

| Name | Type | Required | What it means |
| --- | --- | --- | --- |
| `grant_type` | string | Yes | How you sign in: `password`, `refresh_token`, `client_credentials` or `authorization_code`. |
| `username` | string | For `password` | The sign-in name, e.g. `operator01`. |
| `password` | string | For `password` | The password. |
| `refresh_token` | string | For `refresh_token` | The `refresh_token` from an earlier response. |
| `client_id` / `client_secret` | string | For `client_credentials` | A registered OAuth client. It is for service-to-service calls. |
| `db` | string | No | Tenant database name, e.g. `base_hub`. Empty means the home database. For `authorization_code` it is ignored. The database comes from the `X-Database-Name` header instead. |

`authorization_code` (with `code`, `redirect_uri`, `code_verifier`) is used by the browser login flow (`GET /oauth/authorize`). You normally do not call it by hand.

### Example

```bash
curl -X POST "$BASE_URL/oauth/token" \
  -H "Content-Type: application/json" \
  -d '{"grant_type":"password","username":"operator01","password":"S3cret-pass"}'
```

### Response

HTTP `200`. This route uses the OAuth 2.0 format. It has no `id` / `result` envelope.

```json
{
  "access_token": "eyJhbGciOiJSUzI1NiJ9.<payload>.<signature>",
  "refresh_token": "5f9d6c3b1a...",
  "expires_in": 900,
  "token_type": "Bearer"
}
```

| Field | What it means |
| --- | --- |
| `access_token` | The token. Send it as `Authorization: Bearer <access_token>`. |
| `refresh_token` | Use it to get a new access token without the password. Only present when the grant issues one. |
| `expires_in` | Seconds until the access token expires. Default `900` (15 minutes). It can be configured. |
| `token_type` | Always `Bearer`. |

### Errors

Errors use the OAuth 2.0 format: `{"error": "<code>", "error_description": "<text>"}`.

| HTTP | `error` | What it means | What you should do |
| --- | --- | --- | --- |
| `400` | `invalid_request` | `username` is empty for a `password` grant, or the JSON cannot be parsed. | Fix the body. |
| `400` | `unsupported_grant_type` | `grant_type` is missing or is not one of the four values. | Send a valid `grant_type`. |
| `401` | `invalid_grant` | Wrong username or password, or the refresh token or code is invalid or expired. | Check the credentials or sign in again. |
| `401` | `invalid_client` | `client_credentials`: unknown client or wrong secret. | Check `client_id` and `client_secret`. |
| `429` | `invalid_request` | Too many failed logins for this login and IP address. | Wait the number of seconds in the `Retry-After` header. |

### Good to know

- Refresh before the access token expires: call this route with `grant_type=refresh_token`. Refresh tokens **rotate**: each response gives a new one, so store it.
- `POST /oauth/revoke` (form field `refresh_token`) revokes a refresh token.
- The token carries no roles. Access is checked by permission when you call each route.
- With several databases, send the same `db` here and the `X-Database-Name` header on later calls. A token is rejected for another database.
- A JSON body without `grant_type` is read as an old login format `{"params":{"db","login","password","context"}}`. It answers `{"result":{"uid","db","access_token","refresh_token","expires_in","token_type","user_context","username","auth_type","db_header"}}`. Its errors are plain text. Use `grant_type=password` in new code.
