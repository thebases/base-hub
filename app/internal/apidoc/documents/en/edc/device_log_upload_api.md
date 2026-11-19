## Upload Device Log File

A terminal calls this to upload a log file (plain text or an archive such as `.zip` or `.tar.gz`). The server saves the file as an attachment and creates a log entry linked to it, in one step. Admins then see the entry with `GET /base/device/log`.

### Before you start

- This call is made by the terminal, not by a user. It uses the **device token**, not a user token. A user token is rejected.
- The device token is `<serial>:<secretVersion>:<deviceSecret>`. The serial is the number printed on the terminal. The secret and its version are issued to the terminal when it provisions itself (first-time setup).
- The device must be active, provisioned and not revoked.

### Request

```http
POST /base/device/log/upload
Authorization: Bearer <serial>:<secretVersion>:<deviceSecret>
Content-Type: multipart/form-data
```

Example header: `Authorization: Bearer SN0001:1:9f8e7d6c...`.

| Form field | Type | Required | What it means |
| --- | --- | --- | --- |
| `file` | file | Yes | The log file. It must not be empty. Any type is accepted. The extension decides the compression type (`.zip`, `.gz`, `.tar.gz`, `.tgz`, `.bz2`, `.tar.bz2`, `.xz`, `.7z`, `.rar`). |
| `level` | string | No | Log level, e.g. `ERROR`, `WARN`, `INFO`. |
| `tag` | string | No | Free-text category, e.g. `payment`. Defaults to `file`. |
| `bankBincode` | string | No | Bank BIN code the log relates to, e.g. `970436`. |
| `loggedAt` | string | No | When the log was produced, RFC 3339, e.g. `2026-09-26T14:59:30+07:00`. Defaults to the upload time. |
| `metadata` | string | No | A JSON **object** written as text, e.g. `{"appVersion":"1.3.2"}`. Arrays and other values are rejected. |
| `deviceSerial` | string | No | Ignored. The serial always comes from the token. |

The server fills in the rest:

- `message` is always `uploaded device log file <original file name>`.
- `fileExtension` is the lower-case extension with the dot (`.zip`, `.tar.gz`, `.txt`).
- `compressionType` is one of `zip`, `gzip`, `tar.gz`, `tar.bz2`, `bzip2`, `xz`, `7z`, `rar`. It is left out for other files.
- `storedFileName` is `<UTC yyyyMMddTHHmmssZ>_<file name>`. Spaces and `/ \ :` in the name become `_`.
- `contentType` is the file part's `Content-Type`, else guessed from the extension, else `application/octet-stream`.

### Example

```bash
curl -X POST "$BASE_URL/base/device/log/upload" \
  -H "Authorization: Bearer SN0001:1:$DEVICE_SECRET" \
  -F "file=@edc-20260926.zip" \
  -F "level=ERROR" \
  -F "tag=payment" \
  -F "bankBincode=970436" \
  -F "loggedAt=2026-09-26T14:59:30+07:00" \
  -F 'metadata={"appVersion":"1.3.2"}'
```

### Response

`201 Created`. The body is the new log entry. This route is a frozen contract for terminal firmware, so it is **not** wrapped in the `result` envelope.

```json
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
  "createdAt": "2026-09-26T08:00:00Z",
  "severity": "ERROR"
}
```

| Field | What it means |
| --- | --- |
| `id` | Id of the log entry. |
| `storageProvider` / `storageUrl` | Always `"attachment"` / the download path `/web/content/<attachmentId>`. |
| `loggedAt`, `createdAt` | UTC times. |
| `severity` | A copy of `level`. Only this upload response has it. |

Empty `metadata` and unset optional fields are left out.

### Errors

This route keeps its own error body: `{"error": "<code>", "message": "..."}`.

| HTTP | `error` | What it means | What you should do |
| --- | --- | --- | --- |
| `400` | `file_required` | No `file` field, the body is not `multipart/form-data`, or the file is empty. | Send a non-empty file. |
| `400` | `invalid_metadata` | `metadata` is not a JSON object. | Send an object such as `{"a":1}`. |
| `400` | `invalid_logged_at` | `loggedAt` is not RFC 3339. | Use e.g. `2026-09-26T14:59:30+07:00`. |
| `401` | `missing_device_token` | `Authorization` header missing or not `Bearer ...`. | Send the device token. |
| `401` | `invalid_device_token` | Token is malformed, unknown, has a wrong or old secret, or the device is inactive, revoked or not provisioned. | Check the token. After re-provisioning use the new secret version. |
| `500` | `internal_error` | The file could not be stored, or another server error. | Retry later. |

Both `401` codes carry the message `device authentication failed`.

### Good to know

- The serial in the token is matched case-insensitively (`sn0001` works for `SN0001`). The log is saved under the stored serial.
- After the device is re-provisioned, the old secret and version stop working.
- Large archives can be slow. Set a generous client timeout.
