## Search Devices

Searches devices by serial and pages the results by page number. It gives the same result as `GET /base/devices?serial=...`, for clients that prefer a JSON body with `pageNum` and `pageSize`.

### Before you start

You need a bearer token (the login token you send in the `Authorization` header) for a user who is allowed to read devices. Without that permission you get `403`.

### Request

```http
POST /base/devices/search
Authorization: Bearer <token>
Content-Type: application/json
```

| Name | Type | Required | What it means |
| --- | --- | --- | --- |
| `value` | string | No | Case-insensitive "contains" match on the serial, e.g. `SN00`. Spaces are trimmed. `serial` is accepted as another name. Empty returns all devices. |
| `pageSize` | int | No | Rows per page, e.g. `20`. Default `50`, max `500`. A value `<= 0`, above `500` or missing becomes `50`. `page_size` and `limit` also work. |
| `pageNum` | int | No | Page number, starting at `1`. Default `1`; a value `<= 0` becomes `1`. `page_num` and `page` also work. |

An empty body is fine: all defaults are used.

### Example

```bash
curl -X POST "$BASE_URL/base/devices/search" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"value":"SN00","pageSize":20,"pageNum":1}'
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
        "provisioningStatus": "PENDING",
        "secretVersion": 0,
        "createdAt": "2026-09-26T08:00:00Z",
        "updatedAt": "2026-09-26T08:00:00Z"
      }
    ],
    "total": 1
  }
}
```

| Field | What it means |
| --- | --- |
| `items` | Devices on this page, newest first, including disabled ones. `[]` when nothing matches. |
| `total` | How many devices match in all pages. `0` when nothing matches. |

Each item has the same fields as in *Get Device*.

### Errors

Errors use the standard format described in the API guide (see "Response" → "Errors").

| HTTP | `error.code` | What it means | What you should do |
| --- | --- | --- | --- |
| `400` | `-32700` | The body is not valid JSON. | Fix the JSON. |
| `401` | `-32001` | Missing or invalid token. | Log in again and retry. |
| `403` | `-32003` | Your user may not read devices. | Ask an admin for the permission. |
| `500` | `-32603` | Unexpected server error. | Retry later; report the response `id`. |

### Good to know

- Only the serial is searched. To filter by owner or `active`, use `GET /base/devices`.
- Other filter keys from the old service (`groupId`, `configurationId`, `sortBy`, date filters) are ignored.
- Also available as the older route `POST /base/edc/devices/search` with the same keys inside `kwargs`. Its items use snake_case names, and a user without read permission gets `200` with an empty result instead of `403`.
