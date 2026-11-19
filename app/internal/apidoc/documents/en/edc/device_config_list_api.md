## List Device Configs

Returns one page of config templates, every version of every template. A config template is a named set of terminal settings (see *Create Device Config*). Use it to fill an admin table or a "pick a template" dropdown.

### Before you start

You need a bearer token (the login token you send in the `Authorization` header) for a user who is allowed to read config templates. Without that permission you get `403`.

### Request

```http
GET /base/device/config?limit={limit}&offset={offset}&name={name}&is_active={is_active}
Authorization: Bearer <token>
```

| Name | Type | Required | What it means |
| --- | --- | --- | --- |
| `limit` | int | No | Page size, e.g. `20`. Default `50`. A value `<= 0` or above `500` becomes `50`. |
| `offset` | int | No | Rows to skip, e.g. `40`. Default `0`. |
| `name` | string | No | Case-insensitive "contains" match on the template name, e.g. `retail`. |
| `is_active` | bool | No | `true` or `false` (`1`/`0` also work). A value that cannot be read is ignored (no filter). |

Results are sorted by `name` A to Z, then newest `version` first.

### Example

```bash
curl "$BASE_URL/base/device/config?name=retail&is_active=true&limit=20" \
  -H "Authorization: Bearer $TOKEN"
```

### Response

HTTP `200`.

```json
{
  "id": "111020261535481234570001",
  "result": {
    "items": [
      {
        "id": 12,
        "name": "default-retail",
        "version": 3,
        "configJson": {
          "owner": "THEBASE",
          "baseId": "B001",
          "pingTime": 60,
          "appConfig": { "appPath": "https://cdn.example.com/edc/app-1.4.2.apk", "versionCode": 142, "forceInstall": false },
          "functions": { "qrFunc": true, "cardFunc": true, "voidFunc": true, "mqttEnabled": true }
        },
        "appPath": "https://cdn.example.com/edc/app-1.4.2.apk",
        "appVersionCode": 142,
        "appForceInstall": false,
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
        "mqttTls": true,
        "merchantName": "ABC Coffee",
        "owner": "THEBASE",
        "baseId": "B001",
        "pingTime": 60,
        "bypassSettleWhenUpdate": false,
        "createdBy": "admin@thebase.vn",
        "isActive": true,
        "createdAt": "2026-09-26T08:00:00Z"
      }
    ],
    "total": 1
  }
}
```

| Field | What it means |
| --- | --- |
| `items` | The template versions on this page. |
| `total` | How many versions match your filters in all pages, not just this page. |

`configJson` is shortened above; the real one always contains every known key. Flat fields such as `appPath` or `qrBankAccount` are left out when empty. All fields are explained in *Create Device Config*.

### Errors

Errors use the standard format described in the API guide (see "Response" → "Errors").

| HTTP | `error.code` | What it means | What you should do |
| --- | --- | --- | --- |
| `401` | `-32001` | Missing or invalid token. | Log in again and retry. |
| `403` | `-32003` | Your user may not read config templates. | Ask an admin for the permission. |
| `500` | `-32603` | Unexpected server or database error. | Retry later; report the response `id`. |

### Good to know

- Every edit creates a new version, so one template name can appear many times. Use `is_active=true` to see only the version devices currently follow.
- Items do not include `deviceStatuses` or `tidmid` (linked bank apps). Call *Get Device Config* for those.
- A `limit` or `offset` that is not an integer is treated as `0` (and then `limit` becomes `50`).
