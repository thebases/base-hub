## Metrics cho Prometheus

Công khai các số liệu của dịch vụ (request, bộ nhớ, goroutine...) theo định dạng văn bản Prometheus, để Prometheus server hoặc Grafana agent thu thập (scrape). Công cụ giám sát dùng route này, không phải frontend.

### Request

```http
GET /metrics
```

Không cần đăng nhập. Route nằm ở base path của server.

### Ví dụ

```bash
curl "$BASE_URL/metrics"
```

### Response

Content type là `text/plain` (định dạng Prometheus), không phải JSON:

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

Mỗi metric có một dòng `# HELP` (nó là gì) và một dòng `# TYPE` (counter hoặc gauge), rồi một hoặc nhiều giá trị.

### Lỗi

Không có lỗi ứng dụng. Route luôn trả về metrics hiện tại.

### Cần biết

- Bạn nhận được metrics chuẩn của Go runtime và tiến trình, cộng với `http_requests_total{method,status}` và `http_request_duration_seconds{method}` cho mọi request HTTP, và `ipn_requests_total{provider}` cùng `ipn_request_duration_seconds{provider}` cho callback IPN (thông báo thanh toán).
- Route không cần đăng nhập. Ở production, hãy giới hạn truy cập ở tầng mạng hoặc reverse proxy.
