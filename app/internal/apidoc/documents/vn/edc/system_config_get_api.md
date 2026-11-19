## Lấy cấu hình hệ thống

`GET /base/system-config` không được triển khai trong Base HUB (trả về `404`). Base HUB không có một tài liệu "runtime config" duy nhất. Hãy đọc từng cài đặt từ model `sys.config_param`.

### Trước khi bắt đầu

Bạn cần bearer token của user có quyền đọc `sys.config_param`. Chỉ cấp quyền này cho admin đáng tin: các dòng không bị che và có thể chứa bí mật như khóa và mật khẩu.

### Request

```http
POST /rpc/sys.config_param/search_read
Authorization: Bearer <token>
Content-Type: application/json
```

Mỗi cài đặt là một dòng. `name` là key dạng đường dẫn có dấu chấm, ví dụ `attachment.max_size_mb`. `value` là một giá trị JSON bất kỳ. Gửi các tham số này trong `kwargs`:

| Tên | Kiểu | Bắt buộc | Ý nghĩa |
| --- | --- | --- | --- |
| `domain` | array | Không | Bộ lọc, ví dụ `[["name","ilike","edc."]]` tìm tên chứa `edc.`. |
| `fields` | string[] | Không | Các cột cần trả về, ví dụ `["name","value"]`. |
| `limit` / `offset` | integer | Không | Phân trang, ví dụ `20` và `0`. |

### Ví dụ

```bash
curl -X POST "$BASE_URL/rpc/sys.config_param/search_read" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"kwargs":{"domain":[["name","ilike","edc."]],"fields":["name","value"]}}'
```

### Response

```json
{
  "id": "111020261535481234570001",
  "result": [
    { "name": "edc.device_http_auth", "value": "public" }
  ]
}
```

| Trường | Ý nghĩa |
| --- | --- |
| `id` | Id của request (do bạn gửi, hoặc server tự tạo). |
| `result` | Các cài đặt khớp. |
| `result[].name` | Key của cài đặt. |
| `result[].value` | Giá trị cài đặt (string, number, boolean, array...). |

### Lỗi

Lỗi dùng định dạng chuẩn được mô tả trong hướng dẫn API (xem "Response" → "Errors").

| HTTP | `error.code` | Ý nghĩa | Bạn nên làm gì |
| --- | --- | --- | --- |
| `401` | `-32001` | Thiếu hoặc sai token. | Lấy token mới. |
| `403` | `-32003` | User không có quyền với `sys.config_param`. | Xin admin cấp quyền. |
| `404` | `-32601` | Bạn gọi `GET /base/system-config`, route này không tồn tại. | Dùng lệnh RPC ở trên. |

### Cần biết

- Không bao giờ ghi log hoặc chia sẻ toàn bộ response: nó có thể chứa bí mật.
- Module EDC đọc, trong số khác, `edc.device_http_auth` (`public` hoặc `token`) và `edc.provisioning.activation_code_ttl_minutes`. Bật/tắt provisioning và khóa mã hóa là `app.deviceProvisioningEnable` và `app.encryptKey`. Biến môi trường không được đọc.
- Để xem sức khỏe dịch vụ và trạng thái các thành phần phụ thuộc, dùng `GET /health/detail` (xem "Lấy feature flags").
