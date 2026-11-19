## Issue Device Activation Code

Tạo mã kích hoạt ngắn cho thiết bị, theo ID số. Mã được trả về một lần. Dùng cùng **Activate Device**.

> **Warning:** Đây không phải đường onboarding của máy. Máy provision bằng `POST /device/provision` với mã đặt qua `POST /base/device/activation` (xem **Create Device** để biết toàn bộ quy trình). Mã từ trang này chỉ dùng được với `POST /base/edc/devices/{id}/activate`.

### Trước khi bắt đầu

Cần bearer token của user có quyền ghi `edc.device`; nếu không sẽ nhận `403`. Token của thiết bị không được chấp nhận.

### Request

```http
POST /base/edc/devices/{id}/issue-activation-code
Authorization: Bearer <token>
Content-Type: application/json
```

Body bọc tham số trong `kwargs`.

| Tên | Vị trí | Kiểu | Bắt buộc | Ý nghĩa |
| --- | --- | --- | --- | --- |
| `id` | path | integer | Có | ID số của thiết bị, ví dụ `42` (không phải serial). Phải lớn hơn 0. |
| `kwargs.issued_by` | body | integer | Không | ID user ghi nhận là người cấp, ví dụ `7`. Mặc định `0`. Không lấy từ token. |

### Ví dụ

```bash
curl -X POST "$BASE_URL/base/edc/devices/42/issue-activation-code" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"args": [], "kwargs": {"issued_by": 7}}'
```

### Response

`200`:

```json
{
  "id": "111020261535481234570001",
  "result": {
    "code": "48213907",
    "expires_at": "2026-10-11T09:30:00Z"
  }
}
```

| Trường | Ý nghĩa |
| --- | --- |
| `result.code` | Mã 8 chữ số. **Chỉ hiện một lần.** |
| `result.expires_at` | Thời điểm mã hết hạn (30 phút sau khi cấp, UTC). |

> **Check:** `expires_at` có thể được trả dạng Unix seconds thay vì chuỗi RFC 3339. Hãy xác nhận bằng một lệnh gọi thật.

### Lỗi

Lỗi dùng định dạng chuẩn được mô tả trong hướng dẫn API (xem "Response" → "Errors").

| HTTP | `error.code` | Ý nghĩa | Bạn nên làm gì |
| --- | --- | --- | --- |
| `401` | `-32001` | Thiếu token hoặc token không hợp lệ. | Lấy token mới. |
| `403` | `-32003` | User không được ghi `edc.device`. | Nhờ admin cấp quyền. |
| `400` | `-32602` | `id` không phải số nguyên dương. | Sửa ID. |
| `400` | `-32000` | Không có thiết bị với ID này (`edc.device <id> not found`), hoặc không lưu được mã. | Kiểm tra ID, thử lại sau. |

### Cần biết

- Mã mới hủy mọi mã cũ chưa dùng của cùng thiết bị.
- Cấp mã không làm thay đổi trạng thái thiết bị.
