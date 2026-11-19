## Get Feature Flags

`GET /config/features` is not implemented in Base HUB. It returns a route-not-found response or the web app page, not JSON. There is no `oidc` / `rbac` / `s3` / `provisioning` flag document. Ask each question in the way shown below.

### What to use instead

| You want to know | Use |
| --- | --- |
| Is my login valid, who am I | `GET /auth/me` (same as `/oauth/me`). Check `result.valid`. |
| Are the database, MQTT and Kafka up | `GET /health/detail` (see below). |
| Which modules and models exist | `GET /doc` (login needed) lists the installed `modules`. `GET /doc/openapi.json` lists callable routes. |
| Is device provisioning on | Call `POST /device/provision`. It answers `503 provisioning_disabled` when off (setting `app.deviceProvisioningEnable`). |
| Is `X-API-Key` accepted | Server setting `app.supportApikey` (default off). A rejected key gives `401`. |

### Before you start

`GET /health/detail` needs a bearer token, and the user must belong to the root company on the home database.

### Request

```http
GET /health/detail
Authorization: Bearer <token>
```

### Example

```bash
curl "$BASE_URL/health/detail" -H "Authorization: Bearer $TOKEN"
```

### Response

```json
{
  "status": "healthy",
  "version": "1.0.0",
  "uptime_seconds": 86400,
  "timestamp": "2026-10-11T08:00:00Z",
  "checks": {
    "database": { "status": "up", "enabled": true, "latency_ms": 2 },
    "kafka": { "status": "disabled", "enabled": false },
    "mqtt": { "status": "up", "enabled": true }
  },
  "stats": { "requests_total": 1280, "errors_total": 3 }
}
```

| Field | What it means |
| --- | --- |
| `status` | `healthy`, or `degraded` when the database, Kafka or MQTT check is `down`. |
| `uptime_seconds` | Seconds since the service started. |
| `checks.<name>.status` | `up`, `down` or `disabled`. |
| `checks.<name>.enabled` | Whether this part is turned on in the server config. |
| `stats` | Total requests and errors since start. |

This route keeps its own body, not the `{"id","result"}` envelope.

### Errors

Errors here are plain-text bodies with the HTTP status.

| HTTP | What it means | What you should do |
| --- | --- | --- |
| `401` | Missing or invalid token. | Get a new token. |
| `403` | The token is not for the home database, or the user is not in the root company. | Use a root-company user on the home database. |

### Good to know

- Do not call `GET /config/features` from a frontend. It does not exist here.
- `GET /health` stays public and only returns `{"status":"healthy"}`.
