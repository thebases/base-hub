## Create Device Activation

Sets the one-time activation code that a terminal uses to provision itself (the terminal swaps the code for its device secret). Use it right after creating a device. This is step 2 of onboarding; see **Create Device** (`POST /base/devices`) for the full flow.

### Before you start

- You need a bearer token whose user may write `edc.device.activation`; otherwise you get `403`.
- Create the device first (`POST /base/devices`).

### Request

```http
POST /base/device/activation
Authorization: Bearer <token>
Content-Type: application/json
```

| Name | Type | Required | What it means |
| --- | --- | --- | --- |
| `serial` | string | Yes | The terminal serial, e.g. `SN0001`. It is upper-cased for you. |
| `activationCode` | string | Yes | **Exactly 8 digits**, e.g. `48213907`. Other formats are accepted here but can never be used to provision. |
| `expiresAt` | string | No | When the code stops working, as RFC 3339 (`2026-09-27T08:00:00Z`) or Unix seconds. If missing or unreadable, it is now + 60 minutes (setting `edc.provisioning.activation_code_ttl_minutes`). Include a time zone. |

### Example

```bash
curl -X POST "$BASE_URL/base/device/activation" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"serial":"SN0001","activationCode":"48213907","expiresAt":"2026-09-27T08:00:00Z"}'
```

### Response

`201 Created`:

```json
{
  "id": "111020261535481234570001",
  "result": {
    "id": 7,
    "serial": "SN0001",
    "expiresAt": "2026-09-27T08:00:00Z",
    "createdAt": "2026-09-26T08:05:00Z",
    "updatedAt": "2026-09-26T08:05:00Z"
  }
}
```

| Field | What it means |
| --- | --- |
| `id` (outer) | Request ID, for tracing. |
| `result.id` | Activation ID. Use it for `GET`, `PUT` and `DELETE /base/device/activation/{id}`. |
| `result.serial` | The upper-cased serial. |
| `result.expiresAt` | When the code stops working. |

The code itself is never returned. Keep it until you give it to the terminal.

### Errors

Errors use the standard format described in the API guide (see "Response" → "Errors"). The old string code is in `error.data.reason`.

| HTTP | `error.code` | What it means | What to do |
| --- | --- | --- | --- |
| `400` | `-32700` / `-32000` | Bad JSON, or `serial` / `activationCode` is empty, or the record could not be saved. | Fix the body. |
| `401` | `-32001` | Missing or invalid token. | Get a new token. |
| `403` | `-32003` | Your user may not write this model. | Ask an admin for access. |
| `500` | `-32603` | The server has no provisioning key (`provisioning_secret_not_configured`). | Contact the backend team. |

### Good to know

- **One row per serial.** Calling this again for the same serial replaces the old code and clears `usedAt`.
- **It re-arms the device.** Provisioning status goes back to `PENDING` and the secret, secret version and MQTT client ID are wiped. An `ACTIVE` terminal is disconnected until it provisions again. `active` does not change, so a deactivated device stays blocked.
- A serial with no device still gets a row, but provisioning then fails with `device_not_registered`.
- Also available as: `POST /base/edc/device/activation/issue` with `{"kwargs":{"serial","code","expires_at"}}`. If `code` is missing, the server makes an 8-digit code and returns it once as `activation_code`.
