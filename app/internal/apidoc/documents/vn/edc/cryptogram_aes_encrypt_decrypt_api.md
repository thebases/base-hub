## Mã hóa hoặc giải mã AES

Mã hóa văn bản, hoặc giải mã một chuỗi base64, bằng khóa AES do bạn gửi. Dùng để kiểm tra thiết bị hoặc client của bạn tạo ra cùng kết quả mã hóa với backend.

### Trước khi bắt đầu

- Không cần đăng nhập. Đây là route hỗ trợ công khai.
- Bạn cần một khóa AES ghi dạng hex (32, 48 hoặc 64 ký tự hex). Lấy khóa thử từ `GET /keys`.

### Request

```http
POST /aes
Content-Type: application/json
```

| Tên | Kiểu | Bắt buộc | Ý nghĩa |
| --- | --- | --- | --- |
| `data` | string | Có | Mã hóa: văn bản gốc, ví dụ `{"serial":"SN0001"}`. Giải mã: chuỗi base64 đã mã hóa. |
| `key` | string | Có | Khóa AES dạng hex: 32, 48 hoặc 64 ký tự (AES-128, AES-192 hoặc AES-256). |
| `isEncrypt` | boolean | Không | `true` = mã hóa. `false` hoặc bỏ trống = giải mã. |

Thuật toán: AES-CBC, IV (khối khởi đầu) toàn số 0, đệm PKCS#7, kết quả base64 chuẩn.

### Ví dụ

```bash
curl -X POST "$BASE_URL/aes" \
  -H "Content-Type: application/json" \
  -d '{"data":"{\"serial\":\"SN0001\",\"amount\":150000}","key":"<64-hex-char-aes-key>","isEncrypt":true}'
```

### Response

Mã hóa (`isEncrypt: true`):

```json
{ "code": 0, "data": { "encryptedData": "<base64-ciphertext>" } }
```

Giải mã (`isEncrypt: false`):

```json
{ "code": 0, "data": { "rawData": "{\"serial\":\"SN0001\",\"amount\":150000}" } }
```

| Trường | Ý nghĩa |
| --- | --- |
| `code` | `0` nghĩa là thành công. |
| `data.encryptedData` | Chuỗi base64 đã mã hóa (chỉ khi mã hóa). |
| `data.rawData` | Văn bản đã giải mã (chỉ khi giải mã). |

Route này không dùng envelope `{"id","result"}` trong hướng dẫn API. Nó giữ dạng đơn giản `{code, data}`.

### Lỗi

Mọi lỗi ở đây đều có HTTP status `500`. Lý do thật nằm ở trường `code` trong body: `{"code": 400, "message": "..."}`.

| HTTP | `code` / `message` | Ý nghĩa | Bạn nên làm gì |
| --- | --- | --- | --- |
| `500` | `400` / `invalid request body` | Body không phải JSON hợp lệ. | Sửa JSON. |
| `500` | `500` / `aes: decode key: ...` | Giải mã: `key` không phải hex hợp lệ. | Chỉ gửi ký tự hex. |
| `500` | `500` / `invalid key length: must be 16, 24, or 32 bytes` | Mã hóa: khóa không có 32, 48 hoặc 64 ký tự hex. | Dùng khóa từ `GET /keys`. |
| `500` | `500` / `crypto/aes: invalid key size N` | Giải mã: khóa sai độ dài. | Dùng khóa hex 32, 48 hoặc 64 ký tự. |
| `500` | `500` / `illegal base64 data at input byte N` | Giải mã: `data` không phải base64 hợp lệ. | Gửi đúng chuỗi mã hóa bạn nhận được. |
| `500` | `500` / `aes: ciphertext must be a non-empty multiple of block size` | Giải mã: chuỗi mã hóa rỗng hoặc sai độ dài. | Kiểm tra chuỗi không bị cắt hoặc sửa. |

### Cần biết

- IV luôn bằng 0, nên cùng văn bản và cùng khóa luôn cho cùng kết quả. Phù hợp để test, không phù hợp để bảo vệ dữ liệu thật.
- Giải mã bằng khóa sai thường không báo lỗi. Bạn nhận về văn bản rác trong `rawData`, nên hãy tự so sánh kết quả.
- Mã hóa với khóa không phải hex hợp lệ có thể làm hàm bên dưới bị crash thay vì trả lỗi. Luôn gửi khóa hex hợp lệ.
