## Update User

Changes fields of a login user. You can also turn the user off or on. Use it for profile edits, group changes and disabling accounts.

### Before you start

- You need a bearer token.
- Your user needs permission to edit that user. If not, you get `403`. A normal (non-admin) user can only change a few fields of their own profile: name, email, signature, language, time zone, phone and company.

### Request

```http
PUT /base/admin/users/{id}
Authorization: Bearer <token>
Content-Type: application/json
```

| Path parameter | Type | Required | What it means |
| --- | --- | --- | --- |
| `id` | integer | Yes | The user id, e.g. `8`. It is a number, not a UUID. |

| Body field | Type | Required | What it means |
| --- | --- | --- | --- |
| `values` | object | Yes | The fields to change, e.g. `{"name":"New Name","group_id":3,"active":false}`. A zero or missing `group_id` is ignored. It never clears the group. |

### Example

```bash
curl -X PUT "$BASE_URL/base/admin/users/8" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"values":{"name":"Operator HCM 01","group_id":3}}'
```

### Response

HTTP `200`. The response does not contain the user. Read it again with `GET /base/admin/users/{id}` if you need it.

```json
{ "id": "111020261535481234570001", "result": true }
```

### Errors

Errors use the standard format described in the API guide (see "Response" → "Errors").

| HTTP | `error.code` | What it means | What you should do |
| --- | --- | --- | --- |
| `400` | `-32602` or `-32000` | `values` is missing, the id is invalid, or a validation failed. | Read `error.message` and fix the request. |
| `401` | `-32001` | Missing or invalid token. | Get a new token. |
| `403` | `-32003` | Your user may not edit this user. | Ask an admin for access. |

### Good to know

- Disable a user with `{"values":{"active":false}}`. Enable again with `true`.
- Do not send `password` here. Use `POST /base/admin/users/{id}/password`.
