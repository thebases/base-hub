## Get MQTT Message

Returns one recorded MQTT message (a message the server sent to a terminal, or received from one), with the parsed and the raw payload. Use it when you need the exact text that went over the wire.

### Before you start

You need a bearer token and **read** permission on `edc.mqtt.message`. Get the numeric `id` from "List MQTT Messages".

### Request

```http
GET /base/mqtt-messages/{id}
Authorization: Bearer <token>
```

| Name | Type | Required | What it means |
| --- | --- | --- | --- |
| `id` (path) | integer | Yes | Message id, for example `90413`. |

### Example

```bash
curl "$BASE_URL/base/mqtt-messages/90413" \
  -H "Authorization: Bearer $TOKEN"
```

### Response

```json
{
  "id": "111020261535481234570001",
  "result": {
    "id": 90413,
    "serialNumber": "SN0001",
    "direction": "OUTBOUND",
    "topic": "base/default/SN0001/cmd",
    "payload": { "v": 1, "c": 200, "i": "6f1c2b8e-3a4d-4f5e-9b7a-1c2d3e4f5a6b", "s": 1790412001, "e": 60, "u": "backend", "h": "9f2c...e71a", "p": {} },
    "rawPayload": "{\"v\":1,\"c\":200,\"i\":\"6f1c2b8e-3a4d-4f5e-9b7a-1c2d3e4f5a6b\",\"s\":1790412001,\"e\":60,\"u\":\"backend\",\"h\":\"9f2c...e71a\",\"p\":{}}",
    "qos": 1,
    "retained": false,
    "sync": false,
    "receivedAt": 1790409601
  }
}
```

| Field | What it means |
| --- | --- |
| `serialNumber` | Terminal serial. Left out when the message had no recognizable serial. |
| `direction` | `OUTBOUND` (server to terminal) or `INBOUND` (terminal to server). |
| `topic` | The MQTT topic (the "channel name" the message used). |
| `payload` | The message as parsed JSON. Left out when empty. |
| `rawPayload` | The exact text on the wire. Use it when you need the original formatting. Left out when empty. |
| `qos` | Delivery level. `1` means "at least once". |
| `retained` | Whether the broker keeps the message for new subscribers. |
| `sync` | Always `false` today: Base HUB does not forward messages to webhooks. |
| `receivedAt` | When the message was recorded (Unix seconds). |

### Errors

Errors use the standard format described in the API guide (see "Response" → "Errors").

| HTTP | `error.code` | What it means | What you should do |
| --- | --- | --- | --- |
| `400` | `-32602` | `id` is not an integer. | Use the numeric id. |
| `401` | `-32001` | Missing or invalid token. | Get a new token. |
| `403` | `-32003` | You have no read access to `edc.mqtt.message`. | Ask an admin. |
| `404` | `-32004` | No message has this `id`. | Check the id. |
| `500` | `-32603` | Unexpected server error. | Retry; report the response `id`. |

### Good to know

- For `OUTBOUND` rows, `rawPayload` is the signed command the backend published. It is saved in the same transaction that marks the command `PUBLISHED`.
