## List Device Statuses

Returns a page of live device records: online state, battery, network, app version and the config each terminal uses. Use it for a fleet monitoring table.

### Before you start

- You need a bearer token (see the API guide) of a user who may read `edc.device.status`.
- Each row also reads its device and config as you, so you need read access to `edc.device` and `edc.device.config`. Otherwise you get `403`.

### Request

```http
GET /base/device/status?limit={limit}&offset={offset}&serial={serial}&status={status}&appVersion={appVersion}&firmwareVersion={firmwareVersion}
Authorization: Bearer <token>
```

| Name | Type | Required | What it means |
| --- | --- | --- | --- |
| `serial` | string | No | Part of the serial, not case-sensitive, e.g. `SN00`. |
| `status` | string | No | Exact, upper-case value: `OFFLINE`, `ONLINE`, `BUSY`, `ERROR` or `MAINTENANCE`. An unknown value matches nothing. |
| `appVersion` | string | No | Exact app version, e.g. `1.4.2`. |
| `firmwareVersion` | string | No | Exact firmware version, e.g. `A920-2.1.0`. |
| `limit` | integer | No | Page size, e.g. `20`. Default `50`. A value `<= 0` or `> 500` becomes `50`. |
| `offset` | integer | No | Rows to skip. Default `0`. |

Results are sorted by most recently updated first.

### Example

```bash
curl "$BASE_URL/base/device/status?status=ONLINE&serial=SN00&limit=20" \
  -H "Authorization: Bearer $TOKEN"
```

### Response

`200 OK`.

```json
{
  "id": "111020261535481234570001",
  "result": {
    "items": [
      {
        "id": 7,
        "serial": "SN0001",
        "deviceId": 42,
        "device": {
          "id": 42,
          "serial": "SN0001",
          "imei": "356938035643809",
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
        "batteryPercent": 85,
        "charging": false,
        "networkType": "WIFI",
        "signalStrength": -60,
        "appVersion": "1.4.2",
        "firmwareVersion": "A920-2.1.0",
        "printerStatus": "OK",
        "paperStatus": "OK",
        "lastSeenAt": "2026-09-26T09:14:55Z",
        "metadata": { "ip": "192.168.1.20" },
        "createdAt": "2026-09-20T03:00:00Z",
        "updatedAt": "2026-09-26T09:14:55Z"
      }
    ],
    "total": 1
  }
}
```

| Field | What it means |
| --- | --- |
| `items` | The status records on this page. Each one has the same fields as *Get Device Status*. |
| `total` | How many records match your filters in all (ignores `limit` and `offset`). |

Empty optional fields (`batteryPercent`, `networkType`, `configId`, `config`, `metadata`, ...) are left out.

### Errors

Errors use the standard format described in the API guide (see "Response" → "Errors").

| HTTP | `error.code` | What it means | What you should do |
| --- | --- | --- | --- |
| `401` | `-32001` | Missing or invalid token. | Get a new token. |
| `403` | `-32003` | You cannot read `edc.device.status`, `edc.device` or `edc.device.config`. | Ask an admin for access. |
| `500` | `-32603` | Unexpected server error. | Retry later and report the response `id`. |

### Good to know

- `configId` and `config` are absent when the device uses a manual config or has none yet.
- `deviceConfig` is the stored snapshot. `owner`, `state` and `mqttConfig.mqttDeviceTopic` are added only when the terminal reads its config (see *Get Resolved Device Config*).
- To fill the version filters, call `GET /base/device/status/versions` (see *List Device Software Versions*).
