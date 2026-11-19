## Activate Device

Checks a one-time activation code for a device, creates a new device secret, marks the device `active` and returns the secret once. This is an admin-side shortcut that works on the numeric device ID.

> **Warning:** Terminals do **not** use this route. A terminal onboards with `POST /device/provision` (see **Create Device** for the full create → activation code → provision flow). This route only accepts codes made by **Issue Device Activation Code**, not codes set with `POST /base/device/activation`.

### Before you start

- You need a bearer token whose user may write `edc.device`; otherwise you get `403`.
- Get a code first with `POST /base/edc/devices/{id}/issue-activation-code`.

### Request

```http
POST /base/edc/devices/{id}/activate
Authorization: Bearer <token>
Content-Type: application/json
```

The body wraps the arguments in `kwargs`.

| Name | Where | Type | Required | What it means |
| --- | --- | --- | --- | --- |
| `id` | path | integer | Yes | Numeric device ID, e.g. `42` (not the serial). |
| `kwargs.code` | body | string | Yes | The code from **Issue Device Activation Code**, e.g. `48213907`. Spaces are trimmed; blank is rejected. |

### Example

```bash
curl -X POST "$BASE_URL/base/edc/devices/42/activate" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"args": [], "kwargs": {"code": "48213907"}}'
```

### Response

`200`:

```json
{
  "id": "111020261535481234570001",
  "result": {
    "device_id": 42,
    "secret": "9f8e7d6c5b4a39281706f5e4d3c2b1a0987654321fedcba0123456789abcdef0"
  }
}
```

| Field | What it means |
| --- | --- |
| `result.device_id` | Numeric ID of the device. |
| `result.secret` | The new device secret (64 hex characters). **Shown only once.** Store it on the terminal. |

The terminal then calls device routes with `Authorization: Bearer <SERIAL>:<secretVersion>:<secret>`.

### Errors

Errors use the standard format described in the API guide (see "Response" → "Errors").

| HTTP | `error.code` | What it means | What to do |
| --- | --- | --- | --- |
| `401` | `-32001` | Missing or invalid token. | Get a new token. |
| `403` | `-32003` | Your user may not write `edc.device`. | Ask an admin for access. |
| `400` | `-32602` | `id` is not a positive integer, or `code` is missing/blank. | Fix the request. |
| `400` | `-32000` | No device with this ID (message `edc.device <id> not found`). | Check the ID. |
| `400` | `-32000` | `activation code invalid or expired`. One message covers: no live code, wrong code, code already used, code expired. | Issue a new code and try again. |
| `400` | `-32000` | The new secret could not be saved. | Retry later. |

> **Check:** "Device not found" and "bad code" may be reported as `400` with `-32000`, not `404` / `-32004`. Confirm with a real call.

### Good to know

- The code is used up on success. A second call with the same code fails.
- On success the device gets `status = active`, `secret_version` + 1 and `provisioning_status = active`; `revoked_at` is cleared. The MQTT client ID is set to the upper-cased serial if it was empty.
- The old secret stops working as soon as the version changes.
