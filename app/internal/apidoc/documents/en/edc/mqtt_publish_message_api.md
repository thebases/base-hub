## Publish MQTT Message

Sends one command to one terminal, using the old `{topic, message}` request shape. Use it for testing, or for clients that still call the old route. New code should call `POST /base/devices/{serial}/commands` instead (see "Device Commands API"). This route is a thin wrapper around it: the command is validated, signed, saved, published and tracked exactly like any other command.

### In a nutshell

1. You send a `topic` (only used to find the terminal) and a `message` with the command code.
2. The backend ignores everything in `message` except `c`, `p` and `e`. It builds and signs its own message with a new request id.
3. It saves the command, publishes it to the broker, and answers `202`.
4. `202` means the broker accepted the message. It does **not** mean the terminal received or ran it.

### Before you start

- You need a bearer token and **write** permission on `edc.device.command`.
- The terminal must already exist and have a signing secret.
- The broker connection must be up. Otherwise you get `503` when you publish.

### Request

```http
POST /mqtt/publish
Authorization: Bearer <token>
Content-Type: application/json
```

| Name | Type | Required | What it means |
| --- | --- | --- | --- |
| `topic` | string | Yes | A terminal command topic. It is only used to pick the terminal: `base/<group name>/<serial>/cmd`, `base/<serial>/cmd` or `<serial>/cmd`, e.g. `base/default/SN0001/cmd`. Spaces at both ends are trimmed. Any other topic is rejected. |
| `message` | object | Yes | The command as a JSON object (a string containing a JSON object is also accepted). Must not be `null`. Keys are listed below. |

Keys read from `message` (short v1.3 names or long names):

| Key | Aliases | Required | What it means |
| --- | --- | --- | --- |
| `c` | `command_code`, `commandCode` | Yes | Command code, one of the supported codes listed in "Device Commands API" (for example `100`, `112`, `200`). |
| `p` | `payload` | No | Command data object. Send exactly what the terminal expects for that code. |
| `e` | `ttl_seconds`, `ttlSeconds` | No | Time to live in seconds, `30` to `3600`. Default `60`. After this time the terminal rejects the command as expired. |

All other keys, including `v`, `i`, `s`, `u` and `h` (version, request id, time, sender, signature), are ignored. The backend always builds a new message, picks a new request id (`i`) and signs it with the terminal's own secret. A caller can neither forge nor replay a command.

### Example

```bash
curl -X POST "$BASE_URL/mqtt/publish" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"topic":"base/default/SN0001/cmd","message":{"c":200,"p":{},"e":60}}'
```

### Response

HTTP `202`. This body is **not** wrapped in `{"id","result"}`.

```json
{
  "status": "accepted",
  "topic": "base/default/SN0001/cmd",
  "message": { "c": 200, "p": {}, "e": 60 },
  "qos": 1,
  "brokerAcked": true,
  "deliveryState": "accepted_by_broker"
}
```

| Field | What it means |
| --- | --- |
| `status` | Always `accepted`. |
| `topic` | The topic that was really used: the terminal's current group name, or `<serial>/cmd` for launcher-bridge terminals. It can differ from the `topic` you sent. |
| `message` | Your `message`, echoed exactly as you sent it. |
| `qos` | Always `1` (delivered at least once). The message is never retained. |
| `brokerAcked` | `true`: the broker confirmed it received the message. |
| `deliveryState` | `accepted_by_broker` on success. |

To follow the command afterwards, call `GET /base/device/command?serial=SN0001` or `GET /base/mqtt-messages`.

### Errors

Errors use the standard format described in the API guide (see "Response" → "Errors"). The old text of the error is in `error.message`. For broker failures, `error.data` also contains `topic`, `deliveryState` and `qos`, for example `{"deliveryState": "mqtt_client_unavailable", "qos": 1, "topic": "base/default/SN0001/cmd"}`.

| HTTP | `error.message` | What it means | What you should do |
| --- | --- | --- | --- |
| `400` | `invalid_json` | Body is not valid JSON. | Fix the body. |
| `400` | `Both topic and message are required` | `topic` is empty, or `message` is missing or `null`. | Send both. |
| `400` | `message must be a JSON object (a v1.3 command envelope)` | `message` is not an object. | Send an object. |
| `400` | `topic must be base/<group>/<serial>/cmd, base/<serial>/cmd or <serial>/cmd` | Not a terminal command topic. | Fix the topic. |
| `400` | `command_code is required to publish a v1.3 command` | `message` has no `c`. | Add `c`. |
| `400` | `command code is not supported by the current lite payment runtime` | Unsupported command code. | Use a supported code. |
| `400` | `ttl_seconds must be between 30 and 3600` | Invalid `e`. | Use 30 to 3600. |
| `400` | `payload.id is required for this command code` | Code `109`, `113` or `114` without `p.id`. | Add `p.id`. |
| `401` | | Missing or invalid token. | Get a new token. |
| `403` | `forbidden` | No write access to `edc.device.command`. | Ask an admin. |
| `404` | `device not found` | No terminal has the serial from `topic`. | Check the serial (upper-case). |
| `409` | `device command config missing: ...` | The terminal has no usable signing secret. | Provision or configure the terminal first. |
| `500` | `edc provisioning secret encryption key not configured (app.encryptKey)` | The server cannot decrypt the terminal secret. | Contact the platform team. |
| `503` | `mqtt client unavailable` | The server is not connected to the broker (`deliveryState: mqtt_client_unavailable`). | Retry later. |
| `504` | `mqtt publish timeout waiting for broker ack: ...` | The broker did not confirm in time (`broker_ack_timeout`). | See the warning below. |
| `502` | `mqtt broker rejected publish: ...` | The broker refused the publish, for example by its own ACL (`broker_rejected_publish`). | Check the broker ACL. |
| `500` | error text | Any other publish failure (`publish_failed`), or an unclassified error such as a database failure (then there is no `deliveryState`). | Retry; report the response `id`. |

> **Warning:** after a `504` the message may still have been delivered. The command row is `FAILED`, but a late terminal reply can still record the real result. Do not blindly retry money-moving commands (`100`-`106`).

### Good to know

- The old route published any JSON to any topic. That is no longer possible: the topic only identifies the terminal, and only supported commands are sent.
- The command is saved before publishing. If publishing fails, the row stays and is marked `FAILED` with an `errorCode` (see "Get Device Command").
- `X-Idempotency-Key` is **not** honoured here. Every call creates a new command with a new request id.
- Also available as `POST /base/edc/device/command/publish` (RPC `POST /rpc/edc.device.command/publish`) with `topic` and `message` in `kwargs`. It replies `{"result": {...}}` with the snake_case command row plus `delivery_state` (not the `202` body above). Every failure is `400` with a plain-text message; no access gives `403`.
