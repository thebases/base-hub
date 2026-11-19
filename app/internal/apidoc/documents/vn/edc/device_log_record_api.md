## Record Device Log (internal)

Tạo một dòng device log từ các giá trị bạn đã có. Dùng từ workflow, automation hoặc code phía server khác. API này không nhận file; terminal upload file bằng `POST /base/device/log/upload`.

### Trước khi bắt đầu

- Bạn cần bearer token (xem API guide) của user có quyền ghi device log (`edc.device.log`).
- Không chấp nhận device token, chỉ dùng user token.

### Yêu cầu

```http
POST /base/edc/device/log/record
Authorization: Bearer <token>
Content-Type: application/json
```

API này giống lệnh RPC `POST /rpc/edc.device.log/record`. Body có `args` (để `[]`) và `kwargs` (các giá trị bên dưới, dạng snake_case).

| Key trong `kwargs` | Kiểu | Bắt buộc | Ý nghĩa |
| --- | --- | --- | --- |
| `device_serial` | string | Có | Serial mà log thuộc về, ví dụ `SN0001`. Tự cắt khoảng trắng. Không kiểm tra với danh sách thiết bị. |
| `message` | string | Có điều kiện | Nội dung log, ví dụ `reader timeout`. Bắt buộc trừ khi có `original_file_name`; khi đó mặc định là `uploaded device log file <original_file_name>`. |
| `level` | string | Không | Mức log, ví dụ `WARN`. Rỗng thì không lưu. |
| `tag` | string | Không | Nhóm, ví dụ `payment`. Mặc định `file` khi có `original_file_name`. |
| `metadata` | object | Không | Object JSON bất kỳ. Kiểu khác sẽ được lưu thành `{}`. |
| `logged_at` | string hoặc integer | Không | Thời điểm xảy ra: text RFC 3339 hoặc Unix seconds. Mặc định là bây giờ (UTC). Giá trị không đọc được sẽ bị bỏ qua. |
| `original_file_name`, `stored_file_name`, `file_extension`, `content_type`, `compression_type`, `storage_provider`, `storage_bucket`, `storage_path`, `storage_url`, `bank_bincode` | string | Không | Thông tin về file. Lưu đúng như gửi, không kiểm tra hay di chuyển gì. |
| `file_size` | integer | Không | Kích thước file tính bằng byte. Chấp nhận chuỗi số; giá trị khác bị bỏ qua. |

### Ví dụ

```bash
curl -X POST "$BASE_URL/base/edc/device/log/record" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"args": [], "kwargs": {"device_serial": "SN0001", "level": "WARN", "tag": "payment", "message": "reader timeout", "metadata": {"appVersion": "1.3.2"}}}'
```

### Phản hồi

`200 OK`. `result` là dòng mới tạo với tên field snake_case.

```json
{
  "id": "111020261535481234570001",
  "result": {
    "id": 312,
    "device_serial": "SN0001",
    "level": "WARN",
    "tag": "payment",
    "message": "reader timeout",
    "metadata": { "appVersion": "1.3.2" },
    "logged_at": "2026-10-11T08:00:00Z"
  }
}
```

Dòng này có thể có thêm các field audit như `created_at`.

### Lỗi

Lỗi dùng định dạng chuẩn được mô tả trong API guide (xem "Response" → "Errors").

| HTTP | `error.code` | Ý nghĩa | Bạn nên làm gì |
| --- | --- | --- | --- |
| `400` | `-32602` | `device_serial` rỗng (`device_serial is required`). | Gửi serial. |
| `400` | `-32602` | Không có `message` và cũng không có `original_file_name`. | Gửi một trong hai. |
| `400` | `-32000` | Không tạo được dòng log. | Đọc `message`. |
| `401` | `-32001` | Thiếu hoặc sai token. | Lấy token mới. |
| `403` | `-32003` | User không có quyền ghi device log. | Nhờ admin cấp quyền. |

### Cần biết

- Không idempotent: mỗi lần gọi tạo một dòng mới. Đừng retry mù quáng.
- Các field file chỉ là tham chiếu. Không có gì được upload hay kiểm tra.
