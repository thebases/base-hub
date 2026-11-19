## Get Transaction Log

Returns one raw callback log entry by its numeric ID. Use it to see exactly what a terminal sent and how the server handled it.

### Before you start

- A Base HUB bearer token.
- Permission to read `edc.txn.log`.
- The log `id` from the list or search result (or the `logId` returned by a terminal callback).

### Request

```http
GET /base/txn-logs/{id}
Authorization: Bearer <token>
```

| Path parameter | Type | Required | What it means |
| --- | --- | --- | --- |
| `id` | integer | Yes | Log ID, e.g. `88231`. Not a UUID and not a transaction ID. |

### Example

```bash
curl "$BASE_URL/base/txn-logs/88231" \
  -H "Authorization: Bearer $TOKEN"
```

### Response

HTTP `200`. The entry is under `result`.

```json
{
  "id": "111020261535481234570001",
  "result": {
    "id": 88231,
    "appVersion": "1.3.0",
    "bankApp": "VCB",
    "deviceId": "SN0001",
    "gps": "10.7769,106.7009",
    "requestBody": "{\"transactionId\":\"TXN20260926000123\",\"amount\":150000}",
    "response": "{\"code\":\"00\",\"message\":\"OK\"}",
    "transactionId": "TXN20260926000123",
    "transactionTime": "2026-09-26 15:04:05",
    "type": "SALE",
    "logType": "EDC_RESULT_REQUEST",
    "idempotencyKey": "SN0001-TXN20260926000123-RESULT",
    "parseStatus": "SUCCESS",
    "parseAttempts": 1,
    "sourceIp": "10.20.1.15",
    "userAgent": "okhttp/4.12.0",
    "receivedAt": "2026-09-26T08:04:06Z",
    "processedAt": "2026-09-26T08:04:06Z"
  }
}
```

| Field | What it means |
| --- | --- |
| `requestBody`, `response` | The raw payloads as strings. Parse them yourself. |
| `logType` | Which call this is: `EDC_INIT_REQUEST`, `EDC_RESULT_REQUEST`, `EDC_SETTLE_REQUEST`, `MQTT_COMMAND`, ... |
| `parseStatus` | `PENDING`, `PROCESSING`, `SUCCESS`, `FAILED` or `IGNORED`. |
| `parseError` | Why processing failed. Only present when `parseStatus` is `FAILED`. |

Empty optional fields (`idempotencyKey`, `parseError`, `sourceIp`, `userAgent`, `lockedAt`, `processedAt`) are left out.

### Errors

Errors use the standard format described in the API guide (see "Response" → "Errors").

| HTTP | `error.code` | What it means | What you should do |
| --- | --- | --- | --- |
| `400` | `-32602` | `id` is not an integer. | Send a number. |
| `401` | `-32001` | Token missing or invalid. | Get a new token. |
| `403` | `-32003` | You may not read this model. | Ask an admin for access. |
| `404` | `-32004` | No log has this ID. | Check the ID. |
| `500` | `-32603` | Database error. | Retry later. |

### Good to know

- To find logs by transaction ID, use `GET /base/txn-logs?transactionId=...`.
