## Restart Service

`POST /base/system-config/restart` is not implemented in Base HUB (it returns `404`). The service cannot be restarted over HTTP.

### What to use instead

- **Restart the whole service:** use your supervisor or deploy tool (systemd, Docker, Kubernetes). Plan for downtime: HTTP, MQTT device traffic and background jobs stop until it is back.
- **Reload one module:** in the admin UI, open the Apps screen and use the row action **Reinit**. It waits for the server to finish and does not restart the process.
- **Check it is back:** call `GET /health` (no login needed).

### Example

```bash
curl "$BASE_URL/health"
```

### Response

```json
{ "status": "healthy" }
```

| Field | What it means |
| --- | --- |
| `status` | `healthy` means the HTTP server is up. |

### Errors

Errors use the standard format described in the API guide (see "Response" → "Errors").

| HTTP | `error.code` | What it means | What you should do |
| --- | --- | --- | --- |
| `404` | `-32601` | You called `POST /base/system-config/restart`, which does not exist. | Restart with your deploy tool. |

### Good to know

- `/health` does not check the database or MQTT. For that use `GET /health/detail` (login needed; see "Get Feature Flags").
