## Get Device Activation

Returns one activation record by its numeric ID. Use it to check whether a device's code is still valid or has been used.

### Before you start

You need a bearer token whose user may read `edc.device.activation`; otherwise you get `403`.

### Request

```http
GET /base/device/activation/{id}
Authorization: Bearer <token>
```

| Name | Type | Required | What it means |
| --- | --- | --- | --- |
| `id` | int | Yes | Activation ID, e.g. `7`. It comes from the create or list response. It is **not** the serial. |

### Example

```bash
curl "$BASE_URL/base/device/activation/7" \
  -H "Authorization: Bearer $TOKEN"
```

### Response

`200`:

```json
{
  "id": "111020261535481234570001",
  "result": {
    "id": 7,
    "serial": "SN0001",
    "expiresAt": "2026-09-27T08:00:00Z",
    "createdAt": "2026-09-26T08:00:00Z",
    "updatedAt": "2026-09-26T08:00:00Z"
  }
}
```

| Field | What it means |
| --- | --- |
| `result.id` | Activation ID. |
| `result.serial` | Terminal serial. |
| `result.expiresAt` | When the code stops working. |
| `result.usedAt` | Only present once the terminal has used the code. |

The activation code is never returned.

### Errors

Errors use the standard format described in the API guide (see "Response" → "Errors").

| HTTP | `error.code` | What it means | What to do |
| --- | --- | --- | --- |
| `400` | `-32602` | `id` is not a number. | Send a numeric ID. |
| `401` | `-32001` | Missing or invalid token. | Get a new token. |
| `403` | `-32003` | Your user may not read this model. | Ask an admin for access. |
| `404` | `-32004` | No activation with this ID. | Check the ID with the list call. |

### Good to know

- A code is usable only if `usedAt` is absent and `expiresAt` is in the future.
- To look up by serial, use `GET /base/device/activation?serial=SN0001` (see **List Device Activations**).
