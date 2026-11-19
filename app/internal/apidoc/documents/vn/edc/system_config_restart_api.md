## Khởi động lại dịch vụ

`POST /base/system-config/restart` không được triển khai trong Base HUB (trả về `404`). Không thể khởi động lại dịch vụ qua HTTP.

### Dùng gì thay thế

- **Khởi động lại toàn bộ dịch vụ:** dùng supervisor hoặc công cụ deploy (systemd, Docker, Kubernetes). Hãy tính đến thời gian ngừng: HTTP, MQTT của thiết bị và các job nền dừng cho đến khi dịch vụ chạy lại.
- **Nạp lại một module:** trong giao diện admin, mở màn hình Apps và dùng hành động **Reinit** trên dòng. Nó chờ server xử lý xong và không khởi động lại tiến trình.
- **Kiểm tra dịch vụ đã chạy lại:** gọi `GET /health` (không cần đăng nhập).

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
| `status` | `healthy` nghĩa là HTTP server đang chạy. |

### Lỗi

Lỗi dùng định dạng chuẩn được mô tả trong hướng dẫn API (xem "Response" → "Errors").

| HTTP | `error.code` | Ý nghĩa | Bạn nên làm gì |
| --- | --- | --- | --- |
| `404` | `-32601` | Bạn gọi `POST /base/system-config/restart`, route này không tồn tại. | Khởi động lại bằng công cụ deploy. |

### Cần biết

- `/health` không kiểm tra database hay MQTT. Để kiểm tra, dùng `GET /health/detail` (cần đăng nhập; xem "Lấy feature flags").
