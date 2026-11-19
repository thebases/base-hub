## Get Device Config

Returns one version of a config template (a named set of terminal settings, see *Create Device Config*) by its numeric ID. The answer also lists the devices assigned to that version and its linked bank apps. Use it for a template detail page, or to load a version before editing it.

### Before you start

You need a bearer token (the login token you send in the `Authorization` header) for a user who is allowed to read config templates. The call also reads device statuses and bank-app (TID/MID) configs as you, so you need read permission on those too, or you get `403`.

### Request

```http
GET /base/device/config/{id}
Authorization: Bearer <token>
```

| Name | Type | Required | What it means |
| --- | --- | --- | --- |
| `id` | int (path) | Yes | ID of the config version, e.g. `12`. |

### Example

```bash
curl "$BASE_URL/base/device/config/12" \
  -H "Authorization: Bearer $TOKEN"
```

### Response

HTTP `200`. The version is inside the standard `result` envelope.

```json
{
  "id": "111020261535481234570001",
  "result": {
    "id": 12,
    "name": "default-retail",
    "version": 3,
    "configJson": {
      "owner": "THEBASE",
      "baseId": "B001",
      "pingTime": 60,
      "functions": { "qrFunc": true, "cardFunc": true, "mqttEnabled": true },
      "mqttConfig": { "mqttDomain": "mqtt.example.com", "mqttPort": "8883", "mqttTls": true }
    },
    "funcQr": true,
    "funcPrefixQr": false,
    "funcCard": true,
    "funcInst": false,
    "funcVoid": false,
    "funcApiPay": false,
    "funcBilling": false,
    "funcPromotion": false,
    "funcMqttEnabled": true,
    "mqttDomain": "mqtt.example.com",
    "mqttPort": "8883",
    "mqttTls": true,
    "owner": "THEBASE",
    "baseId": "B001",
    "pingTime": 60,
    "bypassSettleWhenUpdate": false,
    "isActive": true,
    "createdAt": "2026-09-26T08:00:00Z",
    "deviceStatuses": [
      {
        "id": 7,
        "serial": "SN0001",
        "deviceId": 42,
        "configId": 12,
        "deviceConfig": { "owner": "THEBASE", "baseId": "B001", "pingTime": 60 },
        "status": "ONLINE",
        "charging": false,
        "createdAt": "2026-09-20T03:00:00Z",
        "updatedAt": "2026-09-26T08:05:00Z"
      }
    ],
    "tidmid": [
      {
        "id": 3,
        "appBankInfo": "BIDV",
        "bincode": "970418",
        "defaultApp": true,
        "environment": "prod",
        "packageName": "com.wdvn.bidv",
        "tid": "12345678",
        "mid": "000000000123456",
        "createdAt": "2026-09-01T00:00:00Z",
        "updatedAt": "2026-09-01T00:00:00Z"
      }
    ]
  }
}
```

`configJson` is shortened above; the real one always contains every known key. The fields are the same as in *Create Device Config*, plus these two lists:

| Field | What it means |
| --- | --- |
| `deviceStatuses` | Devices currently assigned to this version (plain status rows). Left out when empty. These are the devices that block a delete. |
| `tidmid` | Bank apps linked to this version. `tid` is the terminal ID and `mid` the merchant ID given by the bank. Left out when empty. The bank-app `secret` is never returned. |

### Errors

Errors use the standard format described in the API guide (see "Response" → "Errors").

| HTTP | `error.code` | What it means | What you should do |
| --- | --- | --- | --- |
| `400` | `-32602` | `id` is not an integer. | Send a number in the path. |
| `401` | `-32001` | Missing or invalid token. | Log in again and retry. |
| `403` | `-32003` | Your user may not read config templates (or device statuses / bank apps). | Ask an admin for the permission. |
| `404` | `-32004` | No config has this ID. | Check the ID. |
| `500` | `-32603` | Unexpected server or database error. | Retry later; report the response `id`. |

### Good to know

- The flat `mqttPassword`, `mqttClientKey` and `mqttClientPem` fields are never returned. But `configJson.mqttConfig` still carries them.
- Also available as the older route `POST /base/edc/device/config/{id}/detail` with an empty `kwargs`. It returns the row with snake_case names plus `bank_app_info_ids` and `assigned_device_count`, without the `tidmid` / `deviceStatuses` lists. An unknown ID is `400`, and a user without read permission gets `{"result": null}` instead of `403`.
