## Preview Device Config Template

Shows the config a device would get if a given config template were assigned to it. It is the template's stored JSON with `cardConfig.tidmid` rebuilt from the template's linked bank apps. The admin "Copy from Device Config" action uses it. It assigns nothing.

### Before you start

- You need a bearer token (see the API guide) of a user who may read `edc.device.status`. A denied read returns `{"result": null}`, not `403`.
- You need the id of an **active** config template version.

### Request

```http
POST /base/edc/device/status/preview-template-config
Authorization: Bearer <token>
Content-Type: application/json
```

This is the same as the RPC call `POST /rpc/edc.device.status/preview_template_config`. The body is `{ "args": [], "kwargs": { "config_id": 14 } }`.

| Name | Type | Required | What it means |
| --- | --- | --- | --- |
| `kwargs.config_id` | integer | Yes | Id of an active config template version, e.g. `14`. `configId` is also accepted. |

### Example

```bash
curl -X POST "$BASE_URL/base/edc/device/status/preview-template-config" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"kwargs":{"config_id":14}}'
```

### Response

`200 OK`. `result` is a copy of the full config JSON (shortened here).

```json
{
  "result": {
    "owner": "THEBASE",
    "functions": { "qrFunc": true, "cardFunc": true },
    "cardConfig": { "tidmid": [ { "...": "..." } ] }
  }
}
```

### Errors

Errors use the standard format described in the API guide (see "Response" → "Errors").

| HTTP | What it means | What you should do |
| --- | --- | --- |
| `400` | `config_id` is missing or not positive (`config_id is required`), the template does not exist or is not active (`device config template is not active`), or a database error. | Send the id of an active template version. |
| `401` | Missing or invalid token. | Get a new token. |

### Good to know

- Only an active template can be previewed. This is the same rule as *Assign Device Config*, so the preview matches what the device gets.
- If the template has no linked bank apps, its own `cardConfig.tidmid` is returned unchanged.
- Keys that are added when a terminal reads its config (`owner`, `state`, `mqttConfig.mqttDeviceTopic`) are not added here.
- Safe to repeat. It only reads data.
