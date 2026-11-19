## Lấy feature flags

`GET /config/features` không được triển khai trong Base HUB. Nó trả về route-not-found hoặc trang web app, không phải JSON. Không có tài liệu flag `oidc` / `rbac` / `s3` / `provisioning`. Hãy hỏi từng câu bằng cách ở dưới.

### Dùng gì thay thế

| Bạn muốn biết | Dùng |
| --- | --- |
| Đăng nhập còn hợp lệ không, tôi là ai | `GET /auth/me` (giống `/oauth/me`). Xem `result.valid`. |
| Database, MQTT và Kafka có chạy không | `GET /health/detail` (xem bên dưới). |
| Có những module và model nào | `GET /doc` (cần đăng nhập) liệt kê các `modules` đã cài. `GET /doc/openapi.json` liệt kê các route gọi được. |
| Provisioning thiết bị có bật không | Gọi `POST /device/provision`. Khi tắt nó trả `503 provisioning_disabled` (cài đặt `app.deviceProvisioningEnable`). |
| `X-API-Key` có được chấp nhận không | Cài đặt server `app.supportApikey` (mặc định tắt). Key bị từ chối trả `401`. |

### Trước khi bắt đầu

`GET /health/detail` cần bearer token, và user phải thuộc công ty gốc (root company) trên database chính (home database).

### Request

```http
GET /health/detail
Authorization: Bearer <token>
```

### Ví dụ

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

| Trường | Ý nghĩa |
| --- | --- |
| `status` | `healthy`, hoặc `degraded` khi kiểm tra database, Kafka hoặc MQTT là `down`. |
| `uptime_seconds` | Số giây từ lúc dịch vụ khởi động. |
| `checks.<name>.status` | `up`, `down` hoặc `disabled`. |
| `checks.<name>.enabled` | Thành phần này có được bật trong cấu hình server không. |
| `stats` | Tổng số request và lỗi từ lúc khởi động. |

Route này giữ body riêng, không dùng envelope `{"id","result"}`.

### Lỗi

Lỗi ở đây là body văn bản thuần kèm HTTP status.

| HTTP | Ý nghĩa | Bạn nên làm gì |
| --- | --- | --- |
| `401` | Thiếu hoặc sai token. | Lấy token mới. |
| `403` | Token không thuộc database chính, hoặc user không thuộc công ty gốc. | Dùng user của công ty gốc trên database chính. |

### Cần biết

- Đừng gọi `GET /config/features` từ frontend. Route này không tồn tại ở đây.
- `GET /health` vẫn công khai và chỉ trả về `{"status":"healthy"}`.
