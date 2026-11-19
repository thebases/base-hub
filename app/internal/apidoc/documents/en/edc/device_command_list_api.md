## List Device Commands

Returns a page of the commands that were sent to terminals, newest first. Use it for a command history screen, or to check what happened to the commands of one terminal.

### Before you start

You need a bearer token and **read** permission on `edc.device.command`.

### Request

```http
GET /base/device/command?serial={serial}&status={status}&commandType={commandType}&limit={limit}&offset={offset}
Authorization: Bearer <token>
```

All parameters are optional.

| Name | Type | Required | What it means |
| --- | --- | --- | --- |
| `serial` | string | No | Terminal serial (the unique number printed on the terminal). Partial, case-insensitive match: `SN00` matches `SN0001`. `serial_number` also works. |
| `status` | string | No | Exact match, upper-case: `CREATED`, `PUBLISHED`, `ACKED`, `SUCCESS`, `FAILED`, `TIMEOUT`, `EXPIRED`. |
| `commandType` | string | No | Exact match: `START_TRANSACTION` (codes `100`-`199`) or `SYNC_CONFIG` (codes `200`-`299`). `command_type` also works. The model also lists `PUSH_CONFIG`, `REBOOT`, `OTA_UPDATE`, `PRINT_TEST`, `LOCK_DEVICE`, `UNLOCK_DEVICE` and `CUSTOM`, but the backend does not create them today. |
| `limit` | integer | No | Page size. Default `50`, maximum `500`. A value above 500, zero or non-numeric falls back to `50`. (`__limit` also works.) |
| `offset` | integer | No | How many rows to skip. Default `0`. (`__offset` also works.) |

A blank value is ignored.

### Example

```bash
curl "$BASE_URL/base/device/command?serial=SN0001&status=PUBLISHED&limit=20" \
  -H "Authorization: Bearer $TOKEN"
```

### Response

```json
{
  "id": "111020261535481234570001",
  "result": {
    "items": [
      {
        "id": 128,
        "commandId": "6f1c2b8e-3a4d-4f5e-9b7a-1c2d3e4f5a6b",
        "serialNumber": "SN0001",
        "commandType": "SYNC_CONFIG",
        "commandCode": 200,
        "payload": { "id": "20260926080000" },
        "status": "PUBLISHED",
        "mqttTopic": "base/default/SN0001/cmd",
        "publishedAt": 1790409601,
        "expiredAt": 1790409660,
        "createdBy": "7",
        "createdAt": 1790409600,
        "updatedAt": 1790409601
      }
    ],
    "total": 1
  }
}
```

| Field | What it means |
| --- | --- |
| `result.items` | The commands of this page, newest first. Each item has the same fields as "Get Device Command". Empty fields are left out. |
| `result.total` | How many commands match your filters in total (ignores `limit` and `offset`). |

### Errors

Errors use the standard format described in the API guide (see "Response" → "Errors").

| HTTP | `error.code` | What it means | What you should do |
| --- | --- | --- | --- |
| `401` | `-32001` | Missing or invalid token. | Get a new token. |
| `403` | `-32003` | You have no read access to `edc.device.command`. | Ask an admin. |
| `500` | `-32603` | Unexpected server error. | Retry; report the response `id`. |

### Good to know

- An unknown `status` or `commandType` is not an error. You just get an empty list (these two are exact, case-sensitive matches; only `serial` is partial).
- To find out why a command failed, filter `status=FAILED`, then read `errorCode` and `errorMessage` on each item.
- To send a new command, use `POST /base/devices/{serial}/commands` (see "Device Commands API").
- Also available as `POST /base/edc/device/command/list` (RPC `POST /rpc/edc.device.command/list_commands`). Send the filters in `kwargs` (`serial`, `status`, `command_type`, `limit`, `offset`). The items use snake_case field names. A caller without read access gets `{"result": null}` instead of `403`, and a failure returns `400` with a plain-text message.
