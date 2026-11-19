## Delete Settlement

Permanently removes one settlement (the bank's end-of-batch total for a terminal). Use it only for operator clean-up.

### Before you start

- A Base HUB bearer token.
- Permission to delete `edc.settlement.journal`.

### Request

```http
DELETE /base/settlement/{id}
Authorization: Bearer <token>
```

| Path parameter | Type | Required | What it means |
| --- | --- | --- | --- |
| `id` | integer | Yes | Settlement ID, e.g. `88`. |

### Example

```bash
curl -X DELETE "$BASE_URL/base/settlement/88" \
  -H "Authorization: Bearer $TOKEN"
```

### Response

HTTP `200`.

```json
{
  "id": "111020261535481234570001",
  "result": { "deleted": true, "id": 88 }
}
```

### Errors

Errors use the standard format described in the API guide (see "Response" → "Errors").

| HTTP | `error.code` | What it means | What you should do |
| --- | --- | --- | --- |
| `400` | `-32602` | `id` is not an integer. | Send a number. |
| `401` | `-32001` | Token missing or invalid. | Get a new token. |
| `403` | `-32003` | You may not delete this model. | Ask an admin for access. |
| `404` | `-32004` | No settlement has this ID. | Nothing to delete (or already deleted). |
| `500` | `-32603` | Database error. | Retry later. |

### Good to know

- This is a hard delete and cannot be undone. Deleting twice gives `404` the second time.
- Transactions of that batch are not changed. They keep their `settleId`, which then points to a settlement that no longer exists.
