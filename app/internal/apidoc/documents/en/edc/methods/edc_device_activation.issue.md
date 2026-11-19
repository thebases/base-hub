## Create Device Activation

Sets the one-time activation code (an 8-digit code) that a terminal types in to set itself up for the first time (provisioning). This is step 2 of device onboarding. See `POST /base/devices` for the full create, activate, provision guide.

### Before you start

- You need a bearer token (see the API guide) of a user who may write `edc.device.activation`.
- The device should already be registered (step 1). A serial with no registered device still gets an activation row, but provisioning then fails with `device_not_registered`.

### Request

```http
POST /base/device/activation
Authorization: Bearer <token>
Content-Type: application/json
```

| Name | Type | Required | What it means |
| --- | --- | --- | --- |
| `serial` | string | Yes | Serial of the terminal, e.g. `SN0001`. Upper-cased and trimmed automatically. |
| `activationCode` | string | Yes | **Exactly 8 digits**, e.g. `48213907`. Other formats are accepted here but can never be used to provision. |
| `expiresAt` | string (RFC 3339) | No | When the code stops working, e.g. `2026-09-27T08:00:00Z`. If you leave it out (or send an unreadable value), it is now + the server's activation lifetime (`edc.provisioning.activation_code_ttl_minutes`, default 60 minutes). |

### Example

```bash
curl -X POST "$BASE_URL/base/device/activation" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"serial":"SN0001","activationCode":"48213907","expiresAt":"2026-09-27T08:00:00Z"}'
```

### Response

`201 Created`. The activation is inside the standard `result` envelope.

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
| `result.id` | Activation id. Use it in `PUT /base/device/activation/{id}`. |
| `serial`, `expiresAt` | The terminal and when its code stops working. |

> **Warning:** the activation code is never returned. Keep the code you sent and give it to the person setting up the terminal.

### Errors

Errors use the standard format described in the API guide (see "Response" → "Errors").

| HTTP | `error.code` | What it means | What you should do |
| --- | --- | --- | --- |
| `400` | `-32700` | The body is not valid JSON. | Fix the JSON. |
| `400` | `-32000` | `serial` or `activationCode` is empty, or the record could not be saved. | Send both values. |
| `401` | `-32001` | Missing or invalid token. | Get a new token. |
| `403` | `-32003` | Your user cannot write `edc.device.activation`. | Ask an admin for access. |
| `500` | `-32603` | The server has no provisioning key configured (`reason`: `provisioning_secret_not_configured`). | Ask the server admin to configure it. |

### Good to know

- There is one activation row per serial. Calling this again for the same serial replaces the old code and clears `usedAt`.
- Issuing a code **re-arms the device**: its provisioning status goes back to `PENDING` and its secret, secret version and MQTT client id are wiped. An `ACTIVE` terminal is disconnected until it provisions again with the new code. `active` is not changed.
- Also available as the RPC call below. There the `code` is optional, and when you omit it the server generates an 8-digit code and returns it **once**.

---

## Update Device Activation

Issues a new activation code (and optionally a new expiry) for an existing activation. Use it to extend an expired code or to give a terminal a fresh code so it can provision again.

### Request

```http
PUT /base/device/activation/{id}
Authorization: Bearer <token>
Content-Type: application/json
```

| Name | Type | Required | What it means |
| --- | --- | --- | --- |
| `id` | integer (path) | Yes | Activation id, e.g. `7`. |
| `activationCode` | string | No, but send it | The new code, exactly 8 digits. **If you omit it or send `""`, the server makes a random code that is not returned anywhere**, so always send the code you want. |
| `expiresAt` | string (RFC 3339) | No | New expiry, e.g. `2026-09-27T08:00:00Z`. If omitted (or unreadable) it is now + the activation lifetime (default 60 minutes). Include a timezone (`Z` or `+07:00`). |

### Example

Change the code and set a new expiry:

```bash
curl -X PUT "$BASE_URL/base/device/activation/7" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"activationCode":"48213907","expiresAt":"2026-09-27T08:00:00Z"}'
```

### Response

`200 OK`, same shape as *Create Device Activation* inside `result`.

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

`usedAt` is always cleared, so the code can be used again. The code is never returned.

### Errors

Errors use the standard format described in the API guide (see "Response" → "Errors").

| HTTP | `error.code` | What it means | What you should do |
| --- | --- | --- | --- |
| `400` | `-32602` | `id` is not a number. | Send an integer id. |
| `400` | `-32700` | The body is not valid JSON. | Fix the JSON. |
| `400` | `-32000` | The record could not be saved. | Read `message`. |
| `401` | `-32001` | Missing or invalid token. | Get a new token. |
| `403` | `-32003` | Your user cannot write `edc.device.activation`. | Ask an admin for access. |
| `404` | `-32004` | No activation has this id. | Check the id. |
| `500` | `-32603` | The server has no provisioning key configured. Nothing is changed. | Ask the server admin to configure it. |

### Good to know

- **Every successful call resets the device.** Its secret, secret version and MQTT client id are wiped, its provisioning status goes back to `PENDING`, and the terminal is logged out. Do not call it on a working device unless you want to set it up again.
- There is no "extend expiry only" call. Sending only `expiresAt` also replaces the code with an unknown random one. To extend, send the code again together with the new `expiresAt`.

---

## Call as RPC method: `edc.device.activation.issue`

Use this form from server-side code. It runs the same logic as *Create Device Activation*.

```http
POST /rpc/edc.device.activation/issue
Authorization: Bearer <token>
Content-Type: application/json
```

The same method is also at `POST /base/edc/device/activation/issue`. It has no path id. Send `args` as `[]` and put the values in `kwargs`.

| `kwargs` key | Type | Required | What it means |
| --- | --- | --- | --- |
| `serial` | string | Yes | Serial, e.g. `SN0001`. Upper-cased and trimmed. |
| `code` | string | No | The 8-digit code. `activation_code` and `activationCode` are also accepted. If omitted, the server generates one. |
| `expires_at` | string (RFC 3339) | No | Expiry (`expiresAt` also accepted). Default now + activation lifetime. |

```bash
curl -X POST "$BASE_URL/rpc/edc.device.activation/issue" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"args": [], "kwargs": {"serial": "SN0001", "expires_at": "2026-09-27T08:00:00Z"}}'
```

Response `200`:

```json
{
  "id": "111020261535481234570001",
  "result": { "id": 7, "serial": "SN0001", "expires_at": "2026-09-27T08:00:00Z", "activation_code": "48213907" }
}
```

> **Warning:** `activation_code` is returned only when the server generated it, and only this once. Save it now.

Errors: a missing `serial` is `400` / `-32602`; a missing server provisioning key is `500` / `-32603`.
