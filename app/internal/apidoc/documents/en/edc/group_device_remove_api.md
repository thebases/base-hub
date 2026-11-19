## Remove Device from Group

Takes a device out of a group by clearing its `groupId`. The device itself is not deleted.

### Before you start

You need a bearer token whose user may write devices (permission check, ACL). Without it you get `403`. Get the device id from *List Devices in Group*.

### Request

```http
DELETE /base/groups/{id}/devices/{memberID}
Authorization: Bearer <token>
```

| Path parameter | Type | Required | What it means |
| --- | --- | --- | --- |
| `id` | int | Yes | Group id, e.g. `3`. The device must be in this group now. |
| `memberID` | int | Yes | Numeric device id, e.g. `101`. This is not the serial. |

### Example

```bash
curl -X DELETE "$BASE_URL/base/groups/3/devices/101" \
  -H "Authorization: Bearer $TOKEN"
```

### Response

```json
{
  "id": "111020261535481234570001",
  "result": { "deleted": true, "id": 101 }
}
```

| Field | What it means |
| --- | --- |
| `result.deleted` | `true` when the device was removed from the group. |
| `result.id` | The device id. |

### Errors

Errors use the standard format described in the API guide (see "Response" → "Errors").

| HTTP | `error.code` | What it means | What you should do |
| --- | --- | --- | --- |
| `400` | `-32602` | `id` or `memberID` is not an integer. | Send numbers. |
| `401` | `-32001` | Missing or invalid token. | Get a new token. |
| `403` | `-32003` | The user has no access to devices. | Ask an admin for access. |
| `404` | `-32004` | No device has this `memberID`, or it is not in group `id`. A repeated delete also gives this. | Check the ids. |
| `500` | `-32603` | Unexpected server error. | Retry later; report it with the response `id`. |

### Good to know

- The group in the path is checked. A device in another group (or in none) gives `404` and nothing changes.
- After removal the device has no group. In its MQTT command topic (`base/<group name>/<serial>/cmd`) it uses the group name `default`.
