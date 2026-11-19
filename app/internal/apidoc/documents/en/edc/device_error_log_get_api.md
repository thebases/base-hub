## Get Device Error Log

Returns one error that a terminal reported (event c=1899), including the full original payload. Use it when you already have the error log `id` from the list.

### Before you start

- You need a bearer token (see the API guide).
- Your user needs access to `edc.device.error.log`. Right now no permission row gives this to normal users, so only a superadmin can read it until one is added.

### Request

```http
GET /base/device-error-logs/{id}
Authorization: Bearer <token>
```

| Name | Type | Required | What it means |
| --- | --- | --- | --- |
| `id` | integer (path) | Yes | The error log id, e.g. `512`. |

### Example

```bash
curl "$BASE_URL/base/device-error-logs/512" \
  -H "Authorization: Bearer $TOKEN"
```

### Response

`200 OK`. The error log is inside the standard `result` envelope.

```json
{
  "id": "111020261535481234570001",
  "result": {
    "id": 512,
    "serialNumber": "A910123456",
    "errorCode": "REMOTE_CMD_DROPPED",
    "errorMsg": "Inbound MQTT payload dropped: not a parseable EdcMessage (137 chars)",
    "appVersion": "2.3.0",
    "occurredAt": "2026-09-26T08:05:45.678Z",
    "sentAt": "2026-09-26T08:05:45Z",
    "payload": {
      "serialNumber": "A910123456",
      "errorCode": "REMOTE_CMD_DROPPED",
      "errorMsg": "Inbound MQTT payload dropped: not a parseable EdcMessage (137 chars)",
      "appVersion": "2.3.0",
      "occurredAt": 1790412345678,
      "deviceId": "A910123456"
    },
    "createdAt": "2026-09-26T08:05:46Z"
  }
}
```

| Field | What it means |
| --- | --- |
| `serialNumber` | Serial of the terminal. It always comes from the message envelope, never from the payload. |
| `errorCode`, `errorMsg` | Short error code and a readable message. |
| `requestId` | Id of the remote command that this error belongs to, if any. Left out when not sent. |
| `txnId` | Transaction id on the terminal, if any. Left out when not sent. |
| `appVersion` | App version on the terminal. Left out when not sent. |
| `occurredAt` / `sentAt` / `createdAt` | When it happened on the terminal / when the terminal sent it / when the server saved it. |
| `payload` | The original event exactly as the terminal sent it, including fields from newer app versions. |

The log is append-only: there is no way to edit or delete it.

> **Check:** timestamps are shown as text above; the API guide says responses use Unix seconds. Confirm the format on your server.

### Errors

Errors use the standard format described in the API guide (see "Response" → "Errors").

| HTTP | `error.code` | What it means | What you should do |
| --- | --- | --- | --- |
| `400` | `-32602` | `id` is not a number. | Send an integer id. |
| `401` | `-32001` | Missing or invalid token. | Get a new token. |
| `403` | `-32003` | Your user has no access to `edc.device.error.log`. | Ask an admin for access. |
| `404` | `-32004` | No error log has this `id`. | Check the id. |
| `500` | `-32603` | Unexpected server error. | Retry later and report the response `id`. |
