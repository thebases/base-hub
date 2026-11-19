## Add Device to Group

Puts a device into a group. A device can be in only one group, so adding it here moves it out of its old group.

### Before you start

- You need a bearer token whose user may write devices (permission check, ACL) and read groups. Without it you get `403`.
- You need the group `id` and the numeric device `id` (not the serial).

### Request

```http
POST /base/groups/{id}/devices
Authorization: Bearer <token>
Content-Type: application/json
```

| Path parameter | Type | Required | What it means |
| --- | --- | --- | --- |
| `id` | int | Yes | Group id, e.g. `3`. |

| Body field | Type | Required | What it means |
| --- | --- | --- | --- |
| `deviceId` | int | Yes | Numeric device id, e.g. `42`. This is the `id` from the device APIs, not the serial. |
| `groupId` | int | No | Ignored. The group always comes from the path. |

### Example

```bash
curl -X POST "$BASE_URL/base/groups/3/devices" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"deviceId":42}'
```

### Response

HTTP `201`. `result` is the updated device, with `groupId` set to the group in the path:

```json
{
  "id": "111020261535481234570001",
  "result": {
    "id": 42,
    "serial": "SN0001",
    "imei": "356938035643809",
    "ownerId": 10,
    "holderId": 25,
    "groupId": 3,
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

The device has many more fields than you usually need. The important one is `groupId`. `imei`, `mqttClientId`, `lastProvisionedAt` and `revokedAt` are left out when empty.

### Errors

Errors use the standard format described in the API guide (see "Response" → "Errors").

| HTTP | `error.code` | What it means | What you should do |
| --- | --- | --- | --- |
| `400` | `-32602` | `id` is not an integer. | Send a number. |
| `400` | `-32700` | The body is not valid JSON. | Fix the JSON. |
| `400` | `-32000` (`data.reason` = `add_device_failed`) | `deviceId` is missing or not an integer, or the device could not be saved. | Send a numeric `deviceId`. |
| `401` | `-32001` | Missing or invalid token. | Get a new token. |
| `403` | `-32003` | The user has no access to devices (or cannot read groups). | Ask an admin for access. |
| `404` | `-32004` | No group has this id, or no device has this `deviceId`. | Check both ids. |

### Good to know

- The group is checked first (before the body is read), then the device.
- Safe to retry: adding a device that is already in this group returns it again with `201`.
- To take a device out of its group use `DELETE /base/groups/{id}/devices/{deviceId}`.
