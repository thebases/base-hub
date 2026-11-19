## Delete Transaction

Permanently removes one row from the transaction journal. Use it only for operator clean-up.

### Before you start

- A Base HUB bearer token.
- Permission to delete `edc.transaction.journal`.

### Request

```http
DELETE /base/transaction/{id}
Authorization: Bearer <token>
```

| Path parameter | Type | Required | What it means |
| --- | --- | --- | --- |
| `id` | integer | Yes | Journal ID, e.g. `1025`. |

### Example

```bash
curl -X DELETE "$BASE_URL/base/transaction/1025" \
  -H "Authorization: Bearer $TOKEN"
```

### Response

HTTP `200`.

```json
{
  "id": "111020261535481234570001",
  "result": { "deleted": true, "id": 1025 }
}
```

`result.deleted` is `true` and `result.id` is the ID you deleted.

### Errors

Errors use the standard format described in the API guide (see "Response" → "Errors").

| HTTP | `error.code` | What it means | What you should do |
| --- | --- | --- | --- |
| `400` | `-32602` | `id` is not an integer. | Send a number. |
| `401` | `-32001` | Token missing or invalid. | Get a new token. |
| `403` | `-32003` | You may not delete this model. | Ask an admin for access. |
| `404` | `-32004` | No transaction has this ID. | Nothing to delete (or it was already deleted). |
| `500` | `-32603` | Database error. | Retry later. |

### Good to know

- This is a hard delete. It cannot be undone.
- Deleting twice: the second call returns `404`.
- Only the journal row is removed. The transaction record (`edc.transaction`) is not touched.
- The raw terminal callbacks stay visible in `POST /base/transaction/logs/search`.
