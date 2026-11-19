## Assign Device Config

Chooses which config a terminal uses. Either point it at an active config template version (`configId`), or give it a manual config just for this device (`deviceConfig`). Send exactly one of the two.

### Before you start

- You need a bearer token (see the API guide) of a user who may write `edc.device.status`.
- For a template, you need the id of the **active** version of that template.
- The call also reads the device and config as you at the end, so you need read access to `edc.device` and `edc.device.config`. Without it you get `403` even though the assignment was saved.

### Request

```http
PUT /base/device/status/{serial}/config
Authorization: Bearer <token>
Content-Type: application/json
```

| Name | Type | Required | What it means |
| --- | --- | --- | --- |
| `serial` | string (path) | Yes | Serial of a registered device, e.g. `SN0001`. Exact, case-sensitive. A `serial` in the body is ignored. |
| `configId` | integer | One of the two | Id of a template version, e.g. `14`. It must be the active version (`isActive: true`). `null` counts as not sent. `config_id` is also accepted. |
| `deviceConfig` | object | One of the two | Manual config JSON for this device only. Saved exactly as sent; `{}` is allowed. `null` or a non-object counts as not sent. `device_config` is also accepted. |

### Example

Assign a template:

```bash
curl -X PUT "$BASE_URL/base/device/status/SN0001/config" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"configId": 14}'
```

Set a manual config:

```bash
curl -X PUT "$BASE_URL/base/device/status/SN0001/config" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"deviceConfig": {"owner": "THEBASE", "pingTime": 60, "functions": {"qrFunc": true, "mqttEnabled": true}}}'
```

### Response

`200 OK`. The body is the device's updated status record (same shape as *Get Device Status*), **not** wrapped in `result`.

```json
{
  "id": 7,
  "serial": "SN0001",
  "deviceId": 42,
  "device": {
    "id": 42,
    "serial": "SN0001",
    "ownerId": 10,
    "holderId": 25,
    "groupId": 1,
    "active": true,
    "provisioningStatus": "ACTIVE",
    "secretVersion": 1,
    "createdAt": "2026-09-20T03:00:00Z",
    "updatedAt": "2026-09-20T03:10:00Z"
  },
  "configId": 14,
  "config": {
    "id": 14,
    "name": "default-retail",
    "version": 2,
    "configJson": { "owner": "THEBASE", "pingTime": 30 },
    "isActive": true,
    "createdAt": "2026-09-26T09:00:00Z"
  },
  "deviceConfig": { "owner": "THEBASE", "pingTime": 30 },
  "status": "ONLINE",
  "charging": false,
  "lastSeenAt": "2026-09-26T09:14:55Z",
  "createdAt": "2026-09-20T03:00:00Z",
  "updatedAt": "2026-09-26T09:20:00Z"
}
```

With a manual config, `configId` and `config` are absent and `deviceConfig` is your object.

### Errors

Errors use the standard format described in the API guide (see "Response" → "Errors").

| HTTP | `error.code` | What it means | What you should do |
| --- | --- | --- | --- |
| `400` | `-32700` | The body is not valid JSON. | Fix the JSON. |
| `400` | `-32602` | You sent both `configId` and `deviceConfig`, or neither, or `configId` is unknown or not the active version. | Send exactly one, and use an active version. |
| `401` | `-32001` | Missing or invalid token. | Get a new token. |
| `403` | `-32003` | You lack write access to `edc.device.status` (or read access to the device or config). | Ask an admin for access. |
| `404` | `-32004` | No device is registered with this serial. | Check the serial. |
| `500` | `-32603` | Unexpected server or database error. | Retry later and report the response `id`. |

### Good to know

- **Template:** the device follows that template. Its config is copied, and `cardConfig.tidmid` is rebuilt from the template's linked bank apps (when it has any). When a newer version of the same template name is activated later, the device moves to it automatically.
- **Manual config:** the device stops following any template. Send the complete object because no defaults are added.
- This call only saves the assignment. It does **not** send a command to the terminal. `owner`, `state` and `mqttConfig.mqttDeviceTopic` are added when the config is read, so do not send them.
- It works for a registered device that never reported status. Its status record is created as `OFFLINE`.
- Also available as `POST /base/edc/device/status/assign-config` and `POST /rpc/edc.device.status/assign_config` (`kwargs`: `serial`, `configId` or `config_id`, `deviceConfig` or `device_config`). They return `{"result": <status row>}`.
