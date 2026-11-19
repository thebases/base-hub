## Xóa nhóm (Group)

Xóa vĩnh viễn một nhóm thiết bị. Các thiết bị trong nhóm không bị xóa; `groupId` của chúng được xóa trắng.

### Trước khi bắt đầu

Bạn cần bearer token của user có quyền xóa nhóm (kiểm tra quyền, ACL). Không có quyền sẽ nhận `403`.

### Request

```http
DELETE /base/groups/{id}
Authorization: Bearer <token>
```

| Tham số path | Kiểu | Bắt buộc | Ý nghĩa |
| --- | --- | --- | --- |
| `id` | int | Có | Id nhóm, ví dụ `3`. |

### Ví dụ

```bash
curl -X DELETE "$BASE_URL/base/groups/3" \
  -H "Authorization: Bearer $TOKEN"
```

### Response

```json
{
  "id": "111020261535481234570001",
  "result": { "deleted": true, "id": 3 }
}
```

| Trường | Ý nghĩa |
| --- | --- |
| `result.deleted` | `true` khi nhóm đã bị xóa. |
| `result.id` | Id của nhóm đã xóa. |

### Lỗi

Lỗi dùng định dạng chuẩn được mô tả trong hướng dẫn API (xem "Response" → "Errors").

| HTTP | `error.code` | Ý nghĩa | Bạn nên làm gì |
| --- | --- | --- | --- |
| `400` | `-32602` | `id` không phải số nguyên. | Gửi một số. |
| `401` | `-32001` | Thiếu hoặc sai token. | Lấy token mới. |
| `403` | `-32003` | User không có quyền xóa nhóm. | Xin admin cấp quyền. |
| `404` | `-32004` | Không có nhóm nào với id này. Cũng xảy ra khi xóa cùng một nhóm hai lần. | Kiểm tra id. |
| `500` | `-32603` | Lỗi server bất ngờ. | Thử lại sau; báo cáo kèm `id` của response. |

### Cần biết

- Không thể hoàn tác. Hãy yêu cầu người dùng xác nhận trước khi gọi.
- Thiết bị không còn nhóm sẽ dùng tên nhóm `default` trong topic lệnh MQTT. Nếu bạn xóa nhóm `default` có sẵn, thiết bị mới sẽ được tạo mà không có nhóm.
