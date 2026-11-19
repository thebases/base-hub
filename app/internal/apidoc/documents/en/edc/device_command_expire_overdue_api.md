## Expire Overdue Device Commands

Marks commands as `TIMEOUT` when the terminal never answered them in time. A background job already does this automatically; call this endpoint only when you want to run the sweep right now (for example in a support script or a test).

### Before you start

- You need a bearer token (see the API guide).
- Your user needs **write** permission on `edc.device.command` (the model that stores commands). Without it you get `403`.

### Request

```http
POST /base/edc/device/command/expire-overdue
Authorization: Bearer <token>
Content-Type: application/json
```

There are no parameters. Send `{}`. The same method is also available as `POST /rpc/edc.device.command/expire_overdue`.

### Example

```bash
curl -X POST "$BASE_URL/base/edc/device/command/expire-overdue" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" -d '{}'
```

### Response

```json
{ "id": "111020261535481234570001", "result": { "expired": 3 } }
```

| Field | What it means |
| --- | --- |
| `result.expired` | How many commands were moved to `TIMEOUT` by this call. `0` means nothing was overdue. |

### Errors

Errors use the standard format described in the API guide (see "Response" → "Errors").

| HTTP | `error.code` | What it means | What you should do |
| --- | --- | --- | --- |
| `401` | `-32001` | Missing or invalid token. | Get a new token. |
| `403` | `-32003` | Your user cannot write `edc.device.command`. | Ask an admin for the permission. |
| `400` / `503` | `-32000` / `-32050` | The database is unavailable or the update failed. | Read `message` and retry later. |

### Good to know

- A command is swept only if all of these are true: status is `CREATED` or `PUBLISHED`, the device never acknowledged it, and its `expired_at` time is more than 2 minutes in the past. The command gets `error_code = device_no_response`.
- `ACKED` commands are never swept. An accepted payment command may legitimately run longer than its expiry time.
- `completed_at` is left empty on purpose. If the terminal replies late, the real result is still recorded (see "Record Device Command Response").
- It is safe to call this repeatedly. The automatic job is the cron job `edc.expire_overdue_commands`.
