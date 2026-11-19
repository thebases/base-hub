## Xóa bank app info

Xóa vĩnh viễn một cấu hình bank app (TID/MID) theo id.

### Trước khi bắt đầu

- Bạn cần bearer token.
- User của bạn phải có quyền xóa bản ghi `edc.tidmid.config`. Nếu không, bạn nhận `403`.
- Hãy kiểm tra trước device config nào đang dùng bản ghi này. Xem "Cần biết".

### Request

```http
DELETE /base/bank-app-info/{id}
Authorization: Bearer <token>
```

| Tên | Kiểu | Bắt buộc | Ý nghĩa |
| --- | --- | --- | --- |
| `id` | integer | Có | Id của bank app info (trong path), ví dụ `7`. |

### Ví dụ

```bash
curl -X DELETE "$BASE_URL/base/bank-app-info/7" \
  -H "Authorization: Bearer $TOKEN"
```

### Response

HTTP `200`:

```json
{
  "id": "111020261535481234570001",
  "result": { "deleted": true, "id": 7 }
}
```

| Field | Ý nghĩa |
| --- | --- |
| `result.deleted` | `true` khi bản ghi đã được xóa. |
| `result.id` | Id của bản ghi đã xóa. |

### Lỗi

Lỗi dùng định dạng chuẩn được mô tả trong API guide (xem "Response" → "Errors").

| HTTP | `error.code` | Ý nghĩa | Bạn nên làm gì |
| --- | --- | --- | --- |
| `400` | `-32602` | `id` không phải số. | Gửi id là số nguyên. |
| `401` | `-32001` | Thiếu token hoặc token không hợp lệ. | Lấy token mới. |
| `403` | `-32003` | User của bạn không được xóa bank app info. | Nhờ admin cấp quyền. |
| `404` | `-32004` | Không có bản ghi nào với id này. Có thể nó đã bị xóa. | Coi như "đã xóa rồi". |
| `500` | `-32603` | Không xóa được bản ghi. | Thử lại sau. Báo kèm `id` của response. |

### Cần biết

> **Warning:** Không thể hoàn tác. Hãy xác nhận với người dùng trước khi gọi.

- Bản ghi cũng bị **âm thầm gỡ khỏi mọi device config** đang dùng nó. Các device đó mất bank app này ở lần đồng bộ config tiếp theo.
- Thử lại sau khi xóa thành công sẽ trả `404`. Đây là bình thường.
