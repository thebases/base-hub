## Create or Update Settlement

Creates a settlement by hand, or changes an existing one. A **settlement** is the bank's end-of-batch total for a terminal. Use this for operator corrections. Normally the terminal's "EDC Batch Settle" call creates settlements.

### Before you start

- A Base HUB bearer token.
- Permission to write `edc.settlement.journal`.

### Request

```http
PUT /base/settlement
Authorization: Bearer <token>
Content-Type: application/json
```

- No `id` (or `id: 0`) means **create**.
- Any other `id` means **update**. Only the fields you send are changed. To clear a field, send `""` or `0`.
- Each field also works under its snake_case name (e.g. `settle_id`). Unknown keys are ignored.

| Body field | Type | Required | What it means |
| --- | --- | --- | --- |
| `id` | integer | No | Settlement ID to update. Leave out to create. |
| `settleId` | string | Yes (in practice) | Unique key, e.g. `SN0001:000012:STL20260926000012`. |
| `settleDate` | string (RFC 3339) | Yes (in practice) | Settle time, e.g. `2026-09-26T22:00:00+07:00`. If left out, `0001-01-01T00:00:00Z`. |
| `batchNo` | string | No | Batch number, e.g. `000012`. |
| `reqId`, `billId` | string | No | Request ID and bill ID. |
| `txnQuant`, `txnTotal` | integer | No | Net count and net amount. |
| `saleQuant`, `saleAmount` | integer | No | Sale count and sale amount. |
| `voidQuant`, `voidAmount` | integer | No | Void count and void amount. |
| `tid`, `mid` | string | No | Terminal ID and merchant ID. |
| `aliasValue` | string | No | Acquirer or merchant name. |
| `de37RefCode`, `de39AuthCode` | string | No | Reference code and response code (`00` = success). |

Amounts are integers in whole currency units (VND), e.g. `3750000`. Totals are saved exactly as you send them. The server does not check them against transactions.

### Example

```bash
curl -X PUT "$BASE_URL/base/settlement" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"id":88,"reqId":"STL20260926000012","billId":"000012","settleId":"SN0001:000012:STL20260926000012","settleDate":"2026-09-26T22:00:00+07:00","batchNo":"000012","txnQuant":25,"txnTotal":3750000,"saleQuant":26,"saleAmount":3900000,"voidQuant":1,"voidAmount":150000,"tid":"70010001","mid":"000000012345678","aliasValue":"BANKAPP","de39AuthCode":"00"}'
```

### Response

HTTP `200` for both create and update. The saved row is under `result`.

```json
{
  "id": "111020261535481234570001",
  "result": {
    "id": 88,
    "settleId": "SN0001:000012:STL20260926000012",
    "settleDate": "2026-09-26T22:00:00+07:00",
    "batchNo": "000012",
    "txnQuant": 25,
    "txnTotal": 3750000,
    "saleQuant": 26,
    "saleAmount": 3900000,
    "voidQuant": 1,
    "voidAmount": 150000,
    "tid": "70010001",
    "mid": "000000012345678",
    "aliasValue": "BANKAPP",
    "de39AuthCode": "00",
    "createdAt": "2026-09-26T15:00:01.310Z",
    "updatedAt": "2026-09-26T16:02:44.018Z"
  }
}
```

The example is shortened. The real response has every field of "Get Settlement".

### Errors

Errors use the standard format described in the API guide (see "Response" → "Errors").

| HTTP | `error.code` | What it means | What you should do |
| --- | --- | --- | --- |
| `400` | `-32602` | Body is not a valid JSON object. | Fix the body. |
| `401` | `-32001` | Token missing or invalid. | Get a new token. |
| `403` | `-32003` | You may not write this model. | Ask an admin for access. |
| `404` | `-32004` | You sent an `id` but no such settlement exists. | Check the ID. |
| `500` | `-32603` | Database or validation error, e.g. `settleId` already used by another settlement, or `settleDate` cannot be read. | Fix the field and retry. |

### Good to know

- Not idempotent, but creating again with the same `settleId` fails with `500` instead of making a duplicate.
- Updates are partial. You do not need to send the whole row.
- Changing `settleId` on update does not change the transactions that point to the old `settleId`.
- A later "EDC Batch Settle" call for the same `settleId` overwrites the totals you set here.
- Also available as `POST /base/edc/settlement/upsert` (RPC: `POST /rpc/edc.settlement.journal/upsert`). Send the row in `kwargs`. Rows come back with snake_case keys, and an unknown `id` or other failure is `400` with a plain-text message.
