## Danh sách user

Liệt kê các user đăng nhập. Dùng để làm bảng quản lý user.

### Trước khi bắt đầu

- Bạn cần bearer token.
- User của bạn phải có quyền đọc user. Nếu không, bạn nhận `403`.

### Request

```http
GET /base/admin/users
Authorization: Bearer <token>
```

Query parameter trở thành tham số của call, ví dụ `limit` và `offset` (lấy bao nhiêu dòng, bỏ qua bao nhiêu dòng) và `order`. Giá trị query luôn đến dưới dạng văn bản. Vì vậy nếu cần bộ lọc hoặc danh sách field, hãy dùng dạng RPC:

```http
POST /rpc/base.users/search_read
Authorization: Bearer <token>
Content-Type: application/json
```

| Tên (trong `kwargs`) | Kiểu | Bắt buộc | Ý nghĩa |
| --- | --- | --- | --- |
| `domain` | array | Không | Bộ lọc, ví dụ `[["login","ilike","operator"]]` (login chứa "operator"). |
| `fields` | string[] | Không | Các cột cần trả về, ví dụ `["id","login","name","group_id"]`. |
| `limit` / `offset` | integer | Không | Phân trang, ví dụ `20` và `0`. |
| `order` | string | Không | Sắp xếp, ví dụ `login asc`. |

### Ví dụ

```bash
curl "$BASE_URL/base/admin/users?limit=20" \
  -H "Authorization: Bearer $TOKEN"
```

Có danh sách field (nên dùng):

```bash
curl -X POST "$BASE_URL/rpc/base.users/search_read" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"kwargs":{"fields":["id","login","name","group_id"],"limit":20}}'
```

### Response

HTTP `200`. `result` là danh sách các dòng user:

```json
{
  "id": "111020261535481234570001",
  "result": [
    { "id": 1, "login": "admin", "name": "Administrator", "group_id": 1 },
    { "id": 7, "login": "operator01", "name": "Operator One", "group_id": 3 }
  ]
}
```

| Field | Ý nghĩa |
| --- | --- |
| `id` | Id user (là số). |
| `login` | Tên đăng nhập. |
| `name` | Tên hiển thị. |
| `group_id` | Group chính của user. |

### Lỗi

Lỗi dùng định dạng chuẩn được mô tả trong API guide (xem "Response" → "Errors").

| HTTP | `error.code` | Ý nghĩa | Bạn nên làm gì |
| --- | --- | --- | --- |
| `400` | `-32602` hoặc `-32000` | Field không tồn tại hoặc bộ lọc sai. | Sửa `fields` hoặc `domain`. |
| `401` | `-32001` | Thiếu token hoặc token không hợp lệ. | Lấy token mới. |
| `403` | `-32003` | User của bạn không được đọc user. | Nhờ admin cấp quyền. |

### Cần biết

- Nếu không truyền `fields`, mọi cột đọc được đều được trả về, kể cả cột password hash đã lưu. Luôn truyền `fields` khi làm màn hình.
- Response không có `total`. Hãy phân trang bằng `limit` và `offset`.
- Không có danh sách `roles`. Quyền truy cập đến từ group của user (`group_id`).
