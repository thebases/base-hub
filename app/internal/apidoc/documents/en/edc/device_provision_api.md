## Provision Device

The terminal calls this to swap its serial and one-time activation code for its permanent device secret. This is step 3 of onboarding; see **Create Device** (`POST /base/devices`) for the full create → activation code → provision flow.

### Before you start

- An operator must have created the device and set an activation code (`POST /base/devices`, then `POST /base/device/activation`).
- No token is needed. The activation code is the credential.

### Request

```http
POST /device/provision
Content-Type: application/json
```

The path is **not** under `/base`. CORS is open to any origin.

| Name | Type | Required | What it means |
| --- | --- | --- | --- |
| `serialNo` | string | Yes | Terminal serial, max 128 characters, e.g. `SN0001`. Trimmed, upper-cased, not case-sensitive. |
| `activationCode` | string | Yes | Exactly 8 digits, e.g. `48213907`. Set by an operator. |

Old QR codes may send `serial_no` / `activation_code`; these are accepted too (the camelCase key wins if both are sent).

### Example

```bash
curl -X POST "$BASE_URL/device/provision" \
  -H "Content-Type: application/json" \
  -d '{"serialNo":"SN0001","activationCode":"48213907"}'
```

### Response

`200 OK`. This is a fixed device format, so there is no `result` envelope:

```json
{
  "deviceId": "SN0001",
  "deviceSecret": "9f1c2e7a4b...64 hex characters...",
  "secretVersion": 1
}
```

| Field | What it means |
| --- | --- |
| `deviceId` | The upper-cased serial. Also the MQTT client ID. |
| `deviceSecret` | The permanent secret (64 hex characters). **Shown only once.** |
| `secretVersion` | Always `1` after provisioning. |

> **Warning:** Store `deviceSecret` securely right away. It cannot be fetched again.

Afterwards, call device routes with `Authorization: Bearer <deviceId>:<secretVersion>:<deviceSecret>` and connect to MQTT with client ID `<deviceId>`.

### Errors

This device route keeps its own error body: `{"error":"<name>","message":"..."}`. A validation failure returns `{"errors":[...]}` instead.

| HTTP | `error` | What it means | What to do |
| --- | --- | --- | --- |
| `400` | `invalid_json` | The body is not valid JSON. | Fix the request. |
| `400` | `errors: [...]` | `serialNo` is empty or over 128 characters, or `activationCode` is not exactly 8 digits. | Fix the fields. |
| `401` | `activation_invalid` | Wrong code, or no code set for this serial. | Check the code, or ask for a new one (**Create Device Activation**). |
| `404` | `device_not_registered` | No device has this serial. | Ask an operator to create it (`POST /base/devices`). |
| `409` | `activation_already_used` | The code was already used. | Ask an operator to set a new code. |
| `410` | `activation_expired` | The code expired. | Ask an operator to set a new code. |
| `423` | `device_not_eligible` | The device is inactive, revoked, not `PENDING`, or already has a secret. | Re-enable it and/or set a new code. |
| `500` | `secret_persistence_failed` | The server has no provisioning key (`app.encryptKey`). | Contact the backend team. |
| `500` | `internal_error` | Other server problem. Nothing changed. | Retry later. |
| `503` | `provisioning_disabled` | Provisioning is turned off on this server (`app.deviceProvisioningEnable` is `false`). | Contact the backend team. |

### Good to know

- Retrying after an error is safe; nothing changes on failure.
- If the call worked but the response was lost, a retry returns `409` and the secret cannot be recovered. Ask an operator to set a new code, then provision again.
- A wrong code never reveals whether the device exists in a usable state: the code is checked before device eligibility.
- On success the device becomes `ACTIVE` with `secretVersion = 1`, the MQTT client ID is set to the serial, and the code is marked used. An audit entry is written without the secret.
