## Delete Bank App Info

Permanently deletes a bank app configuration (TID/MID) by its id.

### Before you start

- You need a bearer token.
- Your user needs permission to delete `edc.tidmid.config` records. If not, you get `403`.
- Check which device configs use this record first. See "Good to know".

### Request

```http
DELETE /base/bank-app-info/{id}
Authorization: Bearer <token>
```

| Name | Type | Required | What it means |
| --- | --- | --- | --- |
| `id` | integer | Yes | Bank app info id (in the path), e.g. `7`. |

### Example

```bash
curl -X DELETE "$BASE_URL/base/bank-app-info/7" \
  -H "Authorization: Bearer $TOKEN"
```

### Response

HTTP `200`:

```json
{
  "id": "111020261535481234570001",
  "result": { "deleted": true, "id": 7 }
}
```

| Field | What it means |
| --- | --- |
| `result.deleted` | `true` when the record was deleted. |
| `result.id` | The id of the deleted record. |

### Errors

Errors use the standard format described in the API guide (see "Response" → "Errors").

| HTTP | `error.code` | What it means | What you should do |
| --- | --- | --- | --- |
| `400` | `-32602` | `id` is not a number. | Send an integer id. |
| `401` | `-32001` | Missing or invalid token. | Get a new token. |
| `403` | `-32003` | Your user may not delete bank app info. | Ask an admin for access. |
| `404` | `-32004` | No record has this id. It may already be deleted. | Treat it as "already gone". |
| `500` | `-32603` | The record could not be deleted. | Retry later. Report the response `id`. |

### Good to know

> **Warning:** There is no undo. Confirm with the user before you call this.

- The record is also **silently removed from every device config** that uses it. Those devices lose this bank app on their next config sync.
- A retry after a successful delete returns `404`. That is normal.
