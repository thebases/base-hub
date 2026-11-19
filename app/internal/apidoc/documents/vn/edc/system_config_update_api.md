## Cập nhật cấu hình hệ thống

`PUT /base/system-config` không được triển khai trong Base HUB (trả về `404`). Bạn không thể thay toàn bộ cấu hình trong một lần gọi. Hãy đổi từng cài đặt một trong model `sys.config_param`.

### Trước khi bắt đầu

Bạn cần bearer token của user có quyền ghi `sys.config_param`. Để cập nhật một cài đặt bạn cần `id` của nó; tìm bằng `search_read` (xem "Lấy cấu hình hệ thống").

### Request

Tạo một cài đặt:

```http
POST /rpc/sys.config_param/create
Authorization: Bearer <token>
Content-Type: application/json
```

Cập nhật cài đặt có sẵn theo id:

```http
POST /rpc/sys.config_param/write/{id}
Authorization: Bearer <token>
Content-Type: application/json
```

| Body (`kwargs.values`) | Kiểu | Bắt buộc | Ý nghĩa |
| --- | --- | --- | --- |
| `name` | string | Khi tạo | Key dạng đường dẫn có dấu chấm, duy nhất, ví dụ `edc.device_http_auth`. |
| `value` | JSON bất kỳ | Có | Giá trị mới, ví dụ `"token"`. Boolean, number, string và array giữ nguyên kiểu. |

### Ví dụ

```bash
curl -X POST "$BASE_URL/rpc/sys.config_param/write/12" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"kwargs":{"values":{"value":"token"}}}'
```

### Response

```json
{ "id": "111020261535481234570001", "result": true }
```

`write` trả về `true`. `create` trả về dòng vừa tạo.

### Lỗi

Lỗi dùng định dạng chuẩn được mô tả trong hướng dẫn API (xem "Response" → "Errors").

| HTTP | `error.code` | Ý nghĩa | Bạn nên làm gì |
| --- | --- | --- | --- |
| `400` | `-32602` | Thiếu `values`, `name` bị trùng, hoặc id không hợp lệ. | Sửa body. |
| `401` | `-32001` | Thiếu hoặc sai token. | Lấy token mới. |
| `403` | `-32003` | User không có quyền ghi `sys.config_param`. | Xin admin cấp quyền. |
| `404` | `-32601` | Bạn gọi `PUT /base/system-config`, route này không tồn tại. | Dùng các lệnh RPC ở trên. |

### Cần biết

- Cài đặt được nạp vào bộ nhớ khi dịch vụ khởi động. Hầu hết thay đổi (database, MQTT client, Kafka, server, cài đặt EDC) chỉ có hiệu lực sau khi khởi động lại. Một vài cài đặt được đọc mỗi lần dùng, ví dụ `attachment.max_size_mb`.
- Base HUB không có danh sách "cần khởi động lại" như bedc.
