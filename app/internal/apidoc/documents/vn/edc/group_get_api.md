## Lấy thông tin nhóm (Group)

Trả về một nhóm thiết bị theo id.

### Trước khi bắt đầu

Bạn cần bearer token của user có quyền đọc nhóm (kiểm tra quyền, ACL). Không có quyền sẽ nhận `403`.

### Request

```http
GET /base/groups/{id}
Authorization: Bearer <token>
```

| Tham số path | Kiểu | Bắt buộc | Ý nghĩa |
| --- | --- | --- | --- |
| `id` | int | Có | Id nhóm, ví dụ `3`. |

### Ví dụ

```bash
curl "$BASE_URL/base/groups/3" \
  -H "Authorization: Bearer $TOKEN"
```

### Response

```json
{
  "id": "111020261535481234570001",
  "result": { "id": 3, "name": "Hanoi Stores", "customerId": 12 }
}
```

| Trường | Ý nghĩa |
| --- | --- |
| `result.id` | Id nhóm. |
| `result.name` | Tên nhóm. |
| `result.customerId` | Id khách hàng sở hữu. Bị bỏ khi nhóm không có khách hàng. |

### Lỗi

Lỗi dùng định dạng chuẩn được mô tả trong hướng dẫn API (xem "Response" → "Errors").

| HTTP | `error.code` | Ý nghĩa | Bạn nên làm gì |
| --- | --- | --- | --- |
| `400` | `-32602` | `id` không phải số nguyên. | Gửi một số. |
| `401` | `-32001` | Thiếu hoặc sai token. | Lấy token mới. |
| `403` | `-32003` | User không có quyền với nhóm. | Xin admin cấp quyền. |
| `404` | `-32004` | Không có nhóm nào với id này. | Kiểm tra id. |
| `500` | `-32603` | Lỗi server bất ngờ. | Thử lại sau; báo cáo kèm `id` của response. |
