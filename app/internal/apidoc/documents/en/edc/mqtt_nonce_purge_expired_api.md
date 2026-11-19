## Purge Expired MQTT Nonces (internal)

Deletes old rows from the replay-protection ledger. A nonce is a one-time random value that each secure message carries; the server remembers it so the same message cannot be replayed. Old nonces can be forgotten. A cron job (`edc.purge_expired_nonces`) already does this regularly. Call this endpoint only if an operator wants to clean up right now.

### Before you start

You need a bearer token and **write** permission on `edc.mqtt.nonce` (this method is not read-only).

### Request

```http
POST /base/edc/mqtt/nonce/purge-expired
Authorization: Bearer <token>
Content-Type: application/json
```

Also available as `POST /rpc/edc.mqtt.nonce/purge_expired`. The body is the standard RPC envelope:

| Name | Type | Required | What it means |
| --- | --- | --- | --- |
| `kwargs.retention_hours` | integer | No | Delete rows older than this many hours, e.g. `48`. Default `24`. Zero, negative or non-numeric values fall back to `24`. `retentionHours` also works. |

### Example

```bash
curl -X POST "$BASE_URL/base/edc/mqtt/nonce/purge-expired" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"args": [], "kwargs": {"retention_hours": 48}}'
```

### Response

```json
{ "id": "111020261535481234570001", "result": { "deleted": 120 } }
```

| Field | What it means |
| --- | --- |
| `result.deleted` | How many nonce rows were deleted. |

### Errors

Errors use the standard format described in the API guide (see "Response" → "Errors").

| HTTP | `error.code` | What it means | What you should do |
| --- | --- | --- | --- |
| `401` | `-32001` | Missing or invalid token. | Get a new token. |
| `403` | `-32003` | You cannot write `edc.mqtt.nonce` (`permission denied`). | Ask an admin. |
| `400` | `-32000` | The EDC service is not loaded yet (`edc transport service is not initialized`), or the delete failed (database error text). | Retry later; read `message`. |

### Good to know

- It is safe to call repeatedly.
- Nonces older than the replay window can no longer match, so deleting them does not weaken replay protection. Do not set `retention_hours` below the secure-event replay window.
- The cutoff is compared with each row's `created_at`.
