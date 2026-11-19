## List Devices

Returns one page of registered devices, newest first. Use it for the device table in the admin screen, with optional filters.

### Before you start

You need a bearer token (the login token you send in the `Authorization` header) for a user who is allowed to read devices. Without that permission you get `403`.

### Request

```http
GET /base/devices?limit={limit}&offset={offset}&serial={serial}&owner_id={owner_id}&active={active}
Authorization: Bearer <token>
```

| Name | Type | Required | What it means |
| --- | --- | --- | --- |
| `limit` | int | No | Page size, e.g. `20`. Default `50`, max `500`. A value `<= 0`, above `500`, or not a number becomes `50` (it is not capped at 500). |
| `offset` | int | No | Rows to skip, e.g. `40`. Default `0`. |
| `serial` | string | No | Case-insensitive "contains" match on the serial. `SN00` matches `SN0001`. |
| `owner_id` | int | No | Exact owner ID, e.g. `10`. Ignored if it is not a number. |
| `active` | bool | No | `true` or `false` (`1`/`0` and `t`/`f` also work). Ignored if it is not a boolean. |

### Example

```bash
curl "$BASE_URL/base/devices?limit=20&offset=0&serial=SN00&active=true" \
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
| `items` | The devices on this page, newest first. Empty list `[]` when nothing matches. |
| `total` | How many devices match your filters in all pages (ignores `limit` and `offset`). `0` when nothing matches. |

Each item has the same fields as in *Get Device* (`imei`, `groupId`, `mqttClientId`, `lastProvisionedAt` and `revokedAt` are left out when empty). The device secret is never returned.

### Errors

Errors use the standard format described in the API guide (see "Response" → "Errors").

| HTTP | `error.code` | What it means | What you should do |
| --- | --- | --- | --- |
| `401` | `-32001` | Missing or invalid token. | Log in again and retry. |
| `403` | `-32003` | Your user may not read devices. | Ask an admin for the permission. |
| `500` | `-32603` | Unexpected server error. | Retry later; report the response `id`. |

### Good to know

- Disabled devices (`active=false`) are included unless you send `active=true`.
- To page through results use `offset = (page - 1) * limit`, and stop when `offset >= total`.
- Asking for `limit=1000` does not give 500 rows. It silently gives 50.
- There is no `groupId` filter here. To list the devices of one group call `GET /base/groups/{id}/devices`.
