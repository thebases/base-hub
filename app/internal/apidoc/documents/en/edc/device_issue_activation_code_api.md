## Issue Device Activation Code

Creates a short activation code for a device, by its numeric ID. The code is returned once. Use it with **Activate Device**.

> **Warning:** This is not the terminal onboarding path. Terminals provision with `POST /device/provision` using a code set by `POST /base/device/activation` (see **Create Device** for the full flow). A code from this page works only with `POST /base/edc/devices/{id}/activate`.

### Before you start

You need a bearer token whose user may write `edc.device`; otherwise you get `403`. Device tokens are not accepted.

### Request

```http
POST /base/edc/devices/{id}/issue-activation-code
Authorization: Bearer <token>
Content-Type: application/json
```

The body wraps the arguments in `kwargs`.

| Name | Where | Type | Required | What it means |
| --- | --- | --- | --- | --- |
| `id` | path | integer | Yes | Numeric device ID, e.g. `42` (not the serial). Must be greater than 0. |
| `kwargs.issued_by` | body | integer | No | User ID to record as the issuer, e.g. `7`. Default `0`. It is not taken from the token. |

### Example

```bash
curl -X POST "$BASE_URL/base/edc/devices/42/issue-activation-code" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"args": [], "kwargs": {"issued_by": 7}}'
```

### Response

`200`:

```json
{
  "id": "111020261535481234570001",
  "result": {
    "code": "48213907",
    "expires_at": "2026-10-11T09:30:00Z"
  }
}
```

| Field | What it means |
| --- | --- |
| `result.code` | The 8-digit code. **Shown only once.** |
| `result.expires_at` | When the code stops working (30 minutes after it was issued, UTC). |

> **Check:** `expires_at` may be returned as Unix seconds instead of an RFC 3339 string. Confirm with a real call.

### Errors

Errors use the standard format described in the API guide (see "Response" → "Errors").

| HTTP | `error.code` | What it means | What to do |
| --- | --- | --- | --- |
| `401` | `-32001` | Missing or invalid token. | Get a new token. |
| `403` | `-32003` | Your user may not write `edc.device`. | Ask an admin for access. |
| `400` | `-32602` | `id` is not a positive integer. | Fix the ID. |
| `400` | `-32000` | No device with this ID (`edc.device <id> not found`), or the code could not be stored. | Check the ID, retry later. |

### Good to know

- A new code cancels any earlier unused code for the same device.
- Issuing a code does not change the device status.
