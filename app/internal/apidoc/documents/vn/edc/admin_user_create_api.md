## Tạo user

Tạo một user đăng nhập mới (người có thể đăng nhập vào Base HUB). Dùng khi bạn thêm một operator hoặc admin mới.

### Trước khi bắt đầu

- Bạn cần bearer token (access token lấy từ `POST /oauth/token`, xem "Lấy access token").
- User của bạn phải có quyền tạo bản ghi `base.users`. Nếu không, bạn nhận `403`.

### Request

```http
POST /base/admin/users
Authorization: Bearer <token>
Content-Type: application/json
```

Đặt các field của user trong object `values`. `values` cũng có thể là một danh sách object. Khi đó server tạo nhiều user và `result` là một danh sách.

| Field trong body | Kiểu | Bắt buộc | Ý nghĩa |
| --- | --- | --- | --- |
| `values.login` | string | Có | Tên user gõ khi đăng nhập, ví dụ `operator.hcm01`. |
| `values.password` | string | Có, với user active | Văn bản thường, ví dụ `S3cret-pass`. Server chỉ lưu bản băm (hash). User active mà không có password sẽ bị từ chối. |
| `values.name` | string | Không | Tên hiển thị, ví dụ `Operator HCM 01`. |
| `values.email` | string | Không | Địa chỉ email, ví dụ `op01@example.com`. |
| `values.group_id` | integer | Không | Group chính của user, ví dụ `3`. Group quyết định user được làm gì. |
| `values.company_id` / `values.company_ids` | integer / integer[] | Không | Công ty của user, ví dụ `1` hoặc `[1,2]`. |
| `values.lang_id`, `values.tz` | string | Không | Ngôn ngữ và múi giờ, ví dụ `en_US`, `Asia/Ho_Chi_Minh`. |

Để xem đầy đủ các field, gọi `GET /doc/base.users.json`.

### Ví dụ

```bash
curl -X POST "$BASE_URL/base/admin/users" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"values":{"login":"operator.hcm01","password":"S3cret-pass","name":"Operator HCM 01","group_id":3}}'
```

### Response

HTTP `200`:

```json
{
  "id": "111020261535481234570001",
  "result": { "id": 8, "login": "operator.hcm01", "name": "Operator HCM 01", "group_id": 3 }
}
```

| Field | Ý nghĩa |
| --- | --- |
| `id` | Mã request. Hãy ghi lại khi báo lỗi. |
| `result` | Dòng user mới tạo. `result.id` là id số dùng cho các call `/base/admin/users/{id}` khác. |

> **Warning:** Coi `result` là dữ liệu nhạy cảm. Không ghi vào log. Các cột đã lưu như password hash có thể nằm trong đó.

### Lỗi

Lỗi dùng định dạng chuẩn được mô tả trong API guide (xem "Response" → "Errors").

| HTTP | `error.code` | Ý nghĩa | Bạn nên làm gì |
| --- | --- | --- | --- |
| `400` | `-32602` hoặc `-32000` | Thiếu `values`, user active không có password, login đã tồn tại, hoặc validate lỗi khác. | Đọc `error.message` và sửa body. |
| `401` | `-32001` | Thiếu token hoặc token không hợp lệ. | Lấy token mới. |
| `403` | `-32003` | User của bạn không được tạo user. | Nhờ admin cấp quyền. |

### Cần biết

- Để đổi password sau này, dùng `POST /base/admin/users/{id}/password`.
- Cũng gọi được bằng `POST /rpc/base.users/create` với body `{"kwargs":{"values":{...}}}`.
