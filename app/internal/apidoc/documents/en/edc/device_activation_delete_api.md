## Delete Device Activation

Permanently removes an activation record. Use it to cancel a code that must never be used.

### Before you start

You need a bearer token whose user may delete `edc.device.activation` records; otherwise you get `403`.

### Request

```http
DELETE /base/device/activation/{id}
Authorization: Bearer <token>
```

| Name | Type | Required | What it means |
| --- | --- | --- | --- |
| `id` | int | Yes | Activation ID, e.g. `7`. |

### Example

```bash
curl -X DELETE "$BASE_URL/base/device/activation/7" \
  -H "Authorization: Bearer $TOKEN"
```

### Response

`200`:

```json
{
  "id": "111020261535481234570001",
  "result": { "deleted": true, "id": 7 }
}
```

| Field | What it means |
| --- | --- |
| `result.deleted` | `true` when the record was removed. |
| `result.id` | The ID that was deleted. |

### Errors

Errors use the standard format described in the API guide (see "Response" → "Errors").

| HTTP | `error.code` | What it means | What to do |
| --- | --- | --- | --- |
| `400` | `-32602` | `id` is not a number. | Send a numeric ID. |
| `401` | `-32001` | Missing or invalid token. | Get a new token. |
| `403` | `-32003` | Your user may not delete this model. | Ask an admin for access. |
| `404` | `-32004` | No activation with this ID (maybe already deleted). | Check the ID. |

### Good to know

- You cannot undo this. The device record and an already provisioned terminal are not affected.
- Without an activation, the terminal cannot provision (it gets `activation_invalid`). Create a new one with `POST /base/device/activation`.
