## Get Current User

Tells you who the token belongs to. Call it right after login to show the user name and the language and company scope.

### Request

```http
GET /oauth/me
Authorization: Bearer <token>
```

`GET /auth/me` is the same route under another name. This route does not need a valid token to answer. See "Response".

| Name | Type | Required | What it means |
| --- | --- | --- | --- |
| `X-Database-Name` (header) or `db` (query) | string | No | Which tenant database to use, e.g. `base_hub`. Default is the home database. If you send both, `db` wins. |
| `X-API-Key` (header) | string | No | Use it instead of the Bearer token. It works only when the server turns on `app.supportApikey`. |

### Example

```bash
curl "$BASE_URL/oauth/me" \
  -H "Authorization: Bearer $TOKEN"
```

### Response

HTTP `200`:

```json
{
  "result": {
    "db": "base_hub",
    "uid": 7,
    "is_web": true,
    "valid": true,
    "auth_mode": "oauth",
    "username": "operator01",
    "db_header": "X-Database-Name",
    "user_context": { "lang": "en_US", "allowed_company_ids": [1], "company_id": 1 }
  }
}
```

| Field | What it means |
| --- | --- |
| `result.valid` | `true` when the token is good. **Always check this field.** |
| `result.uid` | The user id. It is `1` when `valid` is `false`. |
| `result.db` | The database the call uses. |
| `result.auth_mode` | `oauth` (Bearer token), `apikey` (`X-API-Key`) or `none` (no valid identity). |
| `result.username` | The sign-in name. Only present when `valid` is `true`. |
| `result.db_header` | The header name to send the database in. Only present when `valid` is `true`. |
| `result.user_context` | The user's language (`lang`), allowed companies (`allowed_company_ids`) and current company (`company_id`). Only present when `valid` is `true`. |

> **Warning:** With a missing, wrong or expired token the server still answers `200`, with `valid: false`. It does not answer `401`. Do not rely on the HTTP status.

### Errors

Errors on this route are plain-text bodies with an HTTP status. They are not JSON.

| HTTP | What it means | What you should do |
| --- | --- | --- |
| `400` | The database name cannot be found. | Check `db` or `X-Database-Name`. |

### Good to know

- A token for a different database also gives `valid: false`.
- The response is sent with `Cache-Control: no-store`, so it is never cached.
- For profile fields such as name, email and group, read the user row: `GET /base/admin/users/{id}`.
