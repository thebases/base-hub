## Tạo khóa HMAC và AES

Tạo một khóa HMAC và một khóa AES-256 ngẫu nhiên mới. Dùng khi phát triển để lấy khóa cho các route hỗ trợ khác (`POST /aes`, `POST /aeshmac`).

### Request

```http
GET /keys
```

Không cần đăng nhập. Ai truy cập được server đều gọi được. Các khóa là ngẫu nhiên và không liên quan đến bí mật thật nào.

### Ví dụ

```bash
curl "$BASE_URL/keys"
```

### Response

```json
{
  "code": 0,
  "data": {
    "hmacKey": "<64-hex-char-hmac-key>",
    "aesKey": "<64-hex-char-aes-key>"
  }
}
```

| Trường | Ý nghĩa |
| --- | --- |
| `code` | `0` nghĩa là thành công. |
| `data.hmacKey` | Khóa mới để ký HMAC: 32 byte ngẫu nhiên viết thành 64 ký tự hex. |
| `data.aesKey` | Khóa AES-256 mới: 32 byte ngẫu nhiên viết thành 64 ký tự hex. |

Route này giữ dạng đơn giản `{code, data}`, không dùng envelope `{"id","result"}` trong hướng dẫn API.

### Lỗi

| HTTP | `message` | Ý nghĩa | Bạn nên làm gì |
| --- | --- | --- | --- |
| `500` | `Fail to create keys` | Server không tạo được byte ngẫu nhiên. Body: `{"code": 500, "message": "Fail to create keys"}`. | Thử lại. Nếu vẫn lỗi, hãy báo cáo. |

### Cần biết

- Khóa mới mỗi lần gọi và không được lưu. Hãy tự lưu lại.
- Chỉ để test. Không bao giờ dùng khóa lấy qua HTTP làm bí mật production.
