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
- The terminal itself reads the same JSON from `GET /base/devices/config/{serial}` (see *Get Device Config*). That route is not case-sensitive, and its body is the raw config without the `result` envelope.
- Also available as `POST /base/edc/device/status/get-config` and `POST /rpc/edc.device.status/get_config` with `{"kwargs": {"serial": "SN0001"}}`.
