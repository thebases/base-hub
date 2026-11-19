## Rotate Device Secret

Replaces the secret of a device that is already active, without a new activation code. Use it when the secret was lost or leaked. The new secret is returned once.

> **Warning:** The old secret stops working immediately. Give the new secret to the terminal right away, or it is locked out.

### Before you start

You need a bearer token whose user may write `edc.device`; otherwise you get `403`. The device must already be `active` (see **Create Device** for how a device becomes active).

### Request

```http
POST /base/edc/devices/{id}/rotate-secret
Authorization: Bearer <token>
Content-Type: application/json
```

The body takes no parameters. Send `{}`.

| Name | Where | Type | Required | What it means |
| --- | --- | --- | --- | --- |
| `id` | path | integer | Yes | Numeric device ID, e.g. `42` (not the serial). |

### Example

```bash
curl -X POST "$BASE_URL/base/edc/devices/42/rotate-secret" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{}'
```

### Response

`200`:

```json
{
  "id": "111020261535481234570001",
  "result": {
    "device_id": 42,
    "secret": "0b1c2d3e4f5a69788796a5b4c3d2e1f00112233445566778899aabbccddeeff0"
  }
}
```

| Field | What it means |
| --- | --- |
| `result.device_id` | Numeric ID of the device. |
| `result.secret` | The new secret (64 hex characters). **Shown only once.** |

### Errors

Errors use the standard format described in the API guide (see "Response" → "Errors").

| HTTP | `error.code` | What it means | What to do |
| --- | --- | --- | --- |
| `401` | `-32001` | Missing or invalid token. | Get a new token. |
| `403` | `-32003` | Your user may not write `edc.device`. | Ask an admin for access. |
| `400` | `-32602` | `id` is not a positive integer. | Fix the ID. |
| `400` | `-32000` | No device with this ID, the device `status` is not `active`, or the secret could not be saved. | Check the ID; activate the device first. |

### Good to know

- `secret_version` goes up by 1. The terminal's bearer token must use the new version: `<SERIAL>:<new version>:<secret>`.
- `status` and `activated_at` do not change. `provisioning_status` becomes `active` and `revoked_at` is cleared, so rotating also un-revokes the device.
- The copy of the secret used to sign MQTT commands is refreshed too. If that step fails it is only logged and the call still succeeds.
