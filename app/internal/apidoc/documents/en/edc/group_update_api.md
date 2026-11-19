## Update Group

Renames a device group and/or changes its customer. Only the fields you send are changed.

### Before you start

You need a bearer token whose user may write groups (permission check, ACL). Without it you get `403`.

### Request

```http
PUT /base/groups/{id}
Authorization: Bearer <token>
Content-Type: application/json
```

| Path parameter | Type | Required | What it means |
| --- | --- | --- | --- |
| `id` | int | Yes | Group id, e.g. `3`. |

| Body field | Type | Required | What it means |
| --- | --- | --- | --- |
| `name` | string | No | New name, e.g. `Hanoi Flagship Stores`. Leave it out to keep the current name. Not trimmed or checked. |
| `customerId` | int | No | New customer id, e.g. `12`. Leave it out to keep it. Send `null` to remove the customer. |

### Example

```bash
curl -X PUT "$BASE_URL/base/groups/3" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"name":"Hanoi Flagship Stores","customerId":12}'
```

### Response

```json
{
  "id": "111020261535481234570001",
  "result": { "id": 3, "name": "Hanoi Flagship Stores", "customerId": 12 }
}
```

`result` is the group after the update.

### Errors

Errors use the standard format described in the API guide (see "Response" → "Errors").

| HTTP | `error.code` | What it means | What you should do |
| --- | --- | --- | --- |
| `400` | `-32602` | `id` is not an integer. | Send a number. |
| `400` | `-32700` | The body is not valid JSON. | Fix the JSON. |
| `400` | `-32000` (`data.reason` = `update_failed`) | The group could not be saved, e.g. `"name": null` is not allowed. | Send a real name. |
| `401` | `-32001` | Missing or invalid token. | Get a new token. |
| `403` | `-32003` | The user has no access to write groups. | Ask an admin for access. |
| `404` | `-32004` | No group has this id. | Check the id. |

### Good to know

- This is a partial update. You can rename without sending `customerId` (the old bedc service cleared a missing `customerId`; this one does not).
- Renaming a group changes the command topic (`base/<group name>/<serial>/cmd`) that its devices receive in `mqttConfig.mqttDeviceTopic`.
