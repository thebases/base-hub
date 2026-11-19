## List Device Activations

Returns a page of activation records (one per device serial), newest first. Use it to see which devices have a code, when it expires, and whether it was used.

### Before you start

You need a bearer token whose user may read `edc.device.activation`; otherwise you get `403`.

### Request

```http
GET /base/device/activation?limit={limit}&offset={offset}&serial={serial}
Authorization: Bearer <token>
```

| Name | Type | Required | What it means |
| --- | --- | --- | --- |
| `limit` | int | No | Page size. Default `50`, max `500`. A value `<= 0`, above `500` or not a number becomes `50`. |
| `offset` | int | No | How many rows to skip, e.g. `50` for page 2. Default `0`. |
| `serial` | string | No | Exact, case-sensitive match. Serials are stored upper-case, so send `SN0001`, not `sn0001`. |

### Example

```bash
curl "$BASE_URL/base/device/activation?serial=SN0001" \
  -H "Authorization: Bearer $TOKEN"
```

### Response

`200`:

```json
{
  "id": "111020261535481234570001",
  "result": {
    "items": [
      {
        "id": 7,
        "serial": "SN0001",
        "expiresAt": "2026-09-27T08:00:00Z",
        "usedAt": "2026-09-26T08:05:00Z",
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
| `result.items` | The activation records on this page. Empty array when nothing matches. |
| `result.total` | How many records match in all (not just this page). |
| `items[].usedAt` | Missing until the terminal has provisioned with the code. |

The activation code is never returned.

### Errors

Errors use the standard format described in the API guide (see "Response" → "Errors").

| HTTP | `error.code` | What it means | What to do |
| --- | --- | --- | --- |
| `401` | `-32001` | Missing or invalid token. | Get a new token. |
| `403` | `-32003` | Your user may not read this model. | Ask an admin for access. |
| `500` | `-32603` | Unexpected server error. | Retry later and report the response `id`. |

### Good to know

- Use `items[].id` for `GET`, `PUT` and `DELETE /base/device/activation/{id}`.
- An activation is usable only if `usedAt` is absent and `expiresAt` is in the future.
