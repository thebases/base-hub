## Gỡ thiết bị khỏi nhóm

Đưa một thiết bị ra khỏi nhóm bằng cách xóa trắng `groupId` của nó. Bản thân thiết bị không bị xóa.

### Trước khi bắt đầu

Bạn cần bearer token của user có quyền ghi thiết bị (kiểm tra quyền, ACL). Không có quyền sẽ nhận `403`. Lấy id thiết bị từ *List Devices in Group*.

### Request

```http
DELETE /base/groups/{id}/devices/{memberID}
Authorization: Bearer <token>
```

| Tham số path | Kiểu | Bắt buộc | Ý nghĩa |
| --- | --- | --- | --- |
| `id` | int | Có | Id nhóm, ví dụ `3`. Thiết bị phải đang ở trong nhóm này. |
| `memberID` | int | Có | Id số của thiết bị, ví dụ `101`. Đây không phải serial. |

### Ví dụ

```bash
curl -X DELETE "$BASE_URL/base/groups/3/devices/101" \
  -H "Authorization: Bearer $TOKEN"
```

### Response

```json
{
  "id": "111020261535481234570001",
  "result": { "deleted": true, "id": 101 }
}
```

| Trường | Ý nghĩa |
| --- | --- |
| `result.deleted` | `true` khi thiết bị đã được gỡ khỏi nhóm. |
| `result.id` | Id thiết bị. |

### Lỗi

Lỗi dùng định dạng chuẩn được mô tả trong hướng dẫn API (xem "Response" → "Errors").

| HTTP | `error.code` | Ý nghĩa | Bạn nên làm gì |
| --- | --- | --- | --- |
| `400` | `-32602` | `id` hoặc `memberID` không phải số nguyên. | Gửi số. |
| `401` | `-32001` | Thiếu hoặc sai token. | Lấy token mới. |
| `403` | `-32003` | User không có quyền với thiết bị. | Xin admin cấp quyền. |
| `404` | `-32004` | Không có thiết bị với `memberID` này, hoặc nó không thuộc nhóm `id`. Xóa lặp lại cũng cho lỗi này. | Kiểm tra các id. |
| `500` | `-32603` | Lỗi server bất ngờ. | Thử lại sau; báo cáo kèm `id` của response. |

### Cần biết

- Nhóm trong path được kiểm tra. Thiết bị ở nhóm khác (hoặc không có nhóm) cho `404` và không có gì thay đổi.
- Sau khi gỡ, thiết bị không còn nhóm. Trong topic lệnh MQTT (`base/<group name>/<serial>/cmd`) nó dùng tên nhóm `default`.
