## List Groups

Returns device groups, page by page. Use it to fill a group picker or a groups table, optionally filtered by name or customer.

### Before you start

You need a bearer token whose user may read groups (permission check, ACL). Without it you get `403`.

### Request

```http
GET /base/groups?limit={limit}&offset={offset}&name={name}&customer_id={customer_id}
Authorization: Bearer <token>
```

| Query parameter | Type | Required | What it means |
| --- | --- | --- | --- |
| `limit` | int | No (default `50`) | Page size, `1` to `500`. A value outside this range (or not a number) becomes `50`. |
| `offset` | int | No (default `0`) | How many rows to skip, e.g. `50` for page 2. |
| `name` | string | No | Case-insensitive "contains" match on the name, e.g. `hanoi`. |
| `customer_id` | int | No | Only groups owned by this customer, e.g. `12`. |

### Example

```bash
curl "$BASE_URL/base/groups?limit=20&offset=0&name=hanoi" \
  -H "Authorization: Bearer $TOKEN"
```

### Response

Groups are sorted by `id`, lowest first.

```json
{
  "id": "111020261535481234570001",
  "result": {
    "items": [
      { "id": 3, "name": "Hanoi Stores", "customerId": 12 },
      { "id": 7, "name": "Hanoi Kiosks" }
    ],
    "total": 2
  }
}
```

| Field | What it means |
| --- | --- |
| `result.items` | The groups on this page. Empty list `[]` when nothing matches. |
| `result.items[].customerId` | Owner customer id. Left out when the group has no customer. |
| `result.total` | Number of matching groups before paging (`0` when none). |

### Errors

Errors use the standard format described in the API guide (see "Response" → "Errors").

| HTTP | `error.code` | What it means | What you should do |
| --- | --- | --- | --- |
| `401` | `-32001` | Missing or invalid token. | Get a new token. |
| `403` | `-32003` | The user has no access to groups. | Ask an admin for access. |
| `500` | `-32603` | Unexpected server error. | Retry later; report it with the response `id`. |

### Good to know

- A `customer_id` that is not a number is silently ignored, so you get the unfiltered list. Check it on your side.
- A `limit` above `500` is not cut to `500`. It resets to `50`.
