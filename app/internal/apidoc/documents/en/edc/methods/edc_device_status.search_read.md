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

---

## Get Device Status

Returns the live record of one terminal, found by serial: whether it is online, battery and network telemetry, and which config it uses. Use it for a device detail screen.

### Before you start

- You need a bearer token (see the API guide) of a user who may read `edc.device.status`.
- The call also reads the linked device and config as you, so you need read access to `edc.device` and `edc.device.config` too. Otherwise you get `403`.

### Request

```http
GET /base/device/status/{serial}
Authorization: Bearer <token>
```

| Name | Type | Required | What it means |
| --- | --- | --- | --- |
| `serial` | string (path) | Yes | Serial (the unique number printed on the terminal), e.g. `SN0001`. Exact match, case-sensitive. Surrounding spaces are trimmed. |

### Example

```bash
curl "$BASE_URL/base/device/status/SN0001" \
  -H "Authorization: Bearer $TOKEN"
```

### Response

`200 OK`. The body is the status record itself, **not** wrapped in `result`.

```json
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
```

`config` and `deviceConfig` are shortened above.

| Field | What it means |
| --- | --- |
| `device` | The registered device (`groupId` appears only if it is in a group). |
| `configId`, `config` | The config template version the device follows. Absent when the device uses a manual config or has none. |
| `deviceConfig` | The stored config snapshot for this device. |
| `status` | `OFFLINE`, `ONLINE`, `BUSY`, `ERROR` or `MAINTENANCE`. |
| `batteryPercent`, `charging`, `networkType`, `signalStrength`, `appVersion`, `firmwareVersion`, `printerStatus`, `paperStatus`, `metadata` | Telemetry reported by the terminal. |
| `lastSeenAt` | Last time the terminal reported. Use it to judge how fresh the data is. |

Empty optional fields are left out.

> **Check:** timestamps are shown as text above; the API guide says responses use Unix seconds. Confirm the format on your server. This route also returns the record without the `{"id","result"}` envelope that the API guide describes for REST routes.

### Errors

Errors use the standard format described in the API guide (see "Response" → "Errors").

| HTTP | `error.code` | What it means | What you should do |
| --- | --- | --- | --- |
| `401` | `-32001` | Missing or invalid token. | Get a new token. |
| `403` | `-32003` | You cannot read `edc.device.status`, `edc.device` or `edc.device.config`. | Ask an admin for access. |
| `404` | `-32004` | No status record for this serial. | Check the spelling. Serials are case-sensitive. |
| `500` | `-32603` | Unexpected server error. | Retry later and report the response `id`. |

### Good to know

- This API is read-only. The terminal updates its own telemetry.
- If you only need the config JSON, use *Get Resolved Device Config*.

---

## Call as RPC method: `edc.device.status.search_read`

Use the standard search method from server-side code. It returns plain status rows, without the linked `device` and `config` that the REST routes attach.

```http
POST /rpc/edc.device.status/search_read
Authorization: Bearer <token>
Content-Type: application/json
```

There is no path id. Send `args` as `[]` and put the options in `kwargs`.

| `kwargs` key | Type | Required | What it means |
| --- | --- | --- | --- |
| `domain` | array | No | Filter as a list of `[field, operator, value]`, e.g. `[["status", "=", "ONLINE"], ["serial", "ilike", "SN00"]]`. The API guide also describes a `filter` object. |
| `fields` | array of strings | No | Field names to return, e.g. `["serial", "status", "last_seen_at"]`. |
| `limit` | integer | No | Maximum rows, e.g. `20`. |
| `offset` | integer | No | Rows to skip. |
| `order` | string | No | Sort, e.g. `updated_at desc`. |

```bash
curl -X POST "$BASE_URL/rpc/edc.device.status/search_read" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"args": [], "kwargs": {"domain": [["status", "=", "ONLINE"]], "limit": 20, "offset": 0, "order": "updated_at desc"}}'
```

`result` is a list of records with snake_case field names (not `{items, total}`). Use `search_count` with the same `domain` for the total. A denied read returns `200` with `[]`.
