## Get Group

Returns one device group by its id.

### Before you start

You need a bearer token whose user may read groups (permission check, ACL). Without it you get `403`.

### Request

```http
GET /base/groups/{id}
Authorization: Bearer <token>
```

| Path parameter | Type | Required | What it means |
| --- | --- | --- | --- |
| `id` | int | Yes | Group id, e.g. `3`. |

### Example

```bash
curl "$BASE_URL/base/groups/3" \
  -H "Authorization: Bearer $TOKEN"
```

### Response

```json
{
  "id": "111020261535481234570001",
  "result": { "id": 3, "name": "Hanoi Stores", "customerId": 12 }
}
```

| Field | What it means |
| --- | --- |
| `result.id` | Group id. |
| `result.name` | Group name. |
| `result.customerId` | Owner customer id. Left out when the group has no customer. |

### Errors

Errors use the standard format described in the API guide (see "Response" → "Errors").

| HTTP | `error.code` | What it means | What you should do |
| --- | --- | --- | --- |
| `400` | `-32602` | `id` is not an integer. | Send a number. |
| `401` | `-32001` | Missing or invalid token. | Get a new token. |
| `403` | `-32003` | The user has no access to groups. | Ask an admin for access. |
| `404` | `-32004` | No group has this id. | Check the id. |
| `500` | `-32603` | Unexpected server error. | Retry later; report it with the response `id`. |
