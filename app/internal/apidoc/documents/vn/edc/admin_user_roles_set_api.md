## Đặt role cho user

> **Không khả dụng.** `POST /base/admin/users/{id}/roles` không tồn tại. Request trả về `404`. Không có role như `ADMIN` hay `OPERATOR`. Việc user được làm gì phụ thuộc vào **group** của user (`group_id`) và các quy tắc truy cập gắn với group đó.

Hãy dùng call cập nhật bên dưới để chuyển user sang group khác.

### Trước khi bắt đầu

- Bạn cần bearer token.
- User của bạn phải có quyền sửa user đó. Nếu không, bạn nhận `403`.

### Request

Đây là cùng một call với "Cập nhật user".

```http
PUT /base/admin/users/{id}
Authorization: Bearer <token>
Content-Type: application/json
```

| Field trong body | Kiểu | Bắt buộc | Ý nghĩa |
| --- | --- | --- | --- |
| `values.group_id` | integer | Có | Id của group cần gán, ví dụ `3`. Giá trị bằng 0, rỗng hoặc không phải số sẽ bị bỏ qua (group không đổi). |

### Ví dụ

```bash
curl -X PUT "$BASE_URL/base/admin/users/8" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"values":{"group_id":3}}'
```

### Response

HTTP `200`:

```json
{ "id": "111020261535481234570001", "result": true }
```

### Lỗi

Lỗi dùng định dạng chuẩn được mô tả trong API guide (xem "Response" → "Errors").

| HTTP | `error.code` | Ý nghĩa | Bạn nên làm gì |
| --- | --- | --- | --- |
| `401` | `-32001` | Thiếu token hoặc token không hợp lệ. | Lấy token mới. |
| `403` | `-32003` | User của bạn không được sửa user này. | Nhờ admin cấp quyền. |
| `404` | `-32601` | Bạn gọi `POST .../roles`. Route này không tồn tại. | Dùng `PUT /base/admin/users/{id}` thay thế. |

### Cần biết

- Mỗi user có một group chính (`group_id`). Các liên kết group khác được quản lý qua model `tbl.groups`, không qua đường dẫn này.
- Các token đã có vẫn giữ quyền cũ cho đến khi hết hạn hoặc được refresh.
