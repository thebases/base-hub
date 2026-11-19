## List MQTT Messages

Returns a page of the MQTT messages exchanged between the server and terminals, both received (`INBOUND`) and sent (`OUTBOUND`), newest first. Use it to troubleshoot what a terminal sent or received.

### Before you start

You need a bearer token and **read** permission on `edc.mqtt.message`.

### Request

```http
GET /base/mqtt-messages?serial={serial}&direction={direction}&topic={topic}&sync={sync}&limit={limit}&offset={offset}
Authorization: Bearer <token>
```

All parameters are optional. A blank value is ignored.

| Name | Type | Required | What it means |
| --- | --- | --- | --- |
| `serial` | string | No | Terminal serial. Partial, case-insensitive match, e.g. `SN0001`. |
| `direction` | string | No | `INBOUND` or `OUTBOUND`. Upper-cased before matching, so `inbound` works. |
| `topic` | string | No | MQTT topic (channel name). Partial, case-insensitive match, e.g. `txn` or `SN0001/cmd`. |
| `sync` | boolean | No | `true` / `false` (or `1` / `0`). Filters on the stored `sync` flag, which is always `false` today. |
| `limit` | integer | No | Page size. Default `50`, maximum `500`. A value above 500, zero or non-numeric falls back to `50`. |
| `offset` | integer | No | How many rows to skip. Default `0`. |

### Example

```bash
curl "$BASE_URL/base/mqtt-messages?serial=SN0001&direction=INBOUND&limit=20" \
  -H "Authorization: Bearer $TOKEN"
```

### Response

```json
{
  "id": "111020261535481234570001",
  "result": {
    "items": [
      {
        "id": 90412,
        "serialNumber": "SN0001",
        "direction": "INBOUND",
        "topic": "base/txn",
        "payload": { "serial": "SN0001", "c": 1102, "p": { "amount": 150000 } },
        "rawPayload": "{\"serial\":\"SN0001\",\"c\":1102,\"p\":{\"amount\":150000}}",
        "qos": 1,
        "retained": false,
        "sync": false,
        "receivedAt": 1790409600
      }
    ],
    "total": 1
  }
}
```

| Field | What it means |
| --- | --- |
| `result.items` | Messages of this page, newest first (by `receivedAt`). Each item has the fields described in "Get MQTT Message". |
| `result.total` | How many messages match your filters (ignores `limit` and `offset`). |

### Errors

Errors use the standard format described in the API guide (see "Response" → "Errors").

| HTTP | `error.code` | What it means | What you should do |
| --- | --- | --- | --- |
| `400` | `-32602` | `sync` is not a boolean (`error.data.reason` = `invalid_sync`). | Send `true` or `false`. |
| `401` | `-32001` | Missing or invalid token. | Get a new token. |
| `403` | `-32003` | You have no read access to `edc.mqtt.message`. | Ask an admin. |
| `500` | `-32603` | Unexpected server error. | Retry; report the response `id`. |

### Good to know

- A message with no recognizable serial cannot be found with `serial`. Use `topic` instead.
- An unknown `direction` returns an empty list, not an error.
- Command topics look like `base/<group name>/<serial>/cmd` (or `<serial>/cmd` for terminals behind the launcher bridge). Terminals send events on `base/evt` and `base/txn`. Filter `topic=/cmd` to see only commands sent by the server.
- Also available as `POST /base/edc/mqtt/message/list` (RPC `POST /rpc/edc.mqtt.message/list_messages`). Send the filters in `kwargs` (`serial`, `direction`, `topic`, `sync`, `limit`, `offset`). The items use snake_case field names. A caller without read access gets `{"result": null}` instead of `403`, and an invalid `sync` returns `400` with a plain-text message.
