## Rotate Device Secret

Thay secret của thiết bị đang active mà không cần mã kích hoạt mới. Dùng khi secret bị mất hoặc lộ. Secret mới được trả về một lần.

> **Warning:** Secret cũ ngừng hoạt động ngay lập tức. Hãy đưa secret mới cho máy ngay, nếu không máy sẽ bị khóa.

### Trước khi bắt đầu

Cần bearer token của user có quyền ghi `edc.device`; nếu không sẽ nhận `403`. Thiết bị phải đang `active` (xem **Create Device** để biết thiết bị trở thành active như thế nào).

### Request

```http
POST /base/edc/devices/{id}/rotate-secret
Authorization: Bearer <token>
Content-Type: application/json
```

Body không có tham số. Gửi `{}`.

| Tên | Vị trí | Kiểu | Bắt buộc | Ý nghĩa |
| --- | --- | --- | --- | --- |
| `id` | path | integer | Có | ID số của thiết bị, ví dụ `42` (không phải serial). |

### Ví dụ

```bash
curl -X POST "$BASE_URL/base/edc/devices/42/rotate-secret" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{}'
```

### Response

`200`:

```json
{
  "id": "111020261535481234570001",
  "result": {
    "device_id": 42,
    "secret": "0b1c2d3e4f5a69788796a5b4c3d2e1f00112233445566778899aabbccddeeff0"
  }
}
```

| Trường | Ý nghĩa |
| --- | --- |
| `result.device_id` | ID số của thiết bị. |
| `result.secret` | Secret mới (64 ký tự hex). **Chỉ hiện một lần.** |

### Lỗi

Lỗi dùng định dạng chuẩn được mô tả trong hướng dẫn API (xem "Response" → "Errors").

| HTTP | `error.code` | Ý nghĩa | Bạn nên làm gì |
| --- | --- | --- | --- |
| `401` | `-32001` | Thiếu token hoặc token không hợp lệ. | Lấy token mới. |
| `403` | `-32003` | User không được ghi `edc.device`. | Nhờ admin cấp quyền. |
| `400` | `-32602` | `id` không phải số nguyên dương. | Sửa ID. |
| `400` | `-32000` | Không có thiết bị với ID này, `status` của thiết bị không phải `active`, hoặc không lưu được secret. | Kiểm tra ID; activate thiết bị trước. |

### Cần biết

- `secret_version` tăng thêm 1. Bearer token của máy phải dùng version mới: `<SERIAL>:<version mới>:<secret>`.
- `status` và `activated_at` không đổi. `provisioning_status` thành `active` và `revoked_at` bị xóa, nên rotate cũng gỡ trạng thái revoke của thiết bị.
- Bản sao secret dùng để ký lệnh MQTT cũng được làm mới. Nếu bước này lỗi thì chỉ ghi log, lệnh gọi vẫn thành công.
