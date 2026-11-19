## Cập nhật user

Đổi các field của một user đăng nhập. Bạn cũng có thể tắt hoặc bật user. Dùng để sửa profile, đổi group và vô hiệu hóa tài khoản.

### Trước khi bắt đầu

- Bạn cần bearer token.
- User của bạn phải có quyền sửa user đó. Nếu không, bạn nhận `403`. User thường (không phải admin) chỉ đổi được vài field trong profile của chính mình: name, email, signature, ngôn ngữ, múi giờ, phone và company.

### Request

```http
PUT /base/admin/users/{id}
Authorization: Bearer <token>
Content-Type: application/json
```

| Path parameter | Kiểu | Bắt buộc | Ý nghĩa |
| --- | --- | --- | --- |
| `id` | integer | Có | Id của user, ví dụ `8`. Là số, không phải UUID. |

| Field trong body | Kiểu | Bắt buộc | Ý nghĩa |
| --- | --- | --- | --- |
| `values` | object | Có | Các field cần đổi, ví dụ `{"name":"New Name","group_id":3,"active":false}`. `group_id` bằng 0 hoặc thiếu sẽ bị bỏ qua. Nó không bao giờ xóa group. |

### Ví dụ

```bash
curl -X PUT "$BASE_URL/base/admin/users/8" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"values":{"name":"Operator HCM 01","group_id":3}}'
```

### Response

HTTP `200`. Response không chứa user. Nếu cần, đọc lại bằng `GET /base/admin/users/{id}`.

```json
{ "id": "111020261535481234570001", "result": true }
```

### Lỗi

Lỗi dùng định dạng chuẩn được mô tả trong API guide (xem "Response" → "Errors").

| HTTP | `error.code` | Ý nghĩa | Bạn nên làm gì |
| --- | --- | --- | --- |
| `400` | `-32602` hoặc `-32000` | Thiếu `values`, id không hợp lệ, hoặc validate lỗi. | Đọc `error.message` và sửa request. |
| `401` | `-32001` | Thiếu token hoặc token không hợp lệ. | Lấy token mới. |
| `403` | `-32003` | User của bạn không được sửa user này. | Nhờ admin cấp quyền. |

### Cần biết

- Tắt user bằng `{"values":{"active":false}}`. Bật lại bằng `true`.
- Không gửi `password` ở đây. Dùng `POST /base/admin/users/{id}/password`.
