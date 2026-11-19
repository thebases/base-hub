## Get Device Command

Returns one command that was sent to a terminal, including the terminal's acknowledgement and result when they have arrived. Use it to follow a command after you sent it, or to see why it failed.

### Before you start

- You need a bearer token and **read** permission on `edc.device.command`.
- You need the numeric `id` of the command. You get it from "List Device Commands". It is **not** the `commandId` (a UUID).

### Request

```http
GET /base/device/command/{id}
Authorization: Bearer <token>
```

| Name | Type | Required | What it means |
| --- | --- | --- | --- |
| `id` (path) | integer | Yes | Numeric command id, for example `128`. |

### Example

```bash
curl "$BASE_URL/base/device/command/128" \
  -H "Authorization: Bearer $TOKEN"
```

### Response

```json
{
  "id": "111020261535481234570001",
  "result": {
    "id": 128,
    "commandId": "6f1c2b8e-3a4d-4f5e-9b7a-1c2d3e4f5a6b",
    "serialNumber": "SN0001",
    "commandType": "SYNC_CONFIG",
    "commandCode": 200,
    "payload": { "id": "20260926080000" },
    "status": "SUCCESS",
    "mqttTopic": "base/default/SN0001/cmd",
    "publishedAt": 1790409601,
    "ackedAt": 1790409602,
    "completedAt": 1790409609,
    "expiredAt": 1790409660,
    "ackPayload": { "c": 1200, "st": 1, "s": 1790409602, "p": { "deviceId": "SN0001" } },
    "resultPayload": { "c": 1200, "st": 0, "s": 1790409609, "p": { "deviceId": "SN0001" } },
    "createdBy": "7",
    "createdAt": 1790409600,
    "updatedAt": 1790409609
  }
}
```

| Field | What it means |
| --- | --- |
| `id` | Numeric row id. |
| `commandId` | Unique id (UUID) of the command message. The terminal copies it into its reply (field `i`). |
| `serialNumber` | Serial of the target terminal. |
| `commandType` | `START_TRANSACTION` for codes `100`-`199`, `SYNC_CONFIG` for codes `200`-`299`. |
| `commandCode` | The numeric command code that was sent. |
| `payload` | The data sent with the command. |
| `status` | Where the command is in its life: `CREATED`, `PUBLISHED`, `ACKED`, `SUCCESS`, `FAILED`, `TIMEOUT` or `EXPIRED`. See "Device Commands API". |
| `mqttTopic` | The MQTT topic the command was published to. |
| `publishedAt`, `ackedAt`, `completedAt`, `expiredAt` | When it was published, acknowledged, finished, and when it expires. Left out until set. |
| `ackPayload`, `resultPayload` | Copies of the terminal's acknowledgement and final reply (`c` = command code + 1000, `st` = status, `s` = time, optional `rc` reason and `p` data). |
| `errorCode`, `errorMessage` | Why a failed command failed. Left out when empty. |
| `createdBy` | Id of the user who sent the command, as a string. |
| `createdAt`, `updatedAt` | Row times. |

Empty fields are left out of the JSON. Times are Unix seconds (see the API guide).

### Errors

Errors use the standard format described in the API guide (see "Response" → "Errors").

| HTTP | `error.code` | What it means | What you should do |
| --- | --- | --- | --- |
| `400` | `-32602` | `id` is not an integer, for example you passed the `commandId` UUID. | Use the numeric `id`. |
| `401` | `-32001` | Missing or invalid token. | Get a new token. |
| `403` | `-32003` | You have no read access to `edc.device.command`. | Ask an admin. |
| `404` | `-32004` | No command has this `id`. | Check the id. |
| `500` | `-32603` | Unexpected server error. | Retry; report the response `id`. |

### Good to know

- A command that failed carries `errorCode` and `errorMessage`. The reason can be a publish failure, a failure reported by the terminal, or a dropped command (event `c=1899`).
- `mqttTopic` is `base/<group name>/<serial>/cmd`, or `<serial>/cmd` for terminals behind the launcher bridge.
