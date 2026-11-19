## Update Device Config

"Edits" a config template by saving a **new version** based on the one you name. The version in `{id}` is not changed, so you keep the full history. A config template is a named set of terminal settings (see *Create Device Config*).

### In a nutshell

1. Load the current version with *Get Device Config*.
2. Change what you need, then send the **whole** `configJson` and the **whole** `bankAppInfoIds` back.
3. The server saves a new version (new `id`, `version` + 1).
4. With `isActive: true` devices switch to it now. Otherwise activate it later with *Activate Device Config*.

### Before you start

You need a bearer token (the login token you send in the `Authorization` header) for a user who is allowed to change config templates. Without that permission you get `403`.

### Request

```http
PUT /base/device/config/{id}
Authorization: Bearer <token>
Content-Type: application/json
```

| Name | Type | Required | What it means |
| --- | --- | --- | --- |
| `id` | int (path) | Yes | The version you are editing, e.g. `13`. It must exist. |
| `name` | string | No | Leave empty to keep the name of `{id}`. A different name starts (or continues) the version history of that other template. |
| `configJson` | object | No | The **full** settings object. It is not merged with the old one: keys you leave out fall back to defaults. If you omit it, the new version copies `configJson` from `{id}`. |
| `isActive` | bool | No (default `false`) | `true` makes the new version the active one right away. |
| `bankAppInfoIds` | int[] | No | The **full** list of bank-app (TID/MID) config IDs to link, e.g. `[3]`. It is not copied from the old version: if you omit it, the new version has no links. |
| `checksum` | string | No | Free text stored as-is. |
| `createdBy` | string | No | Who made the edit, e.g. `operator@thebase.vn`. |

Values of the wrong type are ignored (a non-object `configJson`, a non-boolean `isActive`, a non-array `bankAppInfoIds`). A `base_id` or `id` in the body is ignored: the path `{id}` is always the base version.

### Example

```bash
curl -X PUT "$BASE_URL/base/device/config/13" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "isActive": true,
    "createdBy": "operator@thebase.vn",
    "bankAppInfoIds": [3],
    "configJson": {
      "owner": "THEBASE",
      "baseId": "B001",
      "pingTime": 30,
      "functions": { "qrFunc": true, "cardFunc": true, "voidFunc": true },
      "mqttConfig": { "mqttDomain": "mqtt.example.com", "mqttPort": "8883", "mqttUsername": "edc" }
    }
  }'
```

### Response

HTTP `200`. The **new** version, inside the standard `result` envelope. Note the new `id` and `version`.

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
      "functions": { "qrFunc": true, "cardFunc": true, "voidFunc": true, "mqttEnabled": true },
      "mqttConfig": { "mqttDomain": "mqtt.example.com", "mqttPort": "8883", "mqttUsername": "edc", "mqttTls": true }
    },
    "funcQr": true,
    "funcPrefixQr": false,
    "funcCard": true,
    "funcInst": false,
    "funcVoid": true,
    "funcApiPay": false,
    "funcBilling": false,
    "funcPromotion": false,
    "funcMqttEnabled": true,
    "mqttDomain": "mqtt.example.com",
    "mqttPort": "8883",
    "mqttUsername": "edc",
    "mqttTls": true,
    "owner": "THEBASE",
    "baseId": "B001",
    "pingTime": 30,
    "bypassSettleWhenUpdate": false,
    "createdBy": "operator@thebase.vn",
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
| `400` | `-32700` | The body is not valid JSON. | Fix the JSON. |
| `400` | `-32000` / `-32602` | The resulting name is empty, a `bankAppInfoIds` entry does not exist, or the save failed. | Read `message`; fix the name or the IDs. |
| `401` | `-32001` | Missing or invalid token. | Log in again and retry. |
| `403` | `-32003` | Your user may not change config templates. | Ask an admin for the permission. |
| `404` | `-32004` | No config has this `id`. | Check the ID. |

### Good to know

- Omitting `bankAppInfoIds` drops the bank-app links. Omitting `configJson` reuses the old one.
- With `isActive: false` (the default) the new version is saved, but devices keep using the old one.
- With `isActive: true`, other versions of the same name become inactive, and devices on the previously active version switch to the new one immediately.
- After the call, use the returned `id` for further edits, not the old one.
- Also available as the older route `POST /base/edc/device/config/{id}/update-version` with the same `kwargs` as *Create Device Config* (any `id` key is ignored). It returns the new row with snake_case names, any failure is `400` with a plain-text message, and a user without write permission gets `403`.
