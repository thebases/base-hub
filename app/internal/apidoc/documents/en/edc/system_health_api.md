## Health Check

Tells you whether the service is up and answering HTTP. Use it for load balancer and uptime checks.

### Request

```http
GET /health
```

No login is needed. It is served at the server's base path (`/` by default).

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
| `status` | Always `healthy` while the HTTP server is running. |

### Errors

There are no application errors. If you get no response, the service is down.

### Good to know

- A `200` only means the HTTP server is alive. It does not check the database, MQTT broker or file storage.
- For database, Kafka and MQTT state call `GET /health/detail` with a bearer token (see "Get Feature Flags").
- Base HUB has no `/livez` or `/readyz`, and bedc's longer health document is not provided.
