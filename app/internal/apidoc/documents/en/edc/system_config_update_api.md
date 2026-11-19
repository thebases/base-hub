## Update System Config

`PUT /base/system-config` is not implemented in Base HUB (it returns `404`). You cannot replace the whole configuration in one call. Change one setting at a time in the `sys.config_param` model.

### Before you start

You need a bearer token whose user may write `sys.config_param`. To update a setting you need its `id`; find it with `search_read` (see "Get System Config").

### Request

Create a setting:

```http
POST /rpc/sys.config_param/create
Authorization: Bearer <token>
Content-Type: application/json
```

Update an existing setting by id:

```http
POST /rpc/sys.config_param/write/{id}
Authorization: Bearer <token>
Content-Type: application/json
```

| Body (`kwargs.values`) | Type | Required | What it means |
| --- | --- | --- | --- |
| `name` | string | On create | Unique dot-path key, e.g. `edc.device_http_auth`. |
| `value` | any JSON | Yes | The new value, e.g. `"token"`. Booleans, numbers, strings and arrays keep their type. |

### Example

```bash
curl -X POST "$BASE_URL/rpc/sys.config_param/write/12" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"kwargs":{"values":{"value":"token"}}}'
```

### Response

```json
{ "id": "111020261535481234570001", "result": true }
```

`write` returns `true`. `create` returns the new row.

### Errors

Errors use the standard format described in the API guide (see "Response" → "Errors").

| HTTP | `error.code` | What it means | What you should do |
| --- | --- | --- | --- |
| `400` | `-32602` | Missing `values`, duplicate `name`, or invalid id. | Fix the body. |
| `401` | `-32001` | Missing or invalid token. | Get a new token. |
| `403` | `-32003` | The user may not write `sys.config_param`. | Ask an admin for access. |
| `404` | `-32601` | You called `PUT /base/system-config`, which does not exist. | Use the RPC calls above. |

### Good to know

- Settings are loaded into memory when the service starts. Most changes (database, MQTT client, Kafka, server, EDC settings) only apply after a restart. A few are read each time they are used, for example `attachment.max_size_mb`.
- Base HUB has no "restart required" list like bedc had.
