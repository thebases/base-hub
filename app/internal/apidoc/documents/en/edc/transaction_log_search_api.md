## Search Transaction Callback Logs

Lists the raw calls the terminals made to the server (`init`, `result`, `settle`), newest first. Use it to check exactly what a terminal sent and whether the server processed it.

### Before you start

- A Base HUB bearer token.
- Permission to read `edc.txn.log`.

### Request

```http
POST /base/transaction/logs/search
Authorization: Bearer <token>
Content-Type: application/json
```

Filters go at the top level of the body **or** inside a `filters` object (camelCase or snake_case). The body is optional. A non-empty body that is not a JSON object returns `400`.

| Body field | Type | Required | What it means |
| --- | --- | --- | --- |
| `transactionId` | string | No | Partial match, ignores case, e.g. `TXN2026`. |
| `logType` | string | No | Exact: `EDC_INIT_REQUEST`, `EDC_INIT_RESPONSE`, `EDC_RESULT_REQUEST`, `EDC_RESULT_RESPONSE`, `EDC_SETTLE_REQUEST`, `MANAGEMENT_INIT_REQUEST`, `MANAGEMENT_INIT_RESPONSE`, `MQTT_COMMAND`, `MQTT_ACK`, `MQTT_EVENT`, `SYSTEM_EVENT`. |
| `parseStatus` | string | No | Exact: `PENDING`, `PROCESSING`, `SUCCESS`, `FAILED`, `IGNORED`. |
| `deviceId` | string | No | Exact terminal serial, e.g. `SN0001` (`device_id` also works). |
| `type` | string | No | `QR`, `CARD`, or an exact type like `SALE` (ignores case). Same rules as transaction search. Aliases `txnType`, `transactionType`. |
| `limit` | integer | No | Default `50`. A value `<= 0` or `> 500` becomes `50`. |
| `offset` | integer | No | Default `0`. |

### Example

```bash
curl -X POST "$BASE_URL/base/transaction/logs/search" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"transactionId":"TXN20260926000123","logType":"EDC_RESULT_REQUEST"}'
```

### Response

HTTP `200`. `result` is a list of log rows.

```json
{
  "id": "111020261535481234570001",
  "result": [
    {
      "id": 5531,
      "appVersion": "2.4.1",
      "bankApp": "BANKAPP",
      "deviceId": "SN0001",
      "gps": "21.0285,105.8542",
      "requestBody": "{\"transactionID\":\"TXN20260926000123\",\"transactionAmount\":\"150000\",\"currencyName\":\"VND\",\"tranxType\":\"SALE\"}",
      "response": "{\"Code\":\"200\",\"Desc\":\"Success\",\"tranxType\":\"SALE\",\"Response\":{\"isoResponseCode\":\"00\",\"totalAmount\":\"150000\"}}",
      "transactionId": "TXN20260926000123",
      "transactionTime": "2026-09-26T01:15:00Z",
      "type": "SALE",
      "logType": "EDC_RESULT_REQUEST",
      "idempotencyKey": "result:SN0001:SALE:TXN20260926000123",
      "parseStatus": "SUCCESS",
      "parseAttempts": 1,
      "sourceIp": "10.0.4.17",
      "userAgent": "okhttp/4.12.0",
      "receivedAt": "2026-09-26T01:15:02.401Z",
      "processedAt": "2026-09-26T01:15:02.470Z"
    }
  ]
}
```

| Field | What it means |
| --- | --- |
| `requestBody`, `response` | The terminal's raw JSON strings, exactly as received. Parse them yourself. |
| `logType` | Which call this row records (see the filter list). |
| `parseStatus` | Did the server apply it? See "Good to know". |
| `parseAttempts` | How many times the server tried to process it. |
| `receivedAt`, `processedAt` | When it arrived and when it was processed. |

### Errors

Errors use the standard format described in the API guide (see "Response" → "Errors").

| HTTP | `error.code` | What it means | What you should do |
| --- | --- | --- | --- |
| `400` | `-32602` | Body is not a valid JSON object. | Fix the body. |
| `401` | `-32001` | Token missing or invalid. | Get a new token. |
| `403` | `-32003` | You may not read this model. | Ask an admin for access. |
| `500` | `-32603` | Database error. | Retry later. |

### Good to know

- Sorted by `receivedAt`, newest first. No match gives `[]`. No total count is returned.
- `parseStatus`: `SUCCESS` = applied to the transaction or settlement. `PENDING` = waiting to be applied (also a void waiting for its sale). `FAILED` = could not be applied; see `parseError`. `IGNORED` = audit only (init calls, state-change events, or a void or settle the bank did not approve).
- Result and settle calls are processed in the same database transaction as the call, so you normally see `SUCCESS`, `FAILED` or `IGNORED` at once. A `PENDING` log is retried automatically (up to 10 times, then `FAILED`). An operator can re-queue a `FAILED` one with "Requeue Failed Transaction Log".
- Also available as `POST /base/edc/transaction/logs/search` (RPC: `POST /rpc/edc.txn.log/log_search`). Send the filters in `kwargs`. Rows come back with snake_case keys (`request_body`, `parse_status`).
