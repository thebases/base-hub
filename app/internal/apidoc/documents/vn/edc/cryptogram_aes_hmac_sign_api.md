## Ký và/hoặc mã hóa JSON

Nhận một đối tượng JSON và, nếu bạn yêu cầu, ký bằng HMAC-SHA256 (chữ ký chứng minh dữ liệu không bị sửa) và/hoặc mã hóa bằng AES-256. Mỗi lần gọi server tạo khóa ngẫu nhiên mới và trả lại, để bạn làm lại đúng các bước đó trong client của mình.

### Trước khi bắt đầu

Không cần đăng nhập. Đây là route hỗ trợ công khai cho việc phát triển và test.

### Request

```http
POST /aeshmac
Content-Type: application/json
```

| Tên | Kiểu | Bắt buộc | Ý nghĩa |
| --- | --- | --- | --- |
| `jsonObj` | object | Có | Dữ liệu cần ký hoặc mã hóa. Dùng cặp key/value phẳng, ví dụ `{"serial":"SN0001","amount":150000}`. |
| `keyList` | string[] | Không | Các key của `jsonObj` đưa vào chữ ký, ví dụ `["serial","amount"]`. Rỗng hoặc bỏ trống = tất cả key. |
| `needSign` | boolean | Không | `true` để tạo chữ ký HMAC. Mặc định `false`. |
| `needAES256` | boolean | Không | `true` để mã hóa toàn bộ `jsonObj`. Mặc định `false`. |

Cách tạo từng phần:

- **HMAC:** các key được chọn sắp xếp theo thứ tự chữ cái (không theo thứ tự trong `keyList`) rồi nối thành `key1=value1&key2=value2`. Chuỗi này được ký bằng HMAC-SHA256. Chữ ký là hex chữ thường. Khóa HMAC được dùng nguyên văn là chuỗi 64 ký tự hex, không giải mã thành byte.
- **AES-256:** `jsonObj` được rút gọn (key theo thứ tự chữ cái), rồi mã hóa bằng AES-CBC, IV toàn 0, đệm PKCS#7, kết quả base64. Khóa AES được giải mã hex thành 32 byte.

### Ví dụ

```bash
curl -X POST "$BASE_URL/aeshmac" \
  -H "Content-Type: application/json" \
  -d '{
    "jsonObj": {"serial":"SN0001","amount":150000,"billId":"INV20260926"},
    "keyList": ["serial","amount"],
    "needSign": true,
    "needAES256": true
  }'
```

### Response

Cả sáu trường luôn có mặt. Phần bạn không yêu cầu là chuỗi rỗng.

```json
{
  "code": 0,
  "data": {
    "hmacRawData": "amount=150000&serial=SN0001",
    "hmacKey": "<64-hex-char-hmac-key>",
    "hmacSignData": "<64-hex-char-hmac-sha256-signature>",
    "aesMinifiedJson": "{\"amount\":150000,\"billId\":\"INV20260926\",\"serial\":\"SN0001\"}",
    "aesKey": "<64-hex-char-aes-key>",
    "aesEncryptData": "<base64-ciphertext>"
  }
}
```

| Trường | Ý nghĩa |
| --- | --- |
| `hmacRawData` | Chuỗi chính xác đã được ký. |
| `hmacKey` | Khóa HMAC ngẫu nhiên đã dùng. |
| `hmacSignData` | Chữ ký HMAC-SHA256 (hex). |
| `aesMinifiedJson` | Chuỗi JSON chính xác đã được mã hóa. |
| `aesKey` | Khóa AES ngẫu nhiên đã dùng. |
| `aesEncryptData` | Chuỗi base64 đã mã hóa. |

Route này giữ dạng đơn giản `{code, data}`, không dùng envelope `{"id","result"}` trong hướng dẫn API.

### Lỗi

Mọi lỗi ở đây đều có HTTP status `500`. Lý do thật nằm ở trường `code` trong body.

| HTTP | `code` / `message` | Ý nghĩa | Bạn nên làm gì |
| --- | --- | --- | --- |
| `500` | `400` / `invalid request body` | Body không phải JSON hợp lệ. | Sửa JSON. |
| `500` | `500` / `key 'x' not found in data` | Một phần tử của `keyList` không phải key của `jsonObj`. Chỉ kiểm tra khi `needSign` là `true`. | Sửa lỗi chính tả trong `keyList`. |

### Cần biết

- Bạn không thể gửi khóa của riêng mình. Muốn dùng khóa tự chọn, hãy gọi `POST /aes`.
- Giữ `jsonObj` phẳng. Đối tượng lồng nhau bị in theo kiểu Go (ví dụ `map[a:1]`) trong `hmacRawData`.
- Số từ 10.000.000 trở lên bị in dạng `1e+07` trong `hmacRawData`. Hãy gửi số tiền lớn dưới dạng string.
