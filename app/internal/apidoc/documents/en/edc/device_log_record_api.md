## Record Device Log (internal)

Creates one device log row from values you already have. Use it from workflows, automations or other server-side code. It does not receive files; terminals upload files with `POST /base/device/log/upload`.

### Before you start

- You need a bearer token (see the API guide) of a user who may write device logs (`edc.device.log`).
- Device tokens are not accepted here, only user tokens.

### Request

```http
POST /base/edc/device/log/record
Authorization: Bearer <token>
Content-Type: application/json
```

This is the same as the RPC call `POST /rpc/edc.device.log/record`. The body has `args` (leave it `[]`) and `kwargs` (the values below, in snake_case).

| `kwargs` key | Type | Required | What it means |
| --- | --- | --- | --- |
| `device_serial` | string | Yes | Serial the log belongs to, e.g. `SN0001`. Spaces are trimmed. It is not checked against the device list. |
| `message` | string | Conditional | Log text, e.g. `reader timeout`. Required unless `original_file_name` is set. Then it defaults to `uploaded device log file <original_file_name>`. |
| `level` | string | No | Log level, e.g. `WARN`. Blank is not stored. |
| `tag` | string | No | Category, e.g. `payment`. Defaults to `file` when `original_file_name` is set. |
| `metadata` | object | No | Any JSON object. Other types are stored as `{}`. |
| `logged_at` | string or integer | No | When it happened: RFC 3339 text or Unix seconds. Defaults to now (UTC). A value that cannot be read is silently ignored. |
| `original_file_name`, `stored_file_name`, `file_extension`, `content_type`, `compression_type`, `storage_provider`, `storage_bucket`, `storage_path`, `storage_url`, `bank_bincode` | string | No | Details about a file. They are stored as given and nothing is checked or moved. |
| `file_size` | integer | No | File size in bytes. A numeric string is accepted; other values are ignored. |

### Example

```bash
curl -X POST "$BASE_URL/base/edc/device/log/record" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"args": [], "kwargs": {"device_serial": "SN0001", "level": "WARN", "tag": "payment", "message": "reader timeout", "metadata": {"appVersion": "1.3.2"}}}'
```

### Response

`200 OK`. `result` is the new row with snake_case field names.

```json
{
  "id": "111020261535481234570001",
  "result": {
    "id": 312,
    "device_serial": "SN0001",
    "level": "WARN",
    "tag": "payment",
    "message": "reader timeout",
    "metadata": { "appVersion": "1.3.2" },
    "logged_at": "2026-10-11T08:00:00Z"
  }
}
```

The row may also include audit fields such as `created_at`.

### Errors

Errors use the standard format described in the API guide (see "Response" → "Errors").

| HTTP | `error.code` | What it means | What you should do |
| --- | --- | --- | --- |
| `400` | `-32602` | `device_serial` is blank (`device_serial is required`). | Send the serial. |
| `400` | `-32602` | No `message` and no `original_file_name`. | Send one of them. |
| `400` | `-32000` | The row could not be created. | Read `message`. |
| `401` | `-32001` | Missing or invalid token. | Get a new token. |
| `403` | `-32003` | Your user cannot write device logs. | Ask an admin for access. |

### Good to know

- Not idempotent: every call creates a new row. Do not retry blindly.
- The file fields are only references. Nothing is uploaded or checked.
