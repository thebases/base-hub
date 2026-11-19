## Search Settlements

Lists settlements, newest first. A **settlement** is the bank's end-of-batch total for one terminal batch (how many sales, how many voids, how much money). One row is created each time a terminal sends "EDC Batch Settle".

### Before you start

- A Base HUB bearer token.
- Permission to read `edc.settlement.journal`.

### Request

```http
POST /base/settlement/search
Authorization: Bearer <token>
Content-Type: application/json
```

Filters go at the top level of the body **or** inside a `filters` object (camelCase, or snake_case `settle_id` / `batch_no`). The body is optional. A non-empty body that is not a JSON object returns `400`.

| Body field | Type | Required | What it means |
| --- | --- | --- | --- |
| `settleId` | string | No | Partial match, ignores case. `SN0001` finds every batch of that terminal. |
| `batchNo` | string | No | Exact batch number, e.g. `000012`. |
| `tid` | string | No | Exact terminal ID, e.g. `70010001`. |
| `mid` | string | No | Exact merchant ID, e.g. `000000012345678`. |
| `limit` | integer | No | Default `50`. A value `<= 0` or `> 500` becomes `50`. |
| `offset` | integer | No | Default `0`. |

### Example

```bash
curl -X POST "$BASE_URL/base/settlement/search" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"settleId":"SN0001","limit":20}'
```

### Response

HTTP `200`. Note the capital letters in `Total` and `Settlements`.

```json
{
  "id": "111020261535481234570001",
  "result": {
    "Total": 1,
    "Settlements": [
      {
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
    ]
  }
}
```

| Field | What it means |
| --- | --- |
| `Total` | How many rows match your filters. It ignores `limit` and `offset`. |
| `Settlements` | The page of settlements. Fields are explained in "Get Settlement". |

### Errors

Errors use the standard format described in the API guide (see "Response" → "Errors").

| HTTP | `error.code` | What it means | What you should do |
| --- | --- | --- | --- |
| `400` | `-32602` | Body is not a valid JSON object. | Fix the body. |
| `401` | `-32001` | Token missing or invalid. | Get a new token. |
| `403` | `-32003` | You may not read this model. | Ask an admin for access. |
| `500` | `-32603` | Database error. | Retry later. |

### Good to know

- Amounts are integers in whole currency units (VND). `txnTotal` and `txnQuant` are the bank's totals as the terminal reported them. The server does not recalculate them from transactions.
- `de39AuthCode` is the bank's settle response code (`00` = success).
- To list the payments in a batch, search transactions and match `settleId` or `batchNo`.
- Sorted by `id`, newest first.
- Reconciliation fields (`edcSerial`, `reconcileStatus`, computed totals) are not in this response. Read them through `/rpc` on `edc.settlement.journal`.
- Also available as `POST /base/edc/settlement/search` (RPC: `POST /rpc/edc.settlement.journal/settle_search`). Send the filters in `kwargs`. The reply uses lowercase `total` / `settlements` and snake_case rows.
