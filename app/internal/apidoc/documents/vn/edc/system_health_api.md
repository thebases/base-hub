## Kiểm tra sức khỏe (Health Check)

Cho biết dịch vụ có đang chạy và trả lời HTTP hay không. Dùng cho kiểm tra của load balancer và uptime.

### Request

```http
GET /health
```

Không cần đăng nhập. Route nằm ở base path của server (mặc định `/`).

### Ví dụ

```bash
curl "$BASE_URL/health"
```

### Response

```json
{ "status": "healthy" }
```

| Trường | Ý nghĩa |
| --- | --- |
| `status` | Luôn là `healthy` khi HTTP server đang chạy. |

### Lỗi

Không có lỗi ứng dụng. Nếu không nhận được phản hồi, dịch vụ đang ngừng.

### Cần biết

- `200` chỉ nghĩa là HTTP server còn sống. Nó không kiểm tra database, MQTT broker hay kho lưu file.
- Để xem trạng thái database, Kafka và MQTT, gọi `GET /health/detail` với bearer token (xem "Lấy feature flags").
- Base HUB không có `/livez` hay `/readyz`, và tài liệu health dài của bedc không được cung cấp.
