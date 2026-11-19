## Prometheus Metrics

Exposes service numbers (requests, memory, goroutines...) in Prometheus text format, so a Prometheus server or Grafana agent can scrape them. Monitoring tools use it, not frontends.

### Request

```http
GET /metrics
```

No login is needed. It is served at the server's base path.

### Example

```bash
curl "$BASE_URL/metrics"
```

### Response

The content type is `text/plain` (Prometheus format), not JSON:

```text
# HELP go_goroutines Number of goroutines that currently exist.
# TYPE go_goroutines gauge
go_goroutines 42
# HELP http_requests_total Total number of HTTP requests processed
# TYPE http_requests_total counter
http_requests_total{method="GET",status="200"} 5120
# HELP ipn_requests_total Total number of IPN requests received
# TYPE ipn_requests_total counter
ipn_requests_total{provider="card"} 128
# HELP process_resident_memory_bytes Resident memory size in bytes.
# TYPE process_resident_memory_bytes gauge
process_resident_memory_bytes 5.4263808e+07
```

Each metric has a `# HELP` line (what it is) and a `# TYPE` line (counter or gauge), then one or more values.

### Errors

There are no application errors. The route always returns the current metrics.

### Good to know

- You get standard Go runtime and process metrics, plus `http_requests_total{method,status}` and `http_request_duration_seconds{method}` for every HTTP request, and `ipn_requests_total{provider}` and `ipn_request_duration_seconds{provider}` for IPN (payment notification) callbacks.
- The route has no login. In production, limit it at the network or reverse-proxy level.
