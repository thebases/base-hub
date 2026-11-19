## Activate Device Config

Makes one version the active version of its config template. A config template is a named set of terminal settings that is saved in versions (see *Create Device Config*). Use this call to roll out a saved edit, or to roll back to an older version. Devices that follow the template switch to the version you activate.

### Before you start

You need a bearer token (the login token you send in the `Authorization` header) for a user who is allowed to change config templates. Without that permission you get `403`.

### Request

```http
POST /base/device/config/{id}/activate
Authorization: Bearer <token>
```

No body.

| Name | Type | Required | What it means |
| --- | --- | --- | --- |
| `id` | int (path) | Yes | ID of the config version to activate, e.g. `14`. |

### Example

```bash
curl -X POST "$BASE_URL/base/device/config/14/activate" \
  -H "Authorization: Bearer $TOKEN"
```

### Response

HTTP `200`. The activated version, inside the standard `result` envelope.

```json
{
  "id": "111020261535481234570001",
  "result": {
    "id": 14,
    "name": "default-retail",
    "version": 2,
    "configJson": {
      "owner": "THEBASE",
      "baseId": "B001",
      "pingTime": 30,
      "functions": { "qrFunc": true, "cardFunc": true, "mqttEnabled": true }
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
    "mqttTls": true,
    "owner": "THEBASE",
    "baseId": "B001",
    "pingTime": 30,
    "bypassSettleWhenUpdate": false,
    "isActive": true,
    "createdAt": "2026-09-26T09:00:00Z"
  }
}
```

`configJson` is shortened above; the real one always contains every known key. All fields are explained in *Create Device Config*.

### Errors

Errors use the standard format described in the API guide (see "Response" → "Errors").

| HTTP | `error.code` | What it means | What you should do |
| --- | --- | --- | --- |
| `400` | `-32602` | `id` is not an integer. | Send a number in the path. |
| `401` | `-32001` | Missing or invalid token. | Log in again and retry. |
| `403` | `-32003` | Your user may not change config templates. | Ask an admin for the permission. |
| `404` | `-32004` | No config has this ID. | List configs and pick a valid ID. |
| `500` | `-32603` | Unexpected server or database error. | Retry later; report the response `id`. |

### Good to know

- Only versions with the **same name** are switched off. Other templates keep their own active version.
- Devices that were using the previously active version move to this one. Their stored config is replaced with this version's `configJson` (and `cardConfig.tidmid` is rebuilt from its linked bank apps, if it has any). Devices on other versions, or with a manual config, are not changed. If no other version of that name was active, no device moves.
- Activating a version that is already active is harmless.
- Also available as the older route `POST /base/edc/device/config/{id}/activate` with an empty `kwargs`. It returns the row with snake_case names, any failure is `400` with a plain-text message, and a user without write permission gets `403`.
