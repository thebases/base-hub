## Get Resolved Device Config

Returns only the final config JSON that a terminal receives: a copy of its assigned template, or the manual config set for it. Use it to answer "what settings does SN0001 have right now?".

### Before you start

- You need a bearer token (see the API guide).
- This method is not marked read-only, so the permission check is for **write** access to `edc.device.status`, not read. A read-only user gets `403`.

### Request

```http
GET /base/device/status/{serial}/config
Authorization: Bearer <token>
```

| Name | Type | Required | What it means |
| --- | --- | --- | --- |
| `serial` | string (path) | Yes | Serial, e.g. `SN0001`. Exact match, case-sensitive. |

### Example

```bash
curl "$BASE_URL/base/device/status/SN0001/config" \
  -H "Authorization: Bearer $TOKEN"
```

### Response

`200 OK`. The config is inside the standard `result` envelope.

```json
{
  "id": "111020261535481234570001",
  "result": {
    "owner": "10",
    "baseId": "B001",
    "pingTime": 30,
    "bypassSettleWhenUpdate": false,
    "state": "ACTIVE",
    "ignoreAppBankErrorCodes": [],
    "appConfig": { "appPath": "https://cdn.example.com/edc/app-1.4.2.apk", "versionCode": 142, "forceInstall": false },
    "functions": { "qrFunc": true, "cardFunc": true, "voidFunc": true, "mqttEnabled": true },
    "qrConfig": { "bankAccount": "0123456789", "bankBincode": "970418", "qrTimeInterval": 180 },
    "mqttConfig": { "mqttDomain": "mqtt.example.com", "mqttPort": "8883", "mqttTls": true, "mqttDeviceTopic": "base/default/SN0001/cmd" },
    "merchantInfo": { "mmName": "ABC Coffee", "storeName": "ABC Coffee Q1" }
  }
}
```

The example is shortened. The keys are whatever was assigned to the device, plus three keys the server adds on every read (they override the stored value):

| Key | Value |
| --- | --- |
| `owner` | The device's `ownerId` as a string. |
| `state` | The device's provisioning status, e.g. `ACTIVE`. |
| `mqttConfig.mqttDeviceTopic` | The MQTT topic (the channel name) for commands to this device: `base/<group name>/<serial>/cmd`. A device without a group uses the default group name. |

A device with no stored config returns only these three keys.

### Errors

Errors use the standard format described in the API guide (see "Response" → "Errors").

| HTTP | `error.code` | What it means | What you should do |
| --- | --- | --- | --- |
| `401` | `-32001` | Missing or invalid token. | Get a new token. |
| `403` | `-32003` | Your user has no write access to `edc.device.status`. | Ask an admin for access. |
| `404` | `-32004` | No status record for this serial. | Check the spelling. Serials are case-sensitive here. |
| `500` | `-32603` | Unexpected server error. | Retry later and report the response `id`. |

### Good to know

- A manual config is returned as saved. Defaults are not filled in (template configs do get defaults).
- `appConfig.versionName` is dropped if an old stored config still has it.
- The terminal itself reads the same JSON from `GET /base/devices/config/{serial}` (see *Get Device Config* below). That route is not case-sensitive.

---

## Get Device Config

Returns the config JSON currently assigned to a device. The terminal calls this route to fetch its own settings. Admin tools can use it to see exactly what a device receives.

### Before you start

- This route is called by the terminal, not by a user session. The server setting `edc.device_http_auth` decides how it is protected:
  - `token` mode: a device token is required.
  - `public` mode (the default): the header can be left out.
- The device token is `<serial>:<secretVersion>:<deviceSecret>`. The secret is issued when the terminal provisions itself (first-time setup, see *Provision Device*).
- If you send a token it is always checked, and its serial must equal `{serial}`. One device cannot read another's config.

### Request

```http
GET /base/devices/config/{serial}
Authorization: Bearer <serial>:<secretVersion>:<deviceSecret>
```

| Name | Type | Required | What it means |
| --- | --- | --- | --- |
| `serial` | string (path) | Yes | Serial, e.g. `SN0001`. Spaces are trimmed. Not case-sensitive. |

### Example

```bash
curl "$BASE_URL/base/devices/config/SN0001" \
  -H "Authorization: Bearer SN0001:1:$DEVICE_SECRET"
```

### Response

`200 OK`. This route is a frozen contract for terminal firmware, so the body is the raw config object **without** the `result` envelope. It is the same JSON as *Get Resolved Device Config*.

```json
{
  "owner": "10",
  "baseId": "B-10025",
  "pingTime": 60,
  "state": "ACTIVE",
  "bypassSettleWhenUpdate": false,
  "ignoreAppBankErrorCodes": [],
  "appConfig": { "appPath": "https://cdn.example.com/edc/app-1.4.2.apk", "versionCode": 142, "forceInstall": false },
  "functions": { "qrFunc": true, "cardFunc": true, "voidFunc": true, "mqttEnabled": true },
  "hiddenConfig": { "lockConfig": false },
  "qrConfig": { "bankBincode": "970436", "bankAccount": "0011001234567", "bankAccountHolder": "BASE STORE", "numberOfCopies": 1, "qrTimeInterval": 5 },
  "cardConfig": { "maxTip": 0, "numberOfCopies": 1 },
  "mqttConfig": { "mqttDomain": "mqtt.example.com", "mqttPort": "8883", "mqttTls": true, "mqttDeviceTopic": "base/default/SN0001/cmd" }
}
```

The keys are whatever was assigned to the device, plus `owner`, `state` and `mqttConfig.mqttDeviceTopic`, which the server adds on every read. A device with no config returns only those three keys.

### Errors

This route keeps its own error body: `{"error": "<code>", "message": "..."}`.

| HTTP | `error` | What it means | What you should do |
| --- | --- | --- | --- |
| `401` | `missing_device_token` / `invalid_device_token` | A token is required (`token` mode) but missing, or the token is invalid or belongs to another serial. | Send the correct device token. |
| `404` | `not_found` | Unknown serial (no status record for this device). | Check the serial. |
| `500` | `internal_error` | Unexpected server error. | Retry later. |

---

## Call as RPC method: `edc.device.status.get_config`

Use this form from server-side code. It returns the same config as *Get Resolved Device Config*.

```http
POST /rpc/edc.device.status/get_config
Authorization: Bearer <token>
Content-Type: application/json
```

It is also at `POST /base/edc/device/status/get-config`. There is no path id. Send `args` as `[]` and put the values in `kwargs`.

| `kwargs` key | Type | Required | What it means |
| --- | --- | --- | --- |
| `serial` | string | Yes | Serial, e.g. `SN0001`. Exact match. `serial_number` is also accepted. |

```bash
curl -X POST "$BASE_URL/rpc/edc.device.status/get_config" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"args": [], "kwargs": {"serial": "SN0001"}}'
```

Response `200`: `{"id": "...", "result": {<config object>}}`. A missing `serial` is `400` / `-32602`. The permission check is for write access to `edc.device.status`.
