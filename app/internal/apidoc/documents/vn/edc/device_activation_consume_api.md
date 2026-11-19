## Consume Device Activation Code

Kiểm tra serial và mã kích hoạt với bản ghi activation đã lưu, rồi đánh dấu mã là đã dùng. Dùng trong công cụ hoặc automation chỉ cần "xác minh và đốt mã này".

> **Warning:** Đây không phải lệnh onboarding của máy. Nó không tạo device secret và không thay đổi thiết bị. Máy dùng `POST /device/provision` (xem **Create Device** để biết toàn bộ quy trình). Backend không tự gọi `consume` cho bạn.

### Trước khi bắt đầu

Cần bearer token của user có quyền ghi `edc.device.activation`; nếu không sẽ nhận `403`.

### Request

```http
POST /base/edc/device/activation/consume
Authorization: Bearer <token>
Content-Type: application/json
```

Body bọc tham số trong `kwargs`.

| Tên | Kiểu | Bắt buộc | Ý nghĩa |
| --- | --- | --- | --- |
| `kwargs.serial` | string | Có | Serial của máy, ví dụ `SN0001` (`serial_number` cũng được). Được cắt khoảng trắng và viết hoa. |
| `kwargs.code` | string | Có | Mã kích hoạt cần kiểm tra, ví dụ `48213907`. Được cắt khoảng trắng. |

### Ví dụ

```bash
curl -X POST "$BASE_URL/base/edc/device/activation/consume" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"kwargs":{"serial":"SN0001","code":"48213907"}}'
```

### Response

`200`:

```json
{ "id": "111020261535481234570001", "result": { "id": 7, "serial": "SN0001" } }
```

| Trường | Ý nghĩa |
| --- | --- |
| `result.id` | ID activation đã được consume. |
| `result.serial` | Serial mà nó thuộc về. |

### Lỗi

Lỗi dùng định dạng chuẩn được mô tả trong hướng dẫn API (xem "Response" → "Errors"). Lý do nằm trong `error.message`.

| HTTP | Message chứa | Ý nghĩa | Bạn nên làm gì |
| --- | --- | --- | --- |
| `400` | `serial is required` / `code is required` | Một trường bị trống. | Gửi cả hai trường. |
| `400` | `no activation for serial SN0001` | Serial này chưa từng được đặt mã. | Tạo bằng `POST /base/device/activation`. |
| `400` | `code already used` | Mã đã được consume. | Đặt mã mới. |
| `400` | `code expired` | Mã đã quá `expiresAt`. | Đặt mã mới. |
| `400` | `invalid code` | Sai mã. | Kiểm tra lại mã. |
| `400` | `... encryption key not configured (app.encryptKey)` | Server không có provisioning key. | Liên hệ đội backend. |
| `401` / `403` | | Thiếu/sai token, hoặc không có quyền ghi. | Lấy token / nhờ admin. |

### Cần biết

- Khi thành công `used_at` được đặt. Gọi lần hai với cùng mã sẽ lỗi `code already used`. Muốn dùng lại, đặt mã mới bằng `POST /base/device/activation` (lệnh này xóa `used_at`).
- Mã chỉ được lưu dưới dạng hash có khóa và so sánh an toàn. Không bao giờ trả lại.
- Chỉ có route chuẩn `POST /rpc/edc.device.activation/consume` là tương đương; không có route kiểu resource `/base/...`.
