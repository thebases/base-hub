## Tạo nhóm (Group)

Tạo một nhóm thiết bị (tập hợp terminal có tên, ví dụ theo thành phố hoặc theo khách hàng). Sau đó thêm thiết bị vào bằng `POST /base/groups/{id}/devices`. Một thiết bị chỉ thuộc tối đa một nhóm.

### Trước khi bắt đầu

Bạn cần bearer token (xem hướng dẫn API) của user có quyền tạo nhóm (kiểm tra quyền, gọi là ACL). Không có quyền sẽ nhận `403`.

### Request

```http
POST /base/groups
Authorization: Bearer <token>
Content-Type: application/json
```

| Trường body | Kiểu | Bắt buộc | Ý nghĩa |
| --- | --- | --- | --- |
| `name` | string | Có | Tên nhóm, ví dụ `Hanoi Stores`. Tên không cần duy nhất. Server không cắt khoảng trắng và không từ chối chuỗi rỗng, nhưng bỏ trống trường này sẽ lỗi. |
| `customerId` | int | Không | Id khách hàng sở hữu nhóm, ví dụ `12`. |

Các key khác bị bỏ qua.

### Ví dụ

```bash
curl -X POST "$BASE_URL/base/groups" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"name":"Hanoi Stores","customerId":12}'
```

### Response

HTTP `201`:

```json
{
  "id": "111020261535481234570001",
  "result": { "id": 3, "name": "Hanoi Stores", "customerId": 12 }
}
```

| Trường | Ý nghĩa |
| --- | --- |
| `id` | Id của request (do bạn gửi, hoặc server tạo). |
| `result.id` | Id của nhóm mới. Dùng trong các lệnh nhóm khác. |
| `result.name` | Tên nhóm. |
| `result.customerId` | Khách hàng sở hữu. Bị bỏ khi không có. |

### Lỗi

Lỗi dùng định dạng chuẩn được mô tả trong hướng dẫn API (xem "Response" → "Errors").

| HTTP | `error.code` | Ý nghĩa | Bạn nên làm gì |
| --- | --- | --- | --- |
| `400` | `-32700` | Body không phải JSON hợp lệ. | Sửa JSON. |
| `400` | `-32000` (`data.reason` = `create_failed`) | Thiếu `name`, hoặc không lưu được nhóm. | Gửi `name`. |
| `401` | `-32001` | Thiếu hoặc sai token. | Lấy token mới. |
| `403` | `-32003` | User không có quyền tạo nhóm. | Xin admin cấp quyền. |

### Cần biết

- Tên nhóm trở thành một phần của topic lệnh thiết bị: `base/<group name>/<serial>/cmd`. Hãy chọn tên an toàn khi dùng trong topic MQTT.
