## List Devices

Returns a page of registered devices, newest first. Use it for the device table in the admin UI, with optional filters.

### Before you start

You need a bearer token (see the API guide) of a user who may read `edc.device`.

### Request

```http
GET /base/devices?limit={limit}&offset={offset}&serial={serial}&owner_id={owner_id}&active={active}
Authorization: Bearer <token>
```

| Name | Type | Required | What it means |
| --- | --- | --- | --- |
| `serial` | string | No | Part of the serial, not case-sensitive. `SN00` matches `SN0001`. |
| `owner_id` | integer | No | Exact owner id, e.g. `10`. Ignored if not a number. |
| `active` | boolean | No | `true` or `false` (also `1`/`0`, `t`/`f`). Ignored if not a boolean. |
| `limit` | integer | No | Page size, e.g. `20`. Default `50`, maximum `500`. A value `<= 0`, `> 500` or non-numeric becomes `50`. |
| `offset` | integer | No | Rows to skip. Default `0`. |

### Example

```bash
curl "$BASE_URL/base/devices?limit=20&offset=0&serial=SN00&active=true" \
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
        "id": 42,
        "serial": "SN0001",
        "imei": "356938035643809",
        "ownerId": 10,
        "holderId": 25,
        "groupId": 1,
        "active": true,
        "provisioningStatus": "ACTIVE",
        "mqttClientId": "SN0001",
        "secretVersion": 1,
        "lastProvisionedAt": "2026-09-26T08:05:00Z",
        "createdAt": "2026-09-26T08:00:00Z",
        "updatedAt": "2026-09-26T08:05:00Z"
      }
    ],
    "total": 1
  }
}
```

| Field | What it means |
| --- | --- |
| `items` | The devices on this page, newest first. |
| `total` | How many devices match your filters in all (ignores `limit` and `offset`). |
| `provisioningStatus` | Where the terminal is in first-time setup, e.g. `PENDING` or `ACTIVE`. |

`imei`, `groupId`, `mqttClientId`, `lastProvisionedAt` and `revokedAt` are left out when empty. The device secret is never returned. Inactive devices are included unless you send `active=true`.

### Errors

Errors use the standard format described in the API guide (see "Response" → "Errors").

| HTTP | `error.code` | What it means | What you should do |
| --- | --- | --- | --- |
| `401` | `-32001` | Missing or invalid token. | Get a new token. |
| `403` | `-32003` | Your user cannot read `edc.device`. | Ask an admin for access. |
| `500` | `-32603` | Unexpected server error. | Retry later and report the response `id`. |

### Good to know

- Page with `offset = (page - 1) * limit` and stop when `offset >= total`.
- When nothing matches, `items` is `[]` and `total` is `0`.
- Asking for more than 500 rows does not cap at 500. It silently returns 50.
- There is no `groupId` filter here. Use `GET /base/groups/{id}/devices` for one group.

---

## List Devices in Group

Returns the devices that belong to one group, paged. Use it to show the members of a group.

### Request

```http
GET /base/groups/{id}/devices?limit={limit}&offset={offset}
Authorization: Bearer <token>
```

You need a user who may read `edc.device`.

| Name | Type | Required | What it means |
| --- | --- | --- | --- |
| `id` | integer (path) | Yes | Group id, e.g. `3`. |
| `limit` | integer | No | Page size `1` to `500`, default `50`. Other values become `50`. |
| `offset` | integer | No | Rows to skip, default `0`. |

### Example

```bash
curl "$BASE_URL/base/groups/3/devices?limit=50&offset=0" \
  -H "Authorization: Bearer $TOKEN"
```

### Response

`200 OK`. Devices are sorted by `id` ascending, and inactive devices are included.

```json
{
  "id": "111020261535481234570001",
  "result": {
    "items": [
      {
        "id": 42,
        "serial": "SN0001",
        "imei": "356938035643809",
        "ownerId": 10,
        "holderId": 25,
        "groupId": 3,
        "active": true,
        "provisioningStatus": "ACTIVE",
        "mqttClientId": "SN0001",
        "secretVersion": 1,
        "lastProvisionedAt": "2026-09-26T08:05:00Z",
        "createdAt": "2026-09-26T08:00:00Z",
        "updatedAt": "2026-09-26T08:05:00Z"
      }
    ],
    "total": 1
  }
}
```

Each item has the same fields as in *List Devices*. Its `id` is the `memberID` used by `DELETE /base/groups/{id}/devices/{memberID}`. `total` is the number of devices in the group before paging.

### Errors

Errors use the standard format described in the API guide (see "Response" → "Errors").

| HTTP | `error.code` | What it means | What you should do |
| --- | --- | --- | --- |
| `400` | `-32602` | `id` is not a number. | Send an integer id. |
| `401` | `-32001` | Missing or invalid token. | Get a new token. |
| `403` | `-32003` | Your user cannot read `edc.device`. | Ask an admin for access. |
| `500` | `-32603` | Unexpected server error. | Retry later and report the response `id`. |

### Good to know

- An unknown group returns `200` with `items: []` and `total: 0`, not `404`.
- A device belongs to at most one group (its own `groupId`). There are no separate membership records.

---

## Call as RPC method: `edc.device.search_read`

Use the standard search method from server-side code. It reads the device table directly.

```http
POST /rpc/edc.device/search_read
Authorization: Bearer <token>
Content-Type: application/json
```

There is no path id. Send `args` as `[]` and put the options in `kwargs`.

| `kwargs` key | Type | Required | What it means |
| --- | --- | --- | --- |
| `domain` | array | No | Filter as a list of `[field, operator, value]`, e.g. `[["serial", "ilike", "SN00"], ["active", "=", true]]`. The API guide also describes a `filter` object. |
| `fields` | array of strings | No | Field names to return, e.g. `["id", "serial"]`. |
| `limit` | integer | No | Maximum rows, e.g. `20`. |
| `offset` | integer | No | Rows to skip. |
| `order` | string | No | Sort, e.g. `created_at desc`. |

```bash
curl -X POST "$BASE_URL/rpc/edc.device/search_read" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"args": [], "kwargs": {"domain": [["serial", "ilike", "SN00"]], "limit": 20, "offset": 0, "order": "created_at desc"}}'
```

`result` is a plain list of records with snake_case field names (not `{items, total}`). Use `search_count` with the same `domain` to get the total. A denied read returns `200` with `[]`.
