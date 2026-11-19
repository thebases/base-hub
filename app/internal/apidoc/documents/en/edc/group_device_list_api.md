## List Devices in Group

Returns the devices that belong to one group, page by page. Use it to show a group's members.

### Before you start

You need a bearer token whose user may read devices (permission check, ACL). Without it you get `403`.

### Request

```http
GET /base/groups/{id}/devices?limit={limit}&offset={offset}
Authorization: Bearer <token>
```

| Path parameter | Type | Required | What it means |
| --- | --- | --- | --- |
| `id` | int | Yes | Group id, e.g. `3`. |

| Query parameter | Type | Required | What it means |
| --- | --- | --- | --- |
| `limit` | int | No (default `50`) | Page size, `1` to `500`. A value outside this range becomes `50`. |
| `offset` | int | No (default `0`) | How many rows to skip, e.g. `50` for page 2. |

### Example

```bash
curl "$BASE_URL/base/groups/3/devices?limit=50&offset=0" \
  -H "Authorization: Bearer $TOKEN"
```

### Response

Devices are sorted by `id`, lowest first. Inactive devices are included.

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

| Field | What it means |
| --- | --- |
| `result.items` | The devices on this page. Each one is a full device object, the same as in *List Devices*. |
| `result.items[].id` | Device id. Use it as `memberID` in `DELETE /base/groups/{id}/devices/{memberID}`. |
| `result.total` | Number of devices in the group before paging. |

`imei`, `mqttClientId`, `lastProvisionedAt` and `revokedAt` are left out when empty.

### Errors

Errors use the standard format described in the API guide (see "Response" → "Errors").

| HTTP | `error.code` | What it means | What you should do |
| --- | --- | --- | --- |
| `400` | `-32602` | `id` is not an integer. | Send a number. |
| `401` | `-32001` | Missing or invalid token. | Get a new token. |
| `403` | `-32003` | The user has no access to devices. | Ask an admin for access. |
| `500` | `-32603` | Unexpected server error. | Retry later; report it with the response `id`. |

### Good to know

- An unknown group id gives `200` with `items: []` and `total: 0`, not `404`.
- A device belongs to a group through its own `groupId`. There are no separate membership records.
