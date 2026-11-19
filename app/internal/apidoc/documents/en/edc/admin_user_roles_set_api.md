## Set User Roles

> **Not available.** `POST /base/admin/users/{id}/roles` does not exist. A request returns `404`. There are no roles such as `ADMIN` or `OPERATOR`. What a user can do depends on their **group** (`group_id`) and the access rules attached to that group.

Use the update call below to move a user to another group.

### Before you start

- You need a bearer token.
- Your user needs permission to edit that user. If not, you get `403`.

### Request

This is the same call as "Update User".

```http
PUT /base/admin/users/{id}
Authorization: Bearer <token>
Content-Type: application/json
```

| Body field | Type | Required | What it means |
| --- | --- | --- | --- |
| `values.group_id` | integer | Yes | The id of the group to assign, e.g. `3`. A zero, empty or non-number value is ignored (the group does not change). |

### Example

```bash
curl -X PUT "$BASE_URL/base/admin/users/8" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"values":{"group_id":3}}'
```

### Response

HTTP `200`:

```json
{ "id": "111020261535481234570001", "result": true }
```

### Errors

Errors use the standard format described in the API guide (see "Response" → "Errors").

| HTTP | `error.code` | What it means | What you should do |
| --- | --- | --- | --- |
| `401` | `-32001` | Missing or invalid token. | Get a new token. |
| `403` | `-32003` | Your user may not edit this user. | Ask an admin for access. |
| `404` | `-32601` | You called `POST .../roles`. This route does not exist. | Use `PUT /base/admin/users/{id}` instead. |

### Good to know

- A user has one main group (`group_id`). Other group links are managed through the `tbl.groups` model, not through this path.
- Tokens that already exist keep their old access until they expire or are refreshed.
