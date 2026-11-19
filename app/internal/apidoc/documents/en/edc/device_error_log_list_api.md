## List Device Error Logs

Returns a page of errors reported by terminals (event c=1899 `EVT_DEVICE_ERROR`, sent over MQTT on the `base/evt` topic), newest first. Use it to watch device health by `errorCode`, or to find out why a remote command stayed `PENDING` and never gave a result (match on `requestId`).

### Before you start

- You need a bearer token (see the API guide).
- Your user needs access to `edc.device.error.log`. Right now no permission row gives this to normal users, so only a superadmin can read it until one is added.

### Request

```http
GET /base/device-error-logs?serial={serial}&error_code={errorCode}&request_id={requestId}&txn_id={txnId}&limit={limit}&offset={offset}
Authorization: Bearer <token>
```

| Name | Type | Required | What it means |
| --- | --- | --- | --- |
| `serial` | string | No | Part of the serial, not case-sensitive, e.g. `A9101`. |
| `error_code` | string | No | Part of the error code, not case-sensitive. `REMOTE_TXN_` finds the whole remote-command family. |
| `request_id` | string | No | Exact id of the original remote command, e.g. `req-20260926-0001`. |
| `txn_id` | string | No | Exact transaction id from the terminal app, e.g. `20260926101530`. |
| `limit` | integer | No | Page size, e.g. `20`. Default `50`, maximum `500`. Zero, above 500 or non-numeric falls back to `50`. |
| `offset` | integer | No | Rows to skip. Default `0`. |

Blank filters are ignored. Rows are sorted by `occurredAt` (newest first), then `id`.

### Example

```bash
curl "$BASE_URL/base/device-error-logs?serial=A910123456&error_code=REMOTE_TXN_&limit=20" \
  -H "Authorization: Bearer $TOKEN"
```

### Response

`200 OK`.

```json
{
  "id": "111020261535481234570001",
  "result": {
    "items": [
      {
        "id": 512,
        "serialNumber": "A910123456",
        "errorCode": "REMOTE_TXN_PENDING_EXPIRED",
        "errorMsg": "c=100 got no terminal result within 30 min",
        "requestId": "req-20260926-0001",
        "txnId": "20260926101530",
        "appVersion": "2.3.0",
        "occurredAt": "2026-09-26T08:05:45.678Z",
        "sentAt": "2026-09-26T08:05:45Z",
        "payload": {
          "serialNumber": "A910123456",
          "errorCode": "REMOTE_TXN_PENDING_EXPIRED",
          "errorMsg": "c=100 got no terminal result within 30 min",
          "requestId": "req-20260926-0001",
          "txnId": "20260926101530",
          "appVersion": "2.3.0",
          "occurredAt": 1790412345678,
          "deviceId": "A910123456"
        },
        "createdAt": "2026-09-26T08:05:46Z"
      }
    ],
    "total": 1
  }
}
```

| Field | What it means |
| --- | --- |
| `items` | The errors on this page. Each item has the same fields as *Get Device Error Log*. |
| `total` | How many errors match your filters in all (ignores `limit` and `offset`). |

`requestId`, `txnId`, `appVersion` and `sentAt` are left out when the terminal did not send them.

### Errors

Errors use the standard format described in the API guide (see "Response" → "Errors").

| HTTP | `error.code` | What it means | What you should do |
| --- | --- | --- | --- |
| `401` | `-32001` | Missing or invalid token. | Get a new token. |
| `403` | `-32003` | Your user has no access to `edc.device.error.log`. | Ask an admin for access. |
| `500` | `-32603` | Unexpected server error. | Retry later and report the response `id`. |

### Good to know

- `occurredAt` is when the error happened on the terminal. It can be earlier than `sentAt`: errors raised before MQTT connects (for example at startup) are held and sent after the next c=1800.
- The terminal sends the same `errorCode` and `errorMsg` at most once per 30 seconds, so this list is not a count of every occurrence. The full history stays in the device log file.
- When an error carries a `requestId` that matches a still-open command of that device, the server also marks that command `FAILED` (see `GET /base/device/command/{id}`, fields `errorCode` and `errorMessage`).
- Delivery is best-effort, at most once. A missing row does not prove that no error happened.
