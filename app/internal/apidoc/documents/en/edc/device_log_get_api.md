## Get Device Log

Returns one log entry that a terminal uploaded, including details about the uploaded file. Use it when you already know the log `id` (for example from the list) and want the full record.

### Before you start

- You need a bearer token (see the API guide) of a user who may read device logs (`edc.device.log`).
- Get the `id` from *List Device Logs* (`GET /base/device/log`).

### Request

```http
GET /base/device/log/{id}
Authorization: Bearer <token>
```

| Name | Type | Required | What it means |
| --- | --- | --- | --- |
| `id` | integer (path) | Yes | The log id, e.g. `311`. |

### Example

```bash
curl "$BASE_URL/base/device/log/311" \
  -H "Authorization: Bearer $TOKEN"
```

### Response

`200 OK`. The log is inside the standard `result` envelope.

```json
{
  "id": "111020261535481234570001",
  "result": {
    "id": 311,
    "deviceSerial": "SN0001",
    "level": "ERROR",
    "tag": "payment",
    "message": "uploaded device log file edc-20260926.zip",
    "metadata": { "appVersion": "1.3.2" },
    "originalFileName": "edc-20260926.zip",
    "storedFileName": "20260926T080000Z_edc-20260926.zip",
    "fileExtension": ".zip",
    "contentType": "application/zip",
    "compressionType": "zip",
    "fileSize": 48213,
    "storageProvider": "attachment",
    "storagePath": "device-logs/SN0001/2026/09/26/20260926T080000Z_edc-20260926.zip",
    "storageUrl": "/web/content/5120",
    "bankBincode": "970436",
    "loggedAt": "2026-09-26T07:59:30Z",
    "createdAt": "2026-09-26T08:00:00Z"
  }
}
```

| Field | What it means |
| --- | --- |
| `deviceSerial` | Serial (the unique number printed on the terminal) that sent the log. |
| `level`, `tag` | Severity (e.g. `ERROR`) and a free-text category (e.g. `payment`). File uploads without a tag get the tag `file`. |
| `message` | Short text. For file uploads it is `uploaded device log file <file name>`. |
| `metadata` | Extra JSON object sent by the terminal. |
| `originalFileName`, `storedFileName`, `fileExtension`, `contentType`, `compressionType`, `fileSize` | Details of the uploaded file. `fileSize` is in bytes. |
| `storageProvider`, `storagePath`, `storageUrl` | Where the file is kept. `storageUrl` is a download path on the Base HUB host. |
| `bankBincode` | Bank BIN code the log relates to, if the terminal sent one. |
| `loggedAt` / `createdAt` | When the terminal produced the log / when the server saved it. |

Empty optional fields are left out. This route has no `severity` key; read `level`. `storageBucket` appears only on rows created with the record call and only if it was set.

> **Check:** timestamps are shown as text above; the API guide says responses use Unix seconds. Confirm the format on your server.

### Errors

Errors use the standard format described in the API guide (see "Response" → "Errors").

| HTTP | `error.code` | What it means | What you should do |
| --- | --- | --- | --- |
| `400` | `-32602` | `id` is not a number. | Send an integer id. |
| `401` | `-32001` | Missing or invalid token. | Get a new token. |
| `403` | `-32003` | Your user cannot read device logs. | Ask an admin for access. |
| `404` | `-32004` | No log has this `id`. | Check the id. |
| `500` | `-32603` | Unexpected server error. | Retry later and report the response `id`. |
