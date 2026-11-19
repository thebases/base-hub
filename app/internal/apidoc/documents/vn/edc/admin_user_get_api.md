## Lấy thông tin user

Đọc một user đăng nhập theo id số. Dùng để điền màn hình chi tiết hoặc sửa user.

### Trước khi bắt đầu

- Bạn cần bearer token.
- User của bạn phải có quyền đọc user đó. Nếu không, bạn nhận `403`.
- Lấy id số từ `GET /base/admin/users` (danh sách user) hoặc từ response khi tạo.

### Request

```http
GET /base/admin/users/{id}
Authorization: Bearer <token>
```

| Path parameter | Kiểu | Bắt buộc | Ý nghĩa |
| --- | --- | --- | --- |
| `id` | integer | Có | Id của user, ví dụ `8`. Là số, không phải UUID. |

Route này không chọn được cột. Nếu chỉ muốn một số field, dùng dạng RPC:

```http
POST /rpc/base.users/read
Authorization: Bearer <token>
Content-Type: application/json
```

| Tên (trong `kwargs`) | Kiểu | Bắt buộc | Ý nghĩa |
| --- | --- | --- | --- |
| `fields` | string[] | Không | Các cột cần trả về, ví dụ `["id","login","name","group_id"]`. Các field như `email` được lấy từ partner liên kết. |

### Ví dụ

```bash
curl "$BASE_URL/base/admin/users/8" \
  -H "Authorization: Bearer $TOKEN"
```

Dạng RPC có danh sách field:

```bash
curl -X POST "$BASE_URL/rpc/base.users/read" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"args":[[8]],"kwargs":{"fields":["id","login","name","group_id"]}}'
```

### Response

HTTP `200`. Lưu ý `result` là **danh sách có một dòng**, không phải một object:

```json
{
  "id": "111020261535481234570001",
  "result": [
    { "id": 8, "login": "operator.hcm01", "name": "Operator HCM 01", "group_id": 3 }
  ]
}
```

| Field | Ý nghĩa |
| --- | --- |
| `result[0].id` | Id user. |
| `result[0].login` | Tên đăng nhập. |
| `result[0].name` | Tên hiển thị. |
| `result[0].group_id` | Group chính của user. Group quyết định user được làm gì. |

### Lỗi

Lỗi dùng định dạng chuẩn được mô tả trong API guide (xem "Response" → "Errors").

| HTTP | `error.code` | Ý nghĩa | Bạn nên làm gì |
| --- | --- | --- | --- |
| `400` | `-32602` hoặc `-32000` | Id không hợp lệ hoặc bạn yêu cầu field không tồn tại. | Kiểm tra `id` và `fields`. |
| `401` | `-32001` | Thiếu token hoặc token không hợp lệ. | Lấy token mới. |
| `403` | `-32003` | User của bạn không được đọc user này. | Nhờ admin cấp quyền. |

### Cần biết

- Password và API key không bao giờ được trả về. Khi đã đặt, bạn thấy ký tự thay thế `••••••••`. Khi chưa đặt, bạn thấy giá trị rỗng hoặc `null`.
- Không có danh sách `roles`. Quyền truy cập đến từ group của user (`group_id`).
