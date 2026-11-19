## Record Device Command Response

Saves a terminal's reply against the command it answers. It moves the command to `ACKED` (received) or to a final status (`SUCCESS`, `FAILED`, ...) and stores the reply. The system calls it automatically for every reply that arrives on MQTT. You call it yourself only to replay a reply you captured elsewhere, for example from device logs while the broker link was down.

### Before you start

- You need a bearer token and **write** permission on `edc.device.command`.
- You need the terminal's reply message. A reply is a small JSON object ("envelope") with `c` (reply code = the command code + 1000, so a reply to command `101` has `c=1101`), `i` (the `commandId` of the command), `sn` (serial) and `st` (status number, `0` = success).

### Request

```http
POST /base/edc/device/command/record-response
Authorization: Bearer <token>
Content-Type: application/json
```

Also available as `POST /rpc/edc.device.command/record_response` with the same body.

```json
{ "args": [], "kwargs": { "message": { "c": 1101, "i": "cmd-7f3a", "sn": "SN0001", "st": 0, "p": {} }, "lenient": false } }
```

| Name | Type | Required | What it means |
| --- | --- | --- | --- |
| `kwargs.message` | object or JSON string | Yes | The terminal's reply (EDC v1.3 envelope) with `c`, `i`, `sn`, `st` as described above. The key `response` is accepted as an alias for `message`. |
| `kwargs.lenient` | boolean | No | Default `false`. With `true`, a reply that cannot be applied (no `st`, no `i`/`sn`, bad JSON, no matching command) returns `{"ok": false, "reason": "..."}` instead of an error. This is how the MQTT workflow calls it. Database failures are always errors. `"true"`/`"false"` strings are accepted; any other value counts as `false`. |

### Example

```bash
curl -X POST "$BASE_URL/base/edc/device/command/record-response" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"kwargs":{"lenient":true,"message":{"c":1101,"i":"cmd-7f3a","sn":"SN0001","st":0,"p":{}}}}'
```

### Response

```json
{
  "id": "111020261535481234570001",
  "result": {
    "id": 42,
    "serial_number": "SN0001",
    "command_id": "cmd-7f3a",
    "status": "SUCCESS",
    "acked_at": 1791705601,
    "completed_at": 1791705603
  }
}
```

`result` is the command row after the update (shortened here). Field names are snake_case. In lenient mode a rejected reply returns `{"result": {"ok": false, "reason": "no matching device command"}}`.

### Errors

Errors use the standard format described in the API guide (see "Response" → "Errors").

| HTTP | `error.code` | What it means | What you should do |
| --- | --- | --- | --- |
| `400` | `-32000` / `-32602` | Strict mode only: `message` is missing or not valid JSON, the reply has no `st`, lacks `i` or `sn`, no command matches (`device command not found`), or a database error occurred. | Fix the message, or send `lenient: true`. |
| `401` | `-32001` | Missing or invalid token. | Get a new token. |
| `403` | `-32003` | You cannot write `edc.device.command`. | Ask an admin. |

### Good to know

- Replaying the same reply twice is harmless.
- A reply that matches no command, or whose `c` is not `command code + 1000`, is silently ignored. In strict mode this ends in the `device command not found` error, because the call re-reads the row.
- A reply with `rc` = `req_duplicate` is ignored.
- The first "received" reply (`st=1`, pending) moves `CREATED`/`PUBLISHED`/`TIMEOUT`/`FAILED` commands (with no `completed_at`) to `ACKED` and fills `acked_at`. A final reply sets the final status. Final statuses are sticky: a late acknowledgement never overwrites them. A genuine late reply does override a `TIMEOUT` set by the sweep.
- Resync replies (command `120`) that carry an item sequence are appended to the command's items. The command completes when all items have arrived.
