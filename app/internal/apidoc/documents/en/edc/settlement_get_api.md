## Get Settlement

Returns one settlement by its numeric ID. A **settlement** is the bank's end-of-batch total for one terminal: "in batch `000012`, N payments and this much money are finalised". Use it when you know the `id` from settlement search.

### Before you start

- A Base HUB bearer token.
- Permission to read `edc.settlement.journal`.

### Request

```http
GET /base/settlement/{id}
Authorization: Bearer <token>
```

| Path parameter | Type | Required | What it means |
| --- | --- | --- | --- |
| `id` | integer | Yes | Settlement ID, e.g. `88`. Not the `settleId` string. |

### Example

```bash
curl "$BASE_URL/base/settlement/88" \
  -H "Authorization: Bearer $TOKEN"
```

### Response

HTTP `200`. The settlement is under `result`.

```json
{
  "id": "111020261535481234570001",
  "result": {
    "id": 88,
    "reqId": "STL20260926000012",
    "billId": "000012",
    "settleId": "SN0001:000012:STL20260926000012",
    "settleDate": "2026-09-26T15:00:00Z",
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
    "de37RefCode": "",
    "de39AuthCode": "00",
    "createdAt": "2026-09-26T15:00:01.310Z",
    "updatedAt": "2026-09-26T15:00:01.310Z"
  }
}
```

| Field | What it means |
| --- | --- |
| `settleId` | Unique key: `<serial>:<batchNo>:<settle transactionId>`. |
| `settleDate`, `batchNo` | When the batch was closed and its batch number. |
| `txnQuant`, `txnTotal` | Net count and net amount (sales minus voids) as the bank reported. |
| `saleQuant`, `saleAmount` | Number and total of sales. |
| `voidQuant`, `voidAmount` | Number and total of voids. |
| `tid`, `mid`, `aliasValue` | Terminal ID, merchant ID, acquirer or merchant name. |
| `de39AuthCode` | The bank's settle response code. `00` means success. |

Amounts are integers in whole currency units (VND).

### Errors

Errors use the standard format described in the API guide (see "Response" → "Errors").

| HTTP | `error.code` | What it means | What you should do |
| --- | --- | --- | --- |
| `400` | `-32602` | `id` is not an integer. | Send a number. |
| `401` | `-32001` | Token missing or invalid. | Get a new token. |
| `403` | `-32003` | You may not read this model. | Ask an admin for access. |
| `404` | `-32004` | No settlement has this ID. | Search first to find the ID. |
| `500` | `-32603` | Database error. | Retry later. |

### Good to know

- The settled transactions are not included. Search transactions by `batchNo` or `settleId`.
- Reconciliation fields (`edcSerial`, `reconcileStatus`, computed totals) are not in this response. Read them through `/rpc` on `edc.settlement.journal`.
