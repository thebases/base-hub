## Get Transaction

Returns one payment from the transaction journal (the admin list of payments). Use it when you already know the numeric `id` from a search result.

### Before you start

- A Base HUB bearer token (`Authorization: Bearer <token>`).
- Permission to read `edc.transaction.journal`.

### Request

```http
GET /base/transaction/{id}
Authorization: Bearer <token>
```

| Path parameter | Type | Required | What it means |
| --- | --- | --- | --- |
| `id` | integer | Yes | Journal ID, e.g. `1024`. This is **not** the terminal's `transactionId` string. |

### Example

```bash
curl "$BASE_URL/base/transaction/1024" \
  -H "Authorization: Bearer $TOKEN"
```

### Response

HTTP `200`. The transaction is under `result`.

```json
{
  "id": "111020261535481234570001",
  "result": {
    "id": 1024,
    "reqId": "TXN20260926000123",
    "billId": "TXN20260926000123",
    "transactionId": "TXN20260926000123",
    "txnDate": "2026-09-26T08:15:00+07:00",
    "txnType": "SALE",
    "txnStatus": "APPROVED",
    "txnAmount": 150000,
    "cardNo": "411111******1111",
    "edcSerial": "SN0001",
    "edcId": "SN0001",
    "tid": "70010001",
    "mid": "000000012345678",
    "aliasValue": "BANKAPP",
    "aid": "A0000000031010",
    "appName": "VISA CREDIT",
    "invoiceNo": "000045",
    "batchNo": "000012",
    "stan": "000321",
    "cardHolder": "NGUYEN VAN A",
    "cardType": "CHIP",
    "settleId": "SN0001:000012:STL20260926000012",
    "de37RefCode": "626908123456",
    "de39AuthCode": "123456",
    "isSettled": true,
    "settledAt": "2026-09-26T15:00:00Z",
    "createdAt": "2026-09-26T01:15:02.481Z",
    "updatedAt": "2026-09-26T08:00:03.120Z"
  }
}
```

| Field | What it means |
| --- | --- |
| `id` | Journal ID (the number you pass in the URL). |
| `transactionId` | ID created by the terminal for this payment. |
| `txnDate`, `txnType`, `txnStatus` | When it happened, the type (`SALE`, `VOID`, `QR`...) and the status (`APPROVED`, `FAILED`, `TIMEOUT`). |
| `txnAmount` | Amount in whole currency units: `150000` = 150,000 VND. |
| `cardNo`, `cardHolder`, `cardType` | Masked card number, holder name, card type. |
| `edcSerial`, `tid`, `mid` | Terminal serial, terminal ID (TID) and merchant ID (MID). |
| `batchNo`, `invoiceNo`, `stan` | Batch number, invoice number, trace number from the receipt. |
| `de37RefCode`, `de39AuthCode` | Bank reference number and approval/response code. |
| `settleId` | The settlement this payment belongs to. `null` until its batch is settled. |
| `isSettled`, `settledAt`, `isVoid`, `voidAt`, `voidJson` | Settle and void marks. Left out when not set. |

### Errors

Errors use the standard format described in the API guide (see "Response" → "Errors").

| HTTP | `error.code` | What it means | What you should do |
| --- | --- | --- | --- |
| `400` | `-32602` | `id` is not an integer. | Send a number. |
| `401` | `-32001` | Token missing or invalid. | Get a new token. |
| `403` | `-32003` | You may not read this model. | Ask an admin for access. |
| `404` | `-32004` | No transaction has this ID. | Search first to find the right ID. |
| `500` | `-32603` | Database error. | Retry later. |

### Good to know

- `id` is the journal row number, not `transactionId`. To find a row by `transactionId`, use "Search Transactions".
