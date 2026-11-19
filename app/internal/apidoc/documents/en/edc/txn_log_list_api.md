## List Transaction Logs

Returns a page of raw logs: every request and response exchanged with terminals, plus related MQTT and system events. Newest first. Use it to trace what a terminal really sent for a payment.

### Before you start

- A Base HUB bearer token.
- Permission to read `edc.txn.log`.

### Request

```http
GET /base/txn-logs?limit={limit}&offset={offset}&transactionId={transactionId}&device_id={device_id}&log_type={log_type}&parse_status={parse_status}
Authorization: Bearer <token>
```

| Query parameter | Type | Required | What it means |
| --- | --- | --- | --- |
| `limit` | integer | No | Page size. Default `50`, max `500`. A value `<= 0`, `> 500` or not a number becomes `50`. |
| `offset` | integer | No | Rows to skip. Default `0`. |
| `transactionId` | string | No | Exact match, e.g. `TXN20260926000123`. `transaction_id` also works. |
| `device_id` | string | No | Exact terminal serial, e.g. `SN0001`. |
| `log_type` | string | No | `EDC_INIT_REQUEST`, `EDC_INIT_RESPONSE`, `EDC_RESULT_REQUEST`, `EDC_RESULT_RESPONSE`, `EDC_SETTLE_REQUEST`, `MANAGEMENT_INIT_REQUEST`, `MANAGEMENT_INIT_RESPONSE`, `MQTT_COMMAND`, `MQTT_ACK`, `MQTT_EVENT` or `SYSTEM_EVENT`. |
| `parse_status` | string | No | `PENDING`, `PROCESSING`, `SUCCESS`, `FAILED` or `IGNORED`. |

### Example

```bash
curl "$BASE_URL/base/txn-logs?transactionId=TXN20260926000123&limit=20" \
  -H "Authorization: Bearer $TOKEN"
```

### Response

HTTP `200`.

```json
{
  "id": "111020261535481234570001",
  "result": {
    "items": [
      {
        "id": 88231,
        "deviceId": "SN0001",
        "requestBody": "{\"transactionId\":\"TXN20260926000123\",\"amount\":150000}",
        "response": "{\"code\":\"00\",\"message\":\"OK\"}",
        "transactionId": "TXN20260926000123",
        "type": "SALE",
        "logType": "EDC_RESULT_REQUEST",
        "parseStatus": "SUCCESS",
        "parseAttempts": 1,
        "receivedAt": "2026-09-26T08:04:06Z",
        "processedAt": "2026-09-26T08:04:06Z"
      }
    ],
    "total": 1
  }
}
```

The example is shortened. Each item has the same fields as "Get Transaction Log".

| Field | What it means |
| --- | --- |
| `items` | The page of log entries. |
| `total` | How many rows match your filters. It ignores `limit` and `offset`. |
| `requestBody`, `response` | Raw payloads as strings. Parse them yourself. |
| `parseError` | Why processing failed. Present only when `parseStatus` is `FAILED`. |

### Errors

Errors use the standard format described in the API guide (see "Response" → "Errors").

| HTTP | `error.code` | What it means | What you should do |
| --- | --- | --- | --- |
| `401` | `-32001` | Token missing or invalid. | Get a new token. |
| `403` | `-32003` | You may not read this model. | Ask an admin for access. |
| `500` | `-32603` | Database error. | Retry later. |

### Good to know

- Filter values are case-sensitive. Send `log_type` and `parse_status` in upper-case. An unknown value simply matches nothing.
- Sorted by `receivedAt`, newest first.
- For a partial `transactionId` match or the QR/CARD filter, use "Search Transaction Callback Logs" instead.
