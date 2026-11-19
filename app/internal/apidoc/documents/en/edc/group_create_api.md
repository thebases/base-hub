## Create Group

Creates a device group (a named set of terminals, for example one per city or customer). Add devices to it afterwards with `POST /base/groups/{id}/devices`. A device can be in at most one group.

### Before you start

You need a bearer token (see the API guide) whose user may create groups (a permission check, called ACL). Without it you get `403`.

### Request

```http
POST /base/groups
Authorization: Bearer <token>
Content-Type: application/json
```

| Body field | Type | Required | What it means |
| --- | --- | --- | --- |
| `name` | string | Yes | Group name, e.g. `Hanoi Stores`. Names do not have to be unique. The server does not trim it or reject an empty string, but leaving it out fails. |
| `customerId` | int | No | Id of the customer that owns the group, e.g. `12`. |

Other keys are ignored.

### Example

```bash
curl -X POST "$BASE_URL/base/groups" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"name":"Hanoi Stores","customerId":12}'
```

### Response

HTTP `201`:

```json
{
  "id": "111020261535481234570001",
  "result": { "id": 3, "name": "Hanoi Stores", "customerId": 12 }
}
```

| Field | What it means |
| --- | --- |
| `id` | Request id (yours, or made by the server). |
| `result.id` | Id of the new group. Use it in the other group calls. |
| `result.name` | The group name. |
| `result.customerId` | The owner customer. Left out when none. |

### Errors

Errors use the standard format described in the API guide (see "Response" → "Errors").

| HTTP | `error.code` | What it means | What you should do |
| --- | --- | --- | --- |
| `400` | `-32700` | The body is not valid JSON. | Fix the JSON. |
| `400` | `-32000` (`data.reason` = `create_failed`) | `name` is missing, or the group could not be saved. | Send a `name`. |
| `401` | `-32001` | Missing or invalid token. | Get a new token. |
| `403` | `-32003` | The user has no access to create groups. | Ask an admin for access. |

### Good to know

- The group name becomes part of the device command topic: `base/<group name>/<serial>/cmd`. Choose names that are safe in an MQTT topic.
