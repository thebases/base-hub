## Search Transactions

Lists payments (card and QR) from the transaction journal, newest first. Use it to build the admin payment list or to find a payment by terminal or status.

### Before you start

- A Base HUB bearer token.
- Permission to read `edc.transaction.journal`.

### Request

```http
POST /base/transaction/search
Authorization: Bearer <token>
Content-Type: application/json
```

You can put each filter at the top level of the body **or** inside a `filters` object. Both camelCase and snake_case keys work (for example `filters.edc_serial`). The body is optional: an empty body returns the first page with no filter. A non-empty body that is not a JSON object returns `400`.

| Body field | Type | Required | What it means |
| --- | --- | --- | --- |
| `transactionId` | string | No | Partial match, ignores case, e.g. `TXN2026`. Old name `txnId`. |
| `edcSerial` | string | No | Exact terminal serial, e.g. `SN0001`. |
| `txnStatus` | string | No | Exact and case-sensitive: `APPROVED`, `FAILED` or `TIMEOUT`. |
| `cardNo` | string | No | Exact masked card number as stored, e.g. `411111******1111`. |
| `txnType` | string | No | `QR` = every type containing "QR". `CARD` = every non-QR type. Anything else is an exact match ignoring case (`SALE`, `VOID`). Alias `transactionType`. |
| `limit` | integer | No | Page size. Default `50`. A value `<= 0` or `> 500` becomes `50`. |
| `offset` | integer | No | Rows to skip. Default `0`. |

### Example

```bash
curl -X POST "$BASE_URL/base/transaction/search" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"edcSerial":"SN0001","txnType":"CARD","limit":20,"offset":0}'
```

### Response

HTTP `200`. `result` is a list of transactions. There is no total count.

```json
{
  "id": "111020261535481234570001",
  "result": [
    {
      "id": 1024,
      "transactionId": "TXN20260926000123",
      "txnDate": "2026-09-26T08:15:00+07:00",
      "txnType": "SALE",
      "txnStatus": "APPROVED",
      "txnAmount": 150000,
      "cardNo": "411111******1111",
      "edcSerial": "SN0001",
      "tid": "70010001",
      "mid": "000000012345678",
      "batchNo": "000012",
      "invoiceNo": "000045",
      "settleId": null,
      "de39AuthCode": "123456",
      "createdAt": "2026-09-26T01:15:02.481Z",
      "updatedAt": "2026-09-26T01:15:02.481Z"
    }
  ]
}
```

The example is shortened. Each row has the same fields as "Get Transaction" (also `reqId`, `billId`, `edcId`, `aliasValue`, `aid`, `appName`, `stan`, `cardHolder`, `cardType`, `de37RefCode`).

### Errors

Errors use the standard format described in the API guide (see "Response" → "Errors").

| HTTP | `error.code` | What it means | What you should do |
| --- | --- | --- | --- |
| `400` | `-32602` | Body is not a valid JSON object. | Fix the body. |
| `401` | `-32001` | Token missing or invalid. | Get a new token. |
| `403` | `-32003` | You may not read this model. | Ask an admin for access. |
| `500` | `-32603` | Database error. | Retry later. |

### Good to know

- `txnAmount` is an integer in whole units: `150000` = 150,000 VND.
- `settleId` is `null` until the terminal's batch is settled (a **settlement** is the bank's end-of-batch total). Then `isSettled: true` and `settledAt` appear. Voided sales show `isVoid: true` and `voidAt`.
- Sorted by `id`, newest first. No match gives `result: []`.
- A payment missing here? Check `POST /base/transaction/logs/search` for a `FAILED` or `PENDING` callback.
- Also available as `POST /base/edc/transaction/search` (RPC: `POST /rpc/edc.transaction.journal/txn_search`). Send the filters in `kwargs`. Rows come back with snake_case keys.
