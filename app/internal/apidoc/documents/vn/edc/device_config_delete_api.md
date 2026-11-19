## Delete Device Config

Xóa vĩnh viễn một version của config template (bộ cài đặt có tên cho máy POS, xem *Create Device Config*). Chỉ xóa được khi không có thiết bị nào được gán version đó.

### Trước khi bắt đầu

Bạn cần bearer token (token đăng nhập, gửi trong header `Authorization`) của user có quyền đọc và xóa config template. Không có quyền sẽ nhận `403`.

### Request

```http
DELETE /base/device/config/{id}
Authorization: Bearer <token>
```

| Tên | Kiểu | Bắt buộc | Ý nghĩa |
| --- | --- | --- | --- |
| `id` | int (path) | Có | ID của config version cần xóa, ví dụ `12`. |

### Ví dụ

```bash
curl -X DELETE "$BASE_URL/base/device/config/12" \
  -H "Authorization: Bearer $TOKEN"
```

### Response

HTTP `200`.

```json
{
  "id": "111020261535481234570001",
  "result": { "deleted": true, "id": 12 }
}
```

| Trường | Ý nghĩa |
| --- | --- |
| `result.deleted` | `true` khi version đã được xóa. |
| `result.id` | ID của version đã xóa. |

### Lỗi

Lỗi dùng định dạng chuẩn được mô tả trong API guide (xem "Response" → "Errors").

| HTTP | `error.code` | Ý nghĩa | Bạn nên làm gì |
| --- | --- | --- | --- |
| `400` | `-32602` | `id` không phải số nguyên. | Gửi số trên path. |
| `401` | `-32001` | Thiếu token hoặc token không hợp lệ. | Đăng nhập lại rồi gọi lại. |
| `403` | `-32003` | User không có quyền đọc hoặc xóa config template. | Nhờ admin cấp quyền. |
| `404` | `-32004` | Không có config nào mang ID này. | Kiểm tra lại ID. |
| `409` | `-32009` | Còn ít nhất một thiết bị được gán version này (`data.reason` là `config_in_use`). | Chuyển các thiết bị đó sang config khác trước (*Assign Device Config*), rồi xóa lại. |
| `500` | `-32603` | Lỗi server hoặc database không mong đợi. | Thử lại sau; báo kèm `id` của response. |

### Cần biết

- Chỉ version bạn chỉ định bị xóa. Các version khác cùng tên vẫn còn.
- Các liên kết bank app (cài đặt TID/MID) của version đó bị xóa theo.
- Nếu xóa version đang active, tên template đó không còn version active. Hãy kích hoạt version khác nếu muốn thiết bị tiếp tục theo template.
- *Get Device Config* trả `deviceStatuses`, danh sách các thiết bị đang chặn việc xóa.
