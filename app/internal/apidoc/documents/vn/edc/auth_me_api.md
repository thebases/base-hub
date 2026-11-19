## Lấy user hiện tại

Cho biết token thuộc về ai. Gọi ngay sau khi đăng nhập để hiển thị tên user, ngôn ngữ và phạm vi công ty.

### Request

```http
GET /oauth/me
Authorization: Bearer <token>
```

`GET /auth/me` là cùng một route với tên khác. Route này không cần token hợp lệ để trả lời. Xem "Response".

| Tên | Kiểu | Bắt buộc | Ý nghĩa |
| --- | --- | --- | --- |
| `X-Database-Name` (header) hoặc `db` (query) | string | Không | Dùng tenant database nào, ví dụ `base_hub`. Mặc định là database gốc. Nếu gửi cả hai thì `db` thắng. |
| `X-API-Key` (header) | string | Không | Dùng thay cho Bearer token. Chỉ hoạt động khi server bật `app.supportApikey`. |

### Ví dụ

```bash
curl "$BASE_URL/oauth/me" \
  -H "Authorization: Bearer $TOKEN"
```

### Response

HTTP `200`:

```json
{
  "result": {
    "db": "base_hub",
    "uid": 7,
    "is_web": true,
    "valid": true,
    "auth_mode": "oauth",
    "username": "operator01",
    "db_header": "X-Database-Name",
    "user_context": { "lang": "en_US", "allowed_company_ids": [1], "company_id": 1 }
  }
}
```

| Field | Ý nghĩa |
| --- | --- |
| `result.valid` | `true` khi token tốt. **Luôn kiểm tra field này.** |
| `result.uid` | Id user. Là `1` khi `valid` là `false`. |
| `result.db` | Database mà call đang dùng. |
| `result.auth_mode` | `oauth` (Bearer token), `apikey` (`X-API-Key`) hoặc `none` (không có danh tính hợp lệ). |
| `result.username` | Tên đăng nhập. Chỉ có khi `valid` là `true`. |
| `result.db_header` | Tên header để gửi database. Chỉ có khi `valid` là `true`. |
| `result.user_context` | Ngôn ngữ (`lang`), các công ty được phép (`allowed_company_ids`) và công ty hiện tại (`company_id`) của user. Chỉ có khi `valid` là `true`. |

> **Warning:** Với token thiếu, sai hoặc hết hạn, server vẫn trả `200` với `valid: false`. Nó không trả `401`. Đừng dựa vào HTTP status.

### Lỗi

Lỗi của route này là body văn bản thuần kèm HTTP status. Không phải JSON.

| HTTP | Ý nghĩa | Bạn nên làm gì |
| --- | --- | --- |
| `400` | Không tìm thấy tên database. | Kiểm tra `db` hoặc `X-Database-Name`. |

### Cần biết

- Token của database khác cũng cho `valid: false`.
- Response được gửi kèm `Cache-Control: no-store`, nên không bao giờ bị cache.
- Để lấy các field profile như name, email và group, đọc dòng user: `GET /base/admin/users/{id}`.
