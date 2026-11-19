## Get Device Config (terminal)

Returns the settings JSON that is currently assigned to a device. The terminal itself calls this route to download its settings. Admin tools should use `GET /base/device/status/{serial}/config` (with a Base HUB bearer token) to see what a device receives.

### Before you start

This route is for terminals, not for Base HUB users. It has no Base HUB login and no permission check. The device proves who it is with a device token. A Base HUB user token is not a device token and gets `401`.

How the device is checked depends on the server setting `edc.device_http_auth`:

- `token`: you must send `Authorization: Bearer <serial>:<secretVersion>:<deviceSecret>`. The serial in the token must equal `{serial}` in the path (case-insensitive), so a device can read only its own settings.
- `public` (default): the header can be left out. If you send one anyway, it is checked like in `token` mode.

### Request

```http
GET /base/devices/config/{serial}
Authorization: Bearer <serial>:<secretVersion>:<deviceSecret>
```

| Name | Type | Required | What it means |
| --- | --- | --- | --- |
| `serial` | string (path) | Yes | Device serial, e.g. `SN0001`. Spaces are trimmed; matched case-insensitively. |

### Example

```bash
curl "$BASE_URL/base/devices/config/SN0001" \
  -H "Authorization: Bearer SN0001:1:$DEVICE_SECRET"
```

### Response

HTTP `200`. This is a fixed device contract: the settings object is returned as-is, **without** the `{"id","result"}` envelope.

```json
{
  "owner": "10",
  "baseId": "B-10025",
  "pingTime": 60,
  "state": "ACTIVE",
  "bypassSettleWhenUpdate": false,
  "ignoreAppBankErrorCodes": [],
  "appConfig": { "appPath": "https://cdn.example.com/edc/app-1.4.2.apk", "versionCode": 142, "forceInstall": false },
  "mqttConfig": { "mqttDomain": "mqtt.example.com", "mqttPort": "8883", "mqttTls": true, "mqttDeviceTopic": "base/default/SN0001/cmd" },
  "functions": { "qrFunc": true, "cardFunc": true, "voidFunc": true, "mqttEnabled": true },
  "hiddenConfig": { "lockConfig": false },
  "qrConfig": { "bankBincode": "970436", "bankAccount": "0011001234567", "bankAccountHolder": "BASE STORE", "numberOfCopies": 1, "qrTimeInterval": 5 },
  "cardConfig": { "maxTip": 0, "numberOfCopies": 1 }
}
```

The keys are whatever was assigned to the device (a copy of a config template, or a manual config). The server then sets three values when you read it:

| Key | What it means |
| --- | --- |
| `owner` | The device's `ownerId`, as a string. |
| `state` | The device's `provisioningStatus` (e.g. `ACTIVE`). |
| `mqttConfig.mqttDeviceTopic` | The MQTT topic (the channel name) where the device receives commands: `base/<group name>/<serial>/cmd`. The group name is `default` when the device has no group. |

A device with no config assigned gets only these keys (`owner`, `state`, `mqttConfig`). `appConfig.versionName` is no longer sent.

### Errors

The body of an error on this route is `{"error": "<name>", "message": "..."}`, not the standard error format.

| HTTP | `error` | What it means | What you should do |
| --- | --- | --- | --- |
| `401` | `missing_device_token` | Token mode, and no `Authorization: Bearer ...` header. | Send the device token. |
| `401` | `invalid_device_token` | Badly formed token, wrong secret or version, inactive or unprovisioned device, or the token's serial differs from `{serial}`. | Check the token; provision the device again if needed. |
| `404` | `not_found` | Unknown serial (public mode without a token), or no status record for this device. | Check the serial. |
| `500` | `internal_error` | Unexpected server error. | Retry later. |

### Good to know

- To change what a device receives, edit the template (*Create Device Config*, *Update Device Config*) or assign another config to the device.
