## Update Device Activation

Issues a new activation code (and expiry) for an existing activation record. Use it when a code expired or a terminal must provision again.

> **Warning:** This call resets the device. The terminal is logged out and must provision again.

### Before you start

- You need a bearer token whose user may write `edc.device.activation`; otherwise you get `403`.
- Get the activation `id` from the create or list response.
- The full onboarding flow is on **Create Device** (`POST /base/devices`).

### Request

```http
PUT /base/device/activation/{id}
Authorization: Bearer <token>
Content-Type: application/json
```

| Name | Type | Required | What it means |
| --- | --- | --- | --- |
| `id` (path) | int | Yes | Activation ID, e.g. `7`. |
| `activationCode` | string | No, but always send it | The new code, exactly 8 digits, e.g. `48213907`. If missing or `""`, the server makes a random code that is **not returned anywhere**. |
| `expiresAt` | string | No | New expiry, e.g. `2026-09-27T08:00:00Z` (RFC 3339 or Unix seconds). If missing or unreadable, it is now + 60 minutes (`edc.provisioning.activation_code_ttl_minutes`). Include a time zone. |

### Example

Set a new code that is valid until a chosen time:

```bash
curl -X PUT "$BASE_URL/base/device/activation/7" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"activationCode":"48213907","expiresAt":"2026-09-27T08:00:00Z"}'
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
    "updatedAt": "2026-09-26T10:00:00Z"
  }
}
```

`usedAt` is cleared by this call. The code is never returned, so give the terminal the code you sent.

### Errors

Errors use the standard format described in the API guide (see "Response" → "Errors"). The old string code is in `error.data.reason`.

| HTTP | `error.code` | What it means | What to do |
| --- | --- | --- | --- |
| `400` | `-32602` / `-32000` | `id` is not a number, the body is not valid JSON, or the record could not be saved. | Fix the request. |
| `401` | `-32001` | Missing or invalid token. | Get a new token. |
| `403` | `-32003` | Your user may not write this model. | Ask an admin for access. |
| `404` | `-32004` | No activation with this ID. | Check the ID. |
| `500` | `-32603` | The server has no provisioning key (`provisioning_secret_not_configured`). Nothing changed. | Contact the backend team. |

### Good to know

- Every successful call re-issues the code for that row's serial. The device secret, secret version and MQTT client ID are wiped and provisioning status returns to `PENDING`.
- There is no "extend only" call. Sending only `expiresAt` replaces the code with an unknown random one. Always send `activationCode` together with `expiresAt`.
- Same effect as calling **Create Device Activation** again for the same serial.
