## Delete Group

Permanently deletes a device group. The devices in it are not deleted; their `groupId` is cleared.

### Before you start

You need a bearer token whose user may delete groups (permission check, ACL). Without it you get `403`.

### Request

```http
DELETE /base/groups/{id}
Authorization: Bearer <token>
```

| Path parameter | Type | Required | What it means |
| --- | --- | --- | --- |
| `id` | int | Yes | Group id, e.g. `3`. |

### Example

```bash
curl -X DELETE "$BASE_URL/base/groups/3" \
  -H "Authorization: Bearer $TOKEN"
```

### Response

```json
{
  "id": "111020261535481234570001",
  "result": { "deleted": true, "id": 3 }
}
```

| Field | What it means |
| --- | --- |
| `result.deleted` | `true` when the group was deleted. |
| `result.id` | Id of the deleted group. |

### Errors

Errors use the standard format described in the API guide (see "Response" → "Errors").

| HTTP | `error.code` | What it means | What you should do |
| --- | --- | --- | --- |
| `400` | `-32602` | `id` is not an integer. | Send a number. |
| `401` | `-32001` | Missing or invalid token. | Get a new token. |
| `403` | `-32003` | The user has no access to delete groups. | Ask an admin for access. |
| `404` | `-32004` | No group has this id. It also happens when you delete the same group twice. | Check the id. |
| `500` | `-32603` | Unexpected server error. | Retry later; report it with the response `id`. |

### Good to know

- There is no undo. Ask the user to confirm before you call it.
- Devices left without a group use the group name `default` in their MQTT command topic. If you delete the seeded `default` group, new devices are created without a group.
