## Reset User Password

Sets a new password for a user. You do not need the old password. Use it when a user forgot theirs.

### Before you start

- You need a bearer token.
- Your user needs permission to edit that user. If not, you get `403`.

### Request

```http
POST /base/admin/users/{id}/password
Authorization: Bearer <token>
Content-Type: application/json
```

| Path parameter | Type | Required | What it means |
| --- | --- | --- | --- |
| `id` | integer | Yes | The user id, e.g. `8`. It is a number, not a UUID. Always taken from the path. An `id` in the body is ignored. |

| Body field | Type | Required | What it means |
| --- | --- | --- | --- |
| `password` | string | Yes | The new password, e.g. `N3w-pass-2026`. Spaces at both ends are trimmed. It must not be empty. No minimum length is enforced. The server stores only a hash. |

### Example

```bash
curl -X POST "$BASE_URL/base/admin/users/8/password" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"password":"N3w-pass-2026"}'
```

### Response

HTTP `200`:

```json
{ "id": "111020261535481234570001", "result": { "status": "OK" } }
```

`result.status` is `OK` when the password was changed. The user row is not returned.

### Errors

Errors use the standard format described in the API guide (see "Response" → "Errors").

| HTTP | `error.code` | What it means | What you should do |
| --- | --- | --- | --- |
| `400` | `-32602` or `-32000` | The id is empty, the body is not valid JSON, `password` is empty, or the user does not exist. | Read `error.message` and fix the request. |
| `401` | `-32001` | Missing or invalid token. | Get a new token. |
| `403` | `-32003` | Your user may not edit this user. | Ask an admin for access. |

### Good to know

- Tokens the user already has are **not** revoked. They stay valid until they expire.
- Also available as `POST /rpc/base.users/action_set_password/{id}` with body `{"kwargs":{"new_password":"..."}}`.
- For an API key (instead of a password) use the `base.users.action_generate_api_key` method.
