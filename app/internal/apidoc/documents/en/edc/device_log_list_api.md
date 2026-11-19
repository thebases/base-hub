## List Device Logs

Returns a page of log entries uploaded by terminals, newest first. Use it for a device log screen or to find the log files of one terminal.

### Before you start

You need a bearer token (see the API guide) of a user who may read device logs (`edc.device.log`).

### Request

```http
GET /base/device/log?device_serial={serial}&level={level}&tag={tag}&limit={limit}&offset={offset}
Authorization: Bearer <token>
```

| Name | Type | Required | What it means |
| --- | --- | --- | --- |
| `device_serial` | string | No | Part of the serial, not case-sensitive, e.g. `sn00`. Note the snake_case name. |
| `level` | string | No | Exact log level, case-sensitive, e.g. `ERROR`. |
| `tag` | string | No | Part of the tag, not case-sensitive, e.g. `payment`. Uploaded files with no tag use `file`. |
| `severity` | string | No | Old name of `level`. Used only when `level` is empty. |
| `limit` | integer | No | Page size, e.g. `20`. Default `50`, maximum `500`. Zero, above 500 or non-numeric falls back to `50`. |
| `offset` | integer | No | Rows to skip, e.g. `40`. Default `0`. |

Blank filters are ignored. Rows are sorted by `loggedAt` (newest first), then `createdAt`.

### Example

```bash
curl "$BASE_URL/base/device/log?device_serial=SN0001&level=ERROR&limit=20" \
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
    ],
    "total": 1
  }
}
```

| Field | What it means |
| --- | --- |
| `items` | The logs on this page. Each item has the same fields as *Get Device Log*. |
| `total` | How many logs match your filters in all (ignores `limit` and `offset`). |

Empty optional fields are left out. Items have no `severity` key; read `level`.

### Errors

Errors use the standard format described in the API guide (see "Response" → "Errors").

| HTTP | `error.code` | What it means | What you should do |
| --- | --- | --- | --- |
| `401` | `-32001` | Missing or invalid token. | Get a new token. |
| `403` | `-32003` | Your user cannot read device logs. | Ask an admin for access. |
| `500` | `-32603` | Unexpected server error. | Retry later and report the response `id`. |

### Good to know

- Page through the results with `offset = (page - 1) * limit` and stop when `offset >= total`.
- Files are stored as Base HUB attachments (`storageProvider: "attachment"`). Download one from `storageUrl`, relative to the Base HUB host.
- `storagePath` is only a label, not a real path on disk.
