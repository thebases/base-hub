## Đặt lại password của user

Đặt password mới cho một user. Không cần password cũ. Dùng khi user quên password.

### Trước khi bắt đầu

- Bạn cần bearer token.
- User của bạn phải có quyền sửa user đó. Nếu không, bạn nhận `403`.

### Request

```http
POST /base/admin/users/{id}/password
Authorization: Bearer <token>
Content-Type: application/json
```

| Path parameter | Kiểu | Bắt buộc | Ý nghĩa |
| --- | --- | --- | --- |
| `id` | integer | Có | Id của user, ví dụ `8`. Là số, không phải UUID. Luôn lấy từ path. `id` trong body bị bỏ qua. |

| Field trong body | Kiểu | Bắt buộc | Ý nghĩa |
| --- | --- | --- | --- |
| `password` | string | Có | Password mới, ví dụ `N3w-pass-2026`. Khoảng trắng hai đầu bị cắt. Không được rỗng. Không có yêu cầu độ dài tối thiểu. Server chỉ lưu bản băm (hash). |

### Ví dụ

```bash
curl -X POST "$BASE_URL/base/admin/users/8/password" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"password":"N3w-pass-2026"}'
```

### Response

HTTP `200`:

```json
{ "id": "111020261535481234570001", "result": { "status": "OK" } }
```

`result.status` là `OK` khi đã đổi password. Dòng user không được trả về.

### Lỗi

Lỗi dùng định dạng chuẩn được mô tả trong API guide (xem "Response" → "Errors").

| HTTP | `error.code` | Ý nghĩa | Bạn nên làm gì |
| --- | --- | --- | --- |
| `400` | `-32602` hoặc `-32000` | Id rỗng, body không phải JSON hợp lệ, `password` rỗng, hoặc user không tồn tại. | Đọc `error.message` và sửa request. |
| `401` | `-32001` | Thiếu token hoặc token không hợp lệ. | Lấy token mới. |
| `403` | `-32003` | User của bạn không được sửa user này. | Nhờ admin cấp quyền. |

### Cần biết

- Các token user đang có **không** bị thu hồi. Chúng vẫn hợp lệ cho đến khi hết hạn.
- Cũng gọi được bằng `POST /rpc/base.users/action_set_password/{id}` với body `{"kwargs":{"new_password":"..."}}`.
- Nếu cần API key (thay cho password), dùng method `base.users.action_generate_api_key`.
