## Delete Device Activation

Xóa vĩnh viễn một bản ghi activation. Dùng để hủy một mã không bao giờ được dùng.

### Trước khi bắt đầu

Cần bearer token của user có quyền xóa bản ghi `edc.device.activation`; nếu không sẽ nhận `403`.

### Request

```http
DELETE /base/device/activation/{id}
Authorization: Bearer <token>
```

| Tên | Kiểu | Bắt buộc | Ý nghĩa |
| --- | --- | --- | --- |
| `id` | int | Có | ID activation, ví dụ `7`. |

### Ví dụ

```bash
curl -X DELETE "$BASE_URL/base/device/activation/7" \
  -H "Authorization: Bearer $TOKEN"
```

### Response

`200`:

```json
{
  "id": "111020261535481234570001",
  "result": { "deleted": true, "id": 7 }
}
```

| Trường | Ý nghĩa |
| --- | --- |
| `result.deleted` | `true` khi bản ghi đã bị xóa. |
| `result.id` | ID đã bị xóa. |

### Lỗi

Lỗi dùng định dạng chuẩn được mô tả trong hướng dẫn API (xem "Response" → "Errors").

| HTTP | `error.code` | Ý nghĩa | Bạn nên làm gì |
| --- | --- | --- | --- |
| `400` | `-32602` | `id` không phải số. | Gửi ID dạng số. |
| `401` | `-32001` | Thiếu token hoặc token không hợp lệ. | Lấy token mới. |
| `403` | `-32003` | User không được xóa model này. | Nhờ admin cấp quyền. |
| `404` | `-32004` | Không có activation với ID này (có thể đã bị xóa). | Kiểm tra ID. |

### Cần biết

- Không thể hoàn tác. Bản ghi thiết bị và máy đã provision không bị ảnh hưởng.
- Không có activation thì máy không thể provision (nhận `activation_invalid`). Tạo cái mới bằng `POST /base/device/activation` khi cần.
