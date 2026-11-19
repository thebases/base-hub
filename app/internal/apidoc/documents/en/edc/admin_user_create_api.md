## Create User

Creates a new login user (someone who can sign in to Base HUB). Use it when you onboard a new operator or admin.

### Before you start

- You need a bearer token (the access token from `POST /oauth/token`, see "Get Access Token").
- Your user needs permission to create `base.users` records. If not, you get `403`.

### Request

```http
POST /base/admin/users
Authorization: Bearer <token>
Content-Type: application/json
```

Put the user fields inside a `values` object. `values` can also be a list of objects. Then the server creates several users and `result` is a list.

| Body field | Type | Required | What it means |
| --- | --- | --- | --- |
| `values.login` | string | Yes | The name the user types to sign in, e.g. `operator.hcm01`. |
| `values.password` | string | Yes for an active user | Plain text, e.g. `S3cret-pass`. The server stores only a hash. An active user without a password is rejected. |
| `values.name` | string | No | Display name, e.g. `Operator HCM 01`. |
| `values.email` | string | No | Email address, e.g. `op01@example.com`. |
| `values.group_id` | integer | No | The user's main group, e.g. `3`. The group decides what the user may do. |
| `values.company_id` / `values.company_ids` | integer / integer[] | No | Company the user belongs to, e.g. `1` or `[1,2]`. |
| `values.lang_id`, `values.tz` | string | No | Language and time zone, e.g. `en_US`, `Asia/Ho_Chi_Minh`. |

For the full field list call `GET /doc/base.users.json`.

### Example

```bash
curl -X POST "$BASE_URL/base/admin/users" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"values":{"login":"operator.hcm01","password":"S3cret-pass","name":"Operator HCM 01","group_id":3}}'
```

### Response

HTTP `200`:

```json
{
  "id": "111020261535481234570001",
  "result": { "id": 8, "login": "operator.hcm01", "name": "Operator HCM 01", "group_id": 3 }
}
```

| Field | What it means |
| --- | --- |
| `id` | Request id. Quote it when you report a problem. |
| `result` | The new user row. `result.id` is the numeric user id you use in the other `/base/admin/users/{id}` calls. |

> **Warning:** Treat `result` as sensitive. Do not write it to logs. Stored columns such as the password hash can be part of it.

### Errors

Errors use the standard format described in the API guide (see "Response" → "Errors").

| HTTP | `error.code` | What it means | What you should do |
| --- | --- | --- | --- |
| `400` | `-32602` or `-32000` | `values` is missing, an active user has no password, the login already exists, or another validation failed. | Read `error.message` and fix the body. |
| `401` | `-32001` | Missing or invalid token. | Get a new token. |
| `403` | `-32003` | Your user may not create users. | Ask an admin for access. |

### Good to know

- To change the password later, use `POST /base/admin/users/{id}/password`.
- Also available as `POST /rpc/base.users/create` with body `{"kwargs":{"values":{...}}}`.
