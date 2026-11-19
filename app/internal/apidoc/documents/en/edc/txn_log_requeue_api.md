## Requeue Failed Transaction Log

Marks one failed result or settle callback log as `PENDING` again, so the server's retry job processes it once more. Use it after you fixed the cause of a `FAILED` log. Nothing is processed immediately.

### Before you start

- A Base HUB bearer token.
- Permission to write `edc.txn.log`.
- The log `id` of a `FAILED` log (find it with "Search Transaction Callback Logs").

### Request

```http
POST /base/edc/transaction/logs/{id}/requeue
Authorization: Bearer <token>
Content-Type: application/json
```

The body is the RPC call envelope and takes no parameters (send `{}`). RPC form: `POST /rpc/edc.txn.log/requeue/{id}`.

| Name | Where | Type | Required | What it means |
| --- | --- | --- | --- | --- |
| `id` | path | integer | Yes | Log ID, e.g. `5501`. Exactly one id. |

### Example

```bash
curl -X POST "$BASE_URL/base/edc/transaction/logs/5501/requeue" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{}'
```

### Response

HTTP `200`.

```json
{
  "id": "111020261535481234570001",
  "result": { "id": 5501, "requeued": true }
}
```

`requeued` is `true` when the log was reset. It is `false` when nothing changed (this is not an error).

### Errors

Errors use the standard format described in the API guide (see "Response" → "Errors").

| HTTP | `error.message` (or cause) | What it means | What you should do |
| --- | --- | --- | --- |
| `401` | none | Token missing or invalid. | Get a new token. |
| `403` | `Permission denied: ...` | You may not write `edc.txn.log`. | Ask an admin for access. |
| `400` | `edc.txn.log requeue requires exactly one id` | The path has several ids. | Send one id. |
| `400` | `path id "..." must be a positive integer` | The id is not a positive number. | Fix the id. |
| `400` | `edc.txn.log requeue: ...` | Database error. | Retry later. |

### Good to know

- Only a row with `parse_status` = `FAILED` and `log_type` = `EDC_RESULT_REQUEST` or `EDC_SETTLE_REQUEST` is changed. Any other row, or an unknown id, is left alone and returns `requeued: false`.
- On success `parse_status` becomes `PENDING` and `locked_at` is cleared.
- Safe to repeat.
