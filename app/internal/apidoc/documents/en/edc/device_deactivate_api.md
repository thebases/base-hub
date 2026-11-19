## Deactivate Device

Turns a device off by setting `active` to `false`. The device stays in the registry and can be re-enabled later. Use it to block a lost or retired terminal.

### Before you start

You need a bearer token whose user may write `edc.device`; otherwise you get `403`.

### Request

```http
DELETE /base/devices/{serial}
Authorization: Bearer <token>
```

| Name | Type | Required | What it means |
| --- | --- | --- | --- |
| `serial` | string | Yes | Exact serial of the device, e.g. `SN0001` (case-sensitive). |

### Example

```bash
curl -X DELETE "$BASE_URL/base/devices/SN0001" \
  -H "Authorization: Bearer $TOKEN"
```

### Response

`200`:

```json
{
  "id": "111020261535481234570001",
  "result": { "deleted": true, "id": 42 }
}
```

| Field | What it means |
| --- | --- |
| `result.deleted` | `true` when the call worked. The device is **not** removed; it is only switched off. |
| `result.id` | The numeric ID of the device. |

### Errors

Errors use the standard format described in the API guide (see "Response" → "Errors").

| HTTP | `error.code` | What it means | What to do |
| --- | --- | --- | --- |
| `401` | `-32001` | Missing or invalid token. | Get a new token. |
| `403` | `-32003` | Your user may not write `edc.device`. | Ask an admin for access. |
| `404` | `-32004` | No device with this serial. | Check the spelling (serials are upper-case). |

### Good to know

- Safe to repeat: deactivating an inactive device also succeeds.
- Nothing else changes. The secret, provisioning status, group and config are kept. To re-enable, call `PUT /base/devices/{serial}` with `{"active": true}`.
- An inactive device cannot be provisioned, and its device bearer token is rejected.
- Also available as: `POST /base/edc/devices/{id}/deactivate` (numeric device ID). It returns `{"result":{"device_id":42,"active":false,"status":"<current status>"}}`.
