## Get Device Activation

Trả về một bản ghi activation theo ID số. Dùng để kiểm tra mã của thiết bị còn hiệu lực hay đã được dùng.

### Trước khi bắt đầu

Cần bearer token của user có quyền đọc `edc.device.activation`; nếu không sẽ nhận `403`.

### Request

```http
GET /base/device/activation/{id}
Authorization: Bearer <token>
```

| Tên | Kiểu | Bắt buộc | Ý nghĩa |
| --- | --- | --- | --- |
| `id` | int | Có | ID activation, ví dụ `7`. Lấy từ phản hồi create hoặc list. **Không** phải serial. |

### Ví dụ

```bash
curl "$BASE_URL/base/device/activation/7" \
  -H "Authorization: Bearer $TOKEN"
```

### Response

`200`:

```json
{
  "id": "111020261535481234570001",
  "result": {
    "id": 7,
    "serial": "SN0001",
    "expiresAt": "2026-09-27T08:00:00Z",
    "createdAt": "2026-09-26T08:00:00Z",
    "updatedAt": "2026-09-26T08:00:00Z"
  }
}
```

| Trường | Ý nghĩa |
| --- | --- |
| `result.id` | ID activation. |
| `result.serial` | Serial của máy. |
| `result.expiresAt` | Thời điểm mã hết hạn. |
| `result.usedAt` | Chỉ có khi máy đã dùng mã. |

Mã kích hoạt không bao giờ được trả lại.

### Lỗi

Lỗi dùng định dạng chuẩn được mô tả trong hướng dẫn API (xem "Response" → "Errors").

| HTTP | `error.code` | Ý nghĩa | Bạn nên làm gì |
| --- | --- | --- | --- |
| `400` | `-32602` | `id` không phải số. | Gửi ID dạng số. |
| `401` | `-32001` | Thiếu token hoặc token không hợp lệ. | Lấy token mới. |
| `403` | `-32003` | User không được đọc model này. | Nhờ admin cấp quyền. |
| `404` | `-32004` | Không có activation với ID này. | Kiểm tra ID bằng lệnh list. |

### Cần biết

- Mã chỉ dùng được khi không có `usedAt` và `expiresAt` còn ở tương lai.
- Muốn tra theo serial, dùng `GET /base/device/activation?serial=SN0001` (xem **List Device Activations**).
