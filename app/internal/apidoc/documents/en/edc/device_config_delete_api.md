## Delete Device Config

Permanently deletes one version of a config template (a named set of terminal settings, see *Create Device Config*). It works only when no device is assigned to that version.

### Before you start

You need a bearer token (the login token you send in the `Authorization` header) for a user who is allowed to read and delete config templates. Without that permission you get `403`.

### Request

```http
DELETE /base/device/config/{id}
Authorization: Bearer <token>
```

| Name | Type | Required | What it means |
| --- | --- | --- | --- |
| `id` | int (path) | Yes | ID of the config version to delete, e.g. `12`. |

### Example

```bash
curl -X DELETE "$BASE_URL/base/device/config/12" \
  -H "Authorization: Bearer $TOKEN"
```

### Response

HTTP `200`.

```json
{
  "id": "111020261535481234570001",
  "result": { "deleted": true, "id": 12 }
}
```

| Field | What it means |
| --- | --- |
| `result.deleted` | `true` when the version was deleted. |
| `result.id` | ID of the deleted version. |

### Errors

Errors use the standard format described in the API guide (see "Response" → "Errors").

| HTTP | `error.code` | What it means | What you should do |
| --- | --- | --- | --- |
| `400` | `-32602` | `id` is not an integer. | Send a number in the path. |
| `401` | `-32001` | Missing or invalid token. | Log in again and retry. |
| `403` | `-32003` | Your user may not read or delete config templates. | Ask an admin for the permission. |
| `404` | `-32004` | No config has this ID. | Check the ID. |
| `409` | `-32009` | At least one device is assigned to this version (`data.reason` is `config_in_use`). | Move those devices to another config first (*Assign Device Config*), then delete again. |
| `500` | `-32603` | Unexpected server or database error. | Retry later; report the response `id`. |

### Good to know

- Only the version you name is deleted. Other versions with the same name stay.
- The links to bank apps (TID/MID settings) of that version are deleted with it.
- If you delete the active version, that template name has no active version left. Activate another version if devices should keep following the template.
- *Get Device Config* returns `deviceStatuses`, the list of devices that block the delete.
