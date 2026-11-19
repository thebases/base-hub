## Lấy access token

Đăng nhập bằng username và password và nhận về bearer token (một chuỗi khóa bạn gửi trong header `Authorization`). Mọi route được bảo vệ đều cần token này. Thường đây là call đầu tiên bạn gọi.

### Request

```http
POST /oauth/token
Content-Type: application/json
```

`POST /auth/token` là cùng một route với tên khác. Route này public, nên không cần token. Body có thể là JSON hoặc `application/x-www-form-urlencoded` với cùng tên field.

| Tên | Kiểu | Bắt buộc | Ý nghĩa |
| --- | --- | --- | --- |
| `grant_type` | string | Có | Cách đăng nhập: `password`, `refresh_token`, `client_credentials` hoặc `authorization_code`. |
| `username` | string | Với `password` | Tên đăng nhập, ví dụ `operator01`. |
| `password` | string | Với `password` | Password. |
| `refresh_token` | string | Với `refresh_token` | `refresh_token` từ response trước. |
| `client_id` / `client_secret` | string | Với `client_credentials` | Một OAuth client đã đăng ký. Dùng cho call giữa các service. |
| `db` | string | Không | Tên tenant database, ví dụ `base_hub`. Rỗng nghĩa là database gốc. Với `authorization_code` thì bị bỏ qua. Database lấy từ header `X-Database-Name`. |

`authorization_code` (với `code`, `redirect_uri`, `code_verifier`) được dùng bởi luồng đăng nhập trên trình duyệt (`GET /oauth/authorize`). Bình thường bạn không gọi nó bằng tay.

### Ví dụ

```bash
curl -X POST "$BASE_URL/oauth/token" \
  -H "Content-Type: application/json" \
  -d '{"grant_type":"password","username":"operator01","password":"S3cret-pass"}'
```

### Response

HTTP `200`. Route này dùng định dạng OAuth 2.0. Không có envelope `id` / `result`.

```json
{
  "access_token": "eyJhbGciOiJSUzI1NiJ9.<payload>.<signature>",
  "refresh_token": "5f9d6c3b1a...",
  "expires_in": 900,
  "token_type": "Bearer"
}
```

| Field | Ý nghĩa |
| --- | --- |
| `access_token` | Token. Gửi dưới dạng `Authorization: Bearer <access_token>`. |
| `refresh_token` | Dùng để lấy access token mới mà không cần password. Chỉ có khi grant cấp nó. |
| `expires_in` | Số giây cho đến khi access token hết hạn. Mặc định `900` (15 phút). Có thể cấu hình. |
| `token_type` | Luôn là `Bearer`. |

### Lỗi

Lỗi dùng định dạng OAuth 2.0: `{"error": "<code>", "error_description": "<text>"}`.

| HTTP | `error` | Ý nghĩa | Bạn nên làm gì |
| --- | --- | --- | --- |
| `400` | `invalid_request` | `username` rỗng với grant `password`, hoặc không parse được JSON. | Sửa body. |
| `400` | `unsupported_grant_type` | Thiếu `grant_type` hoặc không thuộc bốn giá trị. | Gửi `grant_type` hợp lệ. |
| `401` | `invalid_grant` | Sai username hoặc password, hoặc refresh token hay code không hợp lệ hoặc đã hết hạn. | Kiểm tra thông tin đăng nhập hoặc đăng nhập lại. |
| `401` | `invalid_client` | `client_credentials`: client không tồn tại hoặc sai secret. | Kiểm tra `client_id` và `client_secret`. |
| `429` | `invalid_request` | Đăng nhập sai quá nhiều lần với login và địa chỉ IP này. | Chờ số giây ghi trong header `Retry-After`. |

### Cần biết

- Hãy refresh trước khi access token hết hạn: gọi route này với `grant_type=refresh_token`. Refresh token **xoay vòng**: mỗi response cho một token mới, nên hãy lưu lại.
- `POST /oauth/revoke` (form field `refresh_token`) thu hồi một refresh token.
- Token không chứa role. Quyền được kiểm tra khi bạn gọi từng route.
- Với nhiều database, gửi cùng `db` ở đây và header `X-Database-Name` ở các call sau. Token bị từ chối nếu dùng cho database khác.
- Body JSON không có `grant_type` được đọc như định dạng đăng nhập cũ `{"params":{"db","login","password","context"}}`. Nó trả `{"result":{"uid","db","access_token","refresh_token","expires_in","token_type","user_context","username","auth_type","db_header"}}`. Lỗi của nó là văn bản thuần. Code mới hãy dùng `grant_type=password`.
