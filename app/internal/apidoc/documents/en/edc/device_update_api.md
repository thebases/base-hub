## Update Device

Changes a device's IMEI, owner, holder or active flag. Use it to hand a device to another holder, or to turn a disabled device back on. Only the keys you send are changed. The serial cannot be changed.

### Before you start

You need a bearer token (the login token you send in the `Authorization` header) for a user who is allowed to change devices. Without that permission you get `403`.

### Request

```http
PUT /base/devices/{serial}
Authorization: Bearer <token>
Content-Type: application/json
```

| Name | Type | Required | What it means |
| --- | --- | --- | --- |
| `serial` (path) | string | Yes | Serial of the device to update, e.g. `SN0001`. |
| `imei` | string | No | New IMEI, e.g. `356938035643809`. Omit it to keep the old one; send `null` to clear it. |
| `ownerId` | int | No | New owner ID, e.g. `10`. Omit it to keep the old one. |
| `holderId` | int | No | New holder ID, e.g. `30`. Omit it to keep the old one. |
| `active` | bool | No | `true` or `false`. Omit it to keep the current value. |
| `serial` (body) | string | No | If you send it, it must equal the path serial. A different value is rejected with `400` ("serial is immutable"). |

### Example

```bash
curl -X PUT "$BASE_URL/base/devices/SN0001" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"imei":"356938035643809","ownerId":10,"holderId":30,"active":true}'
```

### Response

HTTP `200`. The device as saved (read again after the write), inside the standard `result` envelope.

```json
{
  "id": "111020261535481234570001",
  "result": {
    "id": 42,
    "serial": "SN0001",
    "imei": "356938035643809",
    "ownerId": 10,
    "holderId": 30,
    "groupId": 1,
    "active": true,
    "provisioningStatus": "ACTIVE",
    "mqttClientId": "SN0001",
    "secretVersion": 1,
    "lastProvisionedAt": "2026-09-26T08:05:00Z",
    "createdAt": "2026-09-26T08:00:00Z",
    "updatedAt": "2026-09-26T09:30:00Z"
  }
}
```

The fields are the same as in *Get Device*.

### Errors

Errors use the standard format described in the API guide (see "Response" → "Errors").

| HTTP | `error.code` | What it means | What you should do |
| --- | --- | --- | --- |
| `400` | `-32700` | The body is not valid JSON. | Fix the JSON. |
| `400` | `-32000` / `-32602` | The body `serial` differs from the stored one, or the device could not be saved. | Remove `serial` from the body or make it match; read `message`. |
| `401` | `-32001` | Missing or invalid token. | Log in again and retry. |
| `403` | `-32003` | Your user may not change devices. | Ask an admin for the permission. |
| `404` | `-32004` | No device has this serial. | Check the spelling. |

### Good to know

- This is a partial update: keys you leave out keep their stored value.
- To re-enable a device that was turned off with `DELETE /base/devices/{serial}`, send `"active": true`.
- Other keys (`groupId`, `provisioningStatus`, ...) are ignored. Change the group with `POST /base/groups/{id}/devices`.
- Provisioning data (status, secret, MQTT client ID) cannot be changed here.
