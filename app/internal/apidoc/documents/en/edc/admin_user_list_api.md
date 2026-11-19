## List Users

Lists login users. Use it to build a user management table.

### Before you start

- You need a bearer token.
- Your user needs permission to read users. If not, you get `403`.

### Request

```http
GET /base/admin/users
Authorization: Bearer <token>
```

Query parameters become arguments of the call, for example `limit` and `offset` (how many rows, and how many to skip) and `order`. Query values always arrive as text. So if you need a filter or a field list, use the RPC form:

```http
POST /rpc/base.users/search_read
Authorization: Bearer <token>
Content-Type: application/json
```

| Name (in `kwargs`) | Type | Required | What it means |
| --- | --- | --- | --- |
| `domain` | array | No | Filter, e.g. `[["login","ilike","operator"]]` (login contains "operator"). |
| `fields` | string[] | No | Columns to return, e.g. `["id","login","name","group_id"]`. |
| `limit` / `offset` | integer | No | Paging, e.g. `20` and `0`. |
| `order` | string | No | Sorting, e.g. `login asc`. |

### Example

```bash
curl "$BASE_URL/base/admin/users?limit=20" \
  -H "Authorization: Bearer $TOKEN"
```

With a field list (recommended):

```bash
curl -X POST "$BASE_URL/rpc/base.users/search_read" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"kwargs":{"fields":["id","login","name","group_id"],"limit":20}}'
```

### Response

HTTP `200`. `result` is a list of user rows:

```json
{
  "id": "111020261535481234570001",
  "result": [
    { "id": 1, "login": "admin", "name": "Administrator", "group_id": 1 },
    { "id": 7, "login": "operator01", "name": "Operator One", "group_id": 3 }
  ]
}
```

| Field | What it means |
| --- | --- |
| `id` | User id (a number). |
| `login` | Sign-in name. |
| `name` | Display name. |
| `group_id` | The user's main group. |

### Errors

Errors use the standard format described in the API guide (see "Response" → "Errors").

| HTTP | `error.code` | What it means | What you should do |
| --- | --- | --- | --- |
| `400` | `-32602` or `-32000` | Unknown field or bad filter. | Fix `fields` or `domain`. |
| `401` | `-32001` | Missing or invalid token. | Get a new token. |
| `403` | `-32003` | Your user may not read users. | Ask an admin for access. |

### Good to know

- If you do not pass `fields`, every readable column comes back, including the stored password hash column. Always pass `fields` when you build a screen.
- The response has no `total`. Page with `limit` and `offset`.
- There is no `roles` list. Access comes from the user's group (`group_id`).
