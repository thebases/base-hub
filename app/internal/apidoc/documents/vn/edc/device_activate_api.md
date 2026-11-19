## Activate Device

Kiểm tra mã kích hoạt dùng một lần của thiết bị, tạo device secret mới, đặt thiết bị thành `active` và trả secret về một lần. Đây là lối tắt cho admin, làm việc theo ID số của thiết bị.

> **Warning:** Máy **không** dùng route này. Máy onboarding bằng `POST /device/provision` (xem **Create Device** để biết quy trình tạo → mã kích hoạt → provision). Route này chỉ nhận mã tạo bởi **Issue Device Activation Code**, không nhận mã đặt bằng `POST /base/device/activation`.

### Trước khi bắt đầu

- Cần bearer token của user có quyền ghi `edc.device`; nếu không sẽ nhận `403`.
- Lấy mã trước bằng `POST /base/edc/devices/{id}/issue-activation-code`.

### Request

```http
POST /base/edc/devices/{id}/activate
Authorization: Bearer <token>
Content-Type: application/json
```

Body bọc tham số trong `kwargs`.

| Tên | Vị trí | Kiểu | Bắt buộc | Ý nghĩa |
| --- | --- | --- | --- | --- |
| `id` | path | integer | Có | ID số của thiết bị, ví dụ `42` (không phải serial). |
| `kwargs.code` | body | string | Có | Mã từ **Issue Device Activation Code**, ví dụ `48213907`. Khoảng trắng bị bỏ; để trống sẽ bị từ chối. |

### Ví dụ

```bash
curl -X POST "$BASE_URL/base/edc/devices/42/activate" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"args": [], "kwargs": {"code": "48213907"}}'
```

### Response

`200`:

```json
{
  "id": "111020261535481234570001",
  "result": {
    "device_id": 42,
    "secret": "9f8e7d6c5b4a39281706f5e4d3c2b1a0987654321fedcba0123456789abcdef0"
  }
}
```

| Trường | Ý nghĩa |
| --- | --- |
| `result.device_id` | ID số của thiết bị. |
| `result.secret` | Device secret mới (64 ký tự hex). **Chỉ hiện một lần.** Hãy lưu vào máy. |

Sau đó máy gọi các route thiết bị với `Authorization: Bearer <SERIAL>:<secretVersion>:<secret>`.

### Lỗi

Lỗi dùng định dạng chuẩn được mô tả trong hướng dẫn API (xem "Response" → "Errors").

| HTTP | `error.code` | Ý nghĩa | Bạn nên làm gì |
| --- | --- | --- | --- |
| `401` | `-32001` | Thiếu token hoặc token không hợp lệ. | Lấy token mới. |
| `403` | `-32003` | User không được ghi `edc.device`. | Nhờ admin cấp quyền. |
| `400` | `-32602` | `id` không phải số nguyên dương, hoặc `code` thiếu/để trống. | Sửa request. |
| `400` | `-32000` | Không có thiết bị với ID này (message `edc.device <id> not found`). | Kiểm tra ID. |
| `400` | `-32000` | `activation code invalid or expired`. Một message cho mọi trường hợp: không có mã còn hiệu lực, sai mã, mã đã dùng, mã hết hạn. | Cấp mã mới rồi thử lại. |
| `400` | `-32000` | Không lưu được secret mới. | Thử lại sau. |

> **Check:** "Không tìm thấy thiết bị" và "sai mã" có thể được báo là `400` với `-32000`, không phải `404` / `-32004`. Hãy xác nhận bằng một lệnh gọi thật.

### Cần biết

- Mã bị dùng hết khi thành công. Gọi lần hai với cùng mã sẽ lỗi.
- Khi thành công, thiết bị có `status = active`, `secret_version` + 1 và `provisioning_status = active`; `revoked_at` bị xóa. MQTT client ID được đặt bằng serial viết hoa nếu trước đó trống.
- Secret cũ ngừng hoạt động ngay khi version đổi.
