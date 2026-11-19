## Update Device

Changes a device's IMEI, owner, holder and active flag. Only the keys you send are changed. The serial cannot be changed.

### Before you start

You need a bearer token (see the API guide) of a user who may write `edc.device`.

### Request

```http
PUT /base/devices/{serial}
Authorization: Bearer <token>
Content-Type: application/json
```

| Name | Type | Required | What it means |
| --- | --- | --- | --- |
| `serial` | string (path) | Yes | Exact serial of the device, e.g. `SN0001`. |
| `imei` | string | No | New IMEI (the phone-style hardware number). Omit to keep it; send `null` to clear it. |
| `ownerId` | integer | No | New owner id, e.g. `10`. Omit to keep it. |
| `holderId` | integer | No | New holder id, e.g. `30`. Omit to keep it. |
| `active` | boolean | No | `true` or `false`. Omit to keep it. |
| `serial` (body) | string | No | If you send it, it must equal the path serial. A different value is rejected (`serial is immutable`). |

### Example

```bash
curl -X PUT "$BASE_URL/base/devices/SN0001" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"imei":"356938035643809","ownerId":10,"holderId":30,"active":true}'
```

### Response

`200 OK`. The updated device as stored, inside the standard `result` envelope.

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

Other keys in your body (such as `groupId` or `provisioningStatus`) are ignored. Change the group with `POST /base/groups/{id}/devices`.

### Errors

Errors use the standard format described in the API guide (see "Response" → "Errors").

| HTTP | `error.code` | What it means | What you should do |
| --- | --- | --- | --- |
| `400` | `-32700` | The body is not valid JSON. | Fix the JSON. |
| `400` | `-32000` | The body `serial` differs from the stored one, or the device could not be saved. | Remove `serial` from the body or make it match. |
| `401` | `-32001` | Missing or invalid token. | Get a new token. |
| `403` | `-32003` | Your user cannot write `edc.device`. | Ask an admin for access. |
| `404` | `-32004` | No device has this serial. | Check the spelling. |

### Good to know

- This is a partial update. Omitted keys keep their stored value.
- Use `"active": true` to turn on a device that `DELETE /base/devices/{serial}` turned off.
- Provisioning fields (`provisioningStatus`, secret, MQTT client id) are not changed here.

---

## Add Device to Group

Puts a device in a group by setting its `groupId`. A device belongs to at most one group, so adding it to a new group moves it out of the old one.

### Request

```http
POST /base/groups/{id}/devices
Authorization: Bearer <token>
Content-Type: application/json
```

You need a user who may write `edc.device` (and read `edc.group`).

| Name | Type | Required | What it means |
| --- | --- | --- | --- |
| `id` | integer (path) | Yes | Group id, e.g. `3`. |
| `deviceId` | integer | Yes | Numeric device id, e.g. `42`. This is the `id` from the device APIs, **not** the serial. |
| `groupId` | integer | No | Ignored. The group always comes from the path. |

### Example

```bash
curl -X POST "$BASE_URL/base/groups/3/devices" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"deviceId":42}'
```

### Response

`201 Created`. The updated device inside `result`, with `groupId` set to the path group.

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

### Errors

Errors use the standard format described in the API guide (see "Response" → "Errors").

| HTTP | `error.code` | What it means | What you should do |
| --- | --- | --- | --- |
| `400` | `-32602` | `id` is not a number. | Send an integer id. |
| `400` | `-32700` | The body is not valid JSON. | Fix the JSON. |
| `400` | `-32000` | `deviceId` is missing or not an integer, or the device could not be saved. | Send an integer `deviceId`. |
| `401` | `-32001` | Missing or invalid token. | Get a new token. |
| `403` | `-32003` | Your user cannot write `edc.device` or read `edc.group`. | Ask an admin for access. |
| `404` | `-32004` | No group or no device with this id. | Check both ids. The group is checked first. |

### Good to know

- Safe to retry: adding a device that is already in this group returns it again with `201`.
- To take a device out of its group, use `DELETE /base/groups/{id}/devices/{deviceId}`.

---

## Remove Device from Group

Removes a device from a group by clearing its `groupId`. The device itself is not deleted.

### Request

```http
DELETE /base/groups/{id}/devices/{memberID}
Authorization: Bearer <token>
```

You need a user who may write `edc.device`.

| Name | Type | Required | What it means |
| --- | --- | --- | --- |
| `id` | integer (path) | Yes | Group id. The device must be in this group now. |
| `memberID` | integer (path) | Yes | Device id (the `id` from *List Devices in Group*), **not** the serial. |

### Example

```bash
curl -X DELETE "$BASE_URL/base/groups/3/devices/101" \
  -H "Authorization: Bearer $TOKEN"
```

### Response

`200 OK`.

```json
{
  "id": "111020261535481234570001",
  "result": { "deleted": true, "id": 101 }
}
```

`result.id` is the device id.

### Errors

Errors use the standard format described in the API guide (see "Response" → "Errors").

| HTTP | `error.code` | What it means | What you should do |
| --- | --- | --- | --- |
| `400` | `-32602` | `id` or `memberID` is not a number. | Send integer ids. |
| `401` | `-32001` | Missing or invalid token. | Get a new token. |
| `403` | `-32003` | Your user cannot write `edc.device`. | Ask an admin for access. |
| `404` | `-32004` | No device has this `memberID`, or it is not in group `id` (also on a repeated delete). | Check the ids. |
| `500` | `-32603` | Unexpected server error. | Retry later and report the response `id`. |

### Good to know

- The path group is checked. A device in a different group (or none) returns `404` and nothing changes.
- After removal the device has no group. For MQTT topics (`base/<group name>/<serial>/cmd`) it uses the `default` group name.

---

## Call as RPC method: `edc.device.write`

Use the standard write method from server-side code.

```http
POST /rpc/edc.device/write/{id}
Authorization: Bearer <token>
Content-Type: application/json
```

The device id goes in the path (a comma list of ids is allowed). Put the values in the first element of `args`.

| Name | Type | Required | What it means |
| --- | --- | --- | --- |
| `id` | integer (path) | Yes | Device id, e.g. `42`. |
| `args[0]` | object | Yes | Field values to write, in snake_case, e.g. `{"holder_id": 30, "active": true}`. |

```bash
curl -X POST "$BASE_URL/rpc/edc.device/write/42" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"args": [{"holder_id": 30, "active": true}], "kwargs": {}}'
```

`result` is `true` when the write succeeds. Changing `serial` is rejected (`serial is immutable`). Unlike the REST route, this accepts any writable field, so only send fields you mean to change.
