## Get User

Reads one login user by its numeric id. Use it to fill a user detail or edit screen.

### Before you start

- You need a bearer token.
- Your user needs permission to read that user. If not, you get `403`.
- Get the numeric id from `GET /base/admin/users` (list users) or from the create response.

### Request

```http
GET /base/admin/users/{id}
Authorization: Bearer <token>
```

| Path parameter | Type | Required | What it means |
| --- | --- | --- | --- |
| `id` | integer | Yes | The user id, e.g. `8`. It is a number, not a UUID. |

This route cannot choose columns. If you want only some fields, use the RPC form:

```http
POST /rpc/base.users/read
Authorization: Bearer <token>
Content-Type: application/json
```

| Name (in `kwargs`) | Type | Required | What it means |
| --- | --- | --- | --- |
| `fields` | string[] | No | Columns to return, e.g. `["id","login","name","group_id"]`. Fields such as `email` are filled from the linked partner record. |

### Example

```bash
curl "$BASE_URL/base/admin/users/8" \
  -H "Authorization: Bearer $TOKEN"
```

With the RPC form and a field list:

```bash
curl -X POST "$BASE_URL/rpc/base.users/read" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"args":[[8]],"kwargs":{"fields":["id","login","name","group_id"]}}'
```

### Response

HTTP `200`. Note that `result` is a **list with one row**, not a single object:

```json
{
  "id": "111020261535481234570001",
  "result": [
    { "id": 8, "login": "operator.hcm01", "name": "Operator HCM 01", "group_id": 3 }
  ]
}
```

| Field | What it means |
| --- | --- |
| `result[0].id` | User id. |
| `result[0].login` | Sign-in name. |
| `result[0].name` | Display name. |
| `result[0].group_id` | The user's main group. It decides what the user may do. |

### Errors

Errors use the standard format described in the API guide (see "Response" → "Errors").

| HTTP | `error.code` | What it means | What you should do |
| --- | --- | --- | --- |
| `400` | `-32602` or `-32000` | The id is invalid or you asked for an unknown field. | Check `id` and `fields`. |
| `401` | `-32001` | Missing or invalid token. | Get a new token. |
| `403` | `-32003` | Your user may not read this user. | Ask an admin for access. |

### Good to know

- The password and API key are never returned. When they are set you see the placeholder `••••••••`. When they are not set you see an empty value or `null`.
- There is no `roles` list. Access comes from the user's group (`group_id`).
