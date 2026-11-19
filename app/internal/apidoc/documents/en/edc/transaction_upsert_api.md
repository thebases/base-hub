## Create or Update Transaction

Creates a journal row by hand, or changes an existing one. Use it for operator corrections. Normal payments are created automatically from terminal callbacks.

### Before you start

- A Base HUB bearer token.
- Permission to write `edc.transaction.journal`.

### Request

```http
PUT /base/transaction
Authorization: Bearer <token>
Content-Type: application/json
```

- No `id` (or `id: 0`) means **create**.
- Any other `id` means **update**. Only the fields you send are changed. To clear a field, send `""` or `0`.
- Each field also works under its snake_case name (e.g. `edc_serial`). Unknown keys are ignored.

| Body field | Type | Required | What it means |
| --- | --- | --- | --- |
| `id` | integer | No | Journal ID to update. Leave out to create. |
| `transactionId` | string | Yes when creating | Terminal payment ID, e.g. `TXN20260926000123`. Not unique: creating twice makes two rows. |
| `reqId`, `billId` | string | No | Request ID and bill ID. |
| `txnDate` | string (RFC 3339) | No | Payment time, e.g. `2026-09-26T08:15:00+07:00`. If left out, `0001-01-01T00:00:00Z`. |
| `txnType` | string | No | `SALE`, `VOID`, `QR`... |
| `txnStatus` | string | No | `APPROVED`, `FAILED`, `TIMEOUT`... Not checked. |
| `txnAmount` | integer | No | Whole currency units, e.g. `150000`. No decimals. |
| `cardNo` | string | No | Masked card number only, e.g. `411111******1111`. |
| `edcSerial` | string | No | Terminal serial, e.g. `SN0001`. |
| `edcId` | string | No | Terminal ID as sent by the device. |
| `tid`, `mid` | string | No | Terminal ID and merchant ID. |
| `aliasValue`, `aid`, `appName`, `invoiceNo`, `batchNo`, `stan`, `cardHolder`, `cardType` | string | No | Card and receipt details. |
| `settleId` | string | No | Settlement key this line belongs to. Not checked against real settlements. Empty is saved as `null` (not settled). |
| `de37RefCode` | string | No | Bank reference number (RRN, ISO 8583 field 37). |
| `de39AuthCode` | string | No | Approval/response code (field 39). |

You cannot set `isVoid`, `isSettled`, `settledAt` or `voidAt` here. On update they keep their value.

### Example

```bash
curl -X PUT "$BASE_URL/base/transaction" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"transactionId":"TXN20260926000123","txnDate":"2026-09-26T08:15:00+07:00","txnType":"SALE","txnStatus":"APPROVED","txnAmount":150000,"cardNo":"411111******1111","edcSerial":"SN0001","tid":"70010001","mid":"000000012345678","batchNo":"000012","invoiceNo":"000045"}'
```

### Response

HTTP `200` for both create and update. The saved row is under `result`.

```json
{
  "id": "111020261535481234570001",
  "result": {
    "id": 1025,
    "transactionId": "TXN20260926000123",
    "txnDate": "2026-09-26T08:15:00+07:00",
    "txnType": "SALE",
    "txnStatus": "APPROVED",
    "txnAmount": 150000,
    "cardNo": "411111******1111",
    "edcSerial": "SN0001",
    "tid": "70010001",
    "mid": "000000012345678",
    "invoiceNo": "000045",
    "batchNo": "000012",
    "settleId": null,
    "createdAt": "2026-09-26T02:00:00.100Z",
    "updatedAt": "2026-09-26T02:00:00.100Z"
  }
}
```

The example is shortened. The real response has every field of "Get Transaction"; fields you did not send are empty strings.

### Errors

Errors use the standard format described in the API guide (see "Response" → "Errors").

| HTTP | `error.code` | What it means | What you should do |
| --- | --- | --- | --- |
| `400` | `-32602` | Body is not a valid JSON object. | Fix the body. |
| `401` | `-32001` | Token missing or invalid. | Get a new token. |
| `403` | `-32003` | You may not write this model. | Ask an admin for access. |
| `404` | `-32004` | You sent an `id` but no such transaction exists. | Check the ID. |
| `500` | `-32603` | Database or validation error, e.g. no `transactionId` on create, or `txnDate` cannot be read. | Fix the field and retry. |

### Good to know

- **Not idempotent.** Retrying a create adds a second row. After a timeout, search by `transactionId` before retrying.
- Updates are partial. You do not need to send the whole row.
- A later terminal result callback for the same `transactionId` overwrites your manual edits.
- Also available as `POST /base/edc/transaction/upsert` (RPC: `POST /rpc/edc.transaction.journal/upsert`). Send the row in `kwargs`. Rows come back with snake_case keys, and an unknown `id` or other failure is `400` with a plain-text message.
