## Get Device

Returns one device by its serial (the unique number printed on the terminal). Use it for a device detail page, or to check whether a terminal has finished provisioning (the first-time setup that gives it a secret key).

### Before you start

You need a bearer token (the login token you send in the `Authorization` header) for a user who is allowed to read devices. Without that permission you get `403`.

### Request

```http
GET /base/devices/{serial}
Authorization: Bearer <token>
```

| Name | Type | Required | What it means |
| --- | --- | --- | --- |
| `serial` | string (path) | Yes | Exact serial, case-sensitive. Serials are normally upper-case, e.g. `SN0001`. Spaces around it are trimmed. |

### Example

```bash
curl "$BASE_URL/base/devices/SN0001" \
  -H "Authorization: Bearer $TOKEN"
```

### Response

HTTP `200`. The device is inside the standard `result` envelope.

```json
{
  "id": "111020261535481234570001",
  "result": {
    "id": 42,
    "serial": "SN0001",
    "imei": "356938035643809",
    "ownerId": 10,
    "holderId": 25,
    "groupId": 1,
    "active": true,
    "provisioningStatus": "ACTIVE",
    "mqttClientId": "SN0001",
    "secretVersion": 1,
    "lastProvisionedAt": "2026-09-26T08:05:00Z",
    "createdAt": "2026-09-26T08:00:00Z",
    "updatedAt": "2026-09-26T08:05:00Z"
  }
}
```

| Field | What it means |
| --- | --- |
| `id` | Internal database ID of the device. |
| `serial` | The device serial. |
| `imei` | The modem's IMEI number. Left out when empty. |
| `ownerId` | ID of the owner of the device. |
| `holderId` | ID of the holder (who currently has the device). |
| `groupId` | ID of the group the device belongs to. Left out when the device has no group. |
| `active` | `true` if the device is enabled. Disabled devices (`false`) are still returned. |
| `provisioningStatus` | `PENDING` until the terminal has been provisioned, then `ACTIVE`. Other values: `SUSPENDED`, `REVOKED`. |
| `mqttClientId` | The name the terminal uses to connect to the message broker. Left out when empty. |
| `secretVersion` | Counter that goes up each time the device secret is replaced. |
| `lastProvisionedAt`, `revokedAt` | When the device was last provisioned / revoked. Left out when empty. |
| `createdAt`, `updatedAt` | When the record was created / last changed. |

The device secret is never returned.

### Errors

Errors use the standard format described in the API guide (see "Response" → "Errors").

| HTTP | `error.code` | What it means | What you should do |
| --- | --- | --- | --- |
| `401` | `-32001` | Missing or invalid token. | Log in again and retry. |
| `403` | `-32003` | Your user may not read devices. | Ask an admin for the permission. |
| `404` | `-32004` | No device has this serial. | Check the spelling (serials are upper-case). |
| `500` | `-32603` | Unexpected server error. | Retry later; report the response `id`. |

### Good to know

- Also available as the older route `POST /base/edc/devices/by-serial` with body `{"kwargs":{"serial":"SN0001"}}`. It returns snake_case names (`owner_id`, `provisioning_status`) and, for a missing device, `400` instead of `404`. A user without read permission gets `200` with an empty result instead of `403`.
