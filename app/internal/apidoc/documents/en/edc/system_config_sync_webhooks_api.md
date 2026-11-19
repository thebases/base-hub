## Sync MQTT Webhooks

`POST /base/system-config/sync-webhooks` is not implemented in Base HUB (it returns `404`). There is no backlog of undelivered MQTT messages to push on demand.

### What to use instead

| You want to | Use |
| --- | --- |
| See past terminal messages | `GET /base/mqtt-messages` (see `mqtt_message_list_api.md`). Its `sync` filter finds rows by sync state. |
| Forward device events to a partner | A Base HUB workflow. An active workflow with an MQTT trigger on `base/evt` or `base/txn` runs when a terminal message arrives. An active workflow topic is also callable as a webhook at `/wh/<topic>`. Delivery and retry are set on the workflow (`base.workflow`). |
| Send a command to a device again | Publish a new signed command with `POST /base/devices/{serial}/commands`. |

### Example

```bash
curl "$BASE_URL/base/mqtt-messages?serial=SN0001&limit=20" \
  -H "Authorization: Bearer $TOKEN"
```

### Response

```json
{ "result": { "items": [ ], "total": 0 }, "id": "111020261535481234570001" }
```

Rows are left out here. See `mqtt_message_list_api.md` for the row fields.

### Errors

Errors use the standard format described in the API guide (see "Response" → "Errors").

| HTTP | `error.code` | What it means | What you should do |
| --- | --- | --- | --- |
| `404` | `-32601` | You called `POST /base/system-config/sync-webhooks`, which does not exist. | Use one of the options above. |

### Good to know

- Do not build an integration that waits for a `sync-webhooks` job. Messages are delivered by workflow triggers as they arrive.
