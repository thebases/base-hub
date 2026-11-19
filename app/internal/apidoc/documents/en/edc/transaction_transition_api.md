## Transition Transaction Status

Moves one transaction to a new status, for example `INITIATED` to `PENDING_DEVICE` after a start command was sent to the terminal. Use it from an operator tool or an integration. The server only allows valid moves and writes an audit entry for each change.

### Transaction statuses in plain words

| Status | Meaning |
| --- | --- |
| `INITIATED` | Payment started; no answer yet. |
| `PENDING_DEVICE` | A start command was sent to the terminal; the server waits for it. |
| `PROCESSING` | The terminal is processing the payment. |
| `APPROVED` | The bank accepted the payment. |
| `DECLINED` / `FAILED` | The bank refused it, or it failed. |
| `TIMEOUT` | No answer in time. |
| `REVERSED` | An approved sale was cancelled by a void. |
| `SETTLED` | The batch holding this payment was closed with the bank (a **settlement**). |

### Before you start

- A Base HUB bearer token.
- Permission to write `edc.transaction`.
- The numeric `id` of the transaction record (not the `transactionId` string).

### Request

```http
POST /base/edc/transactions/{id}/transition
Authorization: Bearer <token>
Content-Type: application/json
```

The body is the RPC call envelope: put the parameters in `kwargs`. RPC form: `POST /rpc/edc.transaction/transition/{id}`.

| Name | Where | Type | Required | What it means |
| --- | --- | --- | --- | --- |
| `id` | path | integer | Yes | Numeric transaction row ID, e.g. `1001`. |
| `kwargs.status` | body | string | Yes | New status: `INITIATED`, `PENDING_DEVICE`, `PROCESSING`, `APPROVED`, `DECLINED`, `FAILED`, `TIMEOUT`, `REVERSED` or `SETTLED`. |
| `kwargs.reason` | body | string | No | Why you change it (saved in the audit log). Default `manual transition`. |
| `kwargs.result_code` | body | string | No | Saved on the transaction when not blank. |
| `kwargs.result_message` | body | string | No | Saved on the transaction when not blank. |

Allowed moves:

| From | To |
| --- | --- |
| `INITIATED` | `PENDING_DEVICE`, `PROCESSING`, `APPROVED`, `DECLINED`, `FAILED`, `TIMEOUT` |
| `PENDING_DEVICE` | `PROCESSING`, `APPROVED`, `DECLINED`, `FAILED`, `TIMEOUT` |
| `PROCESSING` | `APPROVED`, `DECLINED`, `FAILED`, `TIMEOUT` |
| `APPROVED` | `REVERSED`, `SETTLED` |
| `TIMEOUT` set by the server's timeout sweeper | `APPROVED`, `DECLINED`, `FAILED` |

Every other move is refused.

### Example

```bash
curl -X POST "$BASE_URL/base/edc/transactions/1001/transition" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"args": [], "kwargs": {"status": "PENDING_DEVICE", "reason": "start command published"}}'
```

### Response

HTTP `200`.

```json
{
  "id": "111020261535481234570001",
  "result": {
    "id": 1001,
    "transaction_id": "TXN-20261011-0001",
    "from": "INITIATED",
    "status": "PENDING_DEVICE",
    "changed": true
  }
}
```

| Field | What it means |
| --- | --- |
| `from` | Status before the call. |
| `status` | Status now. |
| `changed` | `true` if the status changed. `false` if it already had that status (nothing done, still `200`). |

### Errors

Errors use the standard format described in the API guide (see "Response" → "Errors").

| HTTP | `error.message` (or cause) | What it means | What you should do |
| --- | --- | --- | --- |
| `401` | none | Token missing or invalid. | Get a new token. |
| `403` | `Permission denied: ...` | You may not write `edc.transaction`. | Ask an admin for access. |
| `400` | `edc.transaction transition requires exactly one id` | The path has several ids. | Send exactly one id. |
| `400` | `edc.transaction transition: status is required` | `status` is missing or blank. | Send `kwargs.status`. |
| `400` | `path id "..." must be a positive integer` | The path id is not a positive number. | Fix the id. |
| `400` | `edc.transaction <id> not found` | No transaction has this id. | Check the id. |
| `400` | `... illegal status transition X -> Y` | The move is not allowed, the status is unknown, or another change happened at the same time. | Reload the transaction and pick an allowed move. |

> **Check:** the exact `error.code` of the `400` rows (and whether "not found" is reported as `404`) was not confirmed in code; branch on HTTP status and `error.message`.

### Good to know

- When a transaction first reaches a final status (`APPROVED`, `DECLINED`, `FAILED`, `TIMEOUT`, `REVERSED`, `SETTLED`), `completed_at` is set.
- Each change adds an entry with your `reason` to the transaction's state log.
- This call only changes the status. It does not move money or create a settlement.
