## Consume Device Activation Code

Checks a serial and activation code against the stored activation record and marks the code as used. Use it from tools or automation that only need "verify and burn this code".

> **Warning:** This is not the terminal onboarding call. It does not create a device secret and does not change the device. Terminals use `POST /device/provision` (see **Create Device** for the full flow). Nothing in the backend calls `consume` for you.

### Before you start

You need a bearer token whose user may write `edc.device.activation`; otherwise you get `403`.

### Request

```http
POST /base/edc/device/activation/consume
Authorization: Bearer <token>
Content-Type: application/json
```

The body wraps the arguments in `kwargs`.

| Name | Type | Required | What it means |
| --- | --- | --- | --- |
| `kwargs.serial` | string | Yes | Terminal serial, e.g. `SN0001` (`serial_number` also works). Trimmed and upper-cased. |
| `kwargs.code` | string | Yes | The activation code to check, e.g. `48213907`. Trimmed. |

### Example

```bash
curl -X POST "$BASE_URL/base/edc/device/activation/consume" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"kwargs":{"serial":"SN0001","code":"48213907"}}'
```

### Response

`200`:

```json
{ "id": "111020261535481234570001", "result": { "id": 7, "serial": "SN0001" } }
```

| Field | What it means |
| --- | --- |
| `result.id` | Activation ID that was consumed. |
| `result.serial` | The serial it belongs to. |

### Errors

Errors use the standard format described in the API guide (see "Response" → "Errors"). The reason is in `error.message`.

| HTTP | Message contains | What it means | What to do |
| --- | --- | --- | --- |
| `400` | `serial is required` / `code is required` | A field is empty. | Send both fields. |
| `400` | `no activation for serial SN0001` | No code was ever set for this serial. | Create one with `POST /base/device/activation`. |
| `400` | `code already used` | The code was already consumed. | Set a new code. |
| `400` | `code expired` | The code is past its `expiresAt`. | Set a new code. |
| `400` | `invalid code` | Wrong code. | Check the code. |
| `400` | `... encryption key not configured (app.encryptKey)` | The server has no provisioning key. | Contact the backend team. |
| `401` / `403` | | Missing/invalid token, or no write access. | Get a token / ask an admin. |

### Good to know

- On success `used_at` is set. A second call with the same code fails with `code already used`. To allow it again, set a new code with `POST /base/device/activation` (this clears `used_at`).
- The code is stored only as a keyed hash and compared safely. It is never returned.
- Only the canonical route `POST /rpc/edc.device.activation/consume` is equivalent; there is no `/base/...` resource-style route.
