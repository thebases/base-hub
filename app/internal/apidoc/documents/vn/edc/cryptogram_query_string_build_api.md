## Tạo chuỗi để ký

Biến một đối tượng JSON thành chuỗi `key=value&key=value` dùng làm đầu vào HMAC. Dùng để kiểm tra client của bạn tạo đúng chuỗi giống hệt trước khi ký.

### Request

```http
POST /jsonstring
Content-Type: application/json
```

Không cần đăng nhập.

| Tên | Kiểu | Bắt buộc | Ý nghĩa |
| --- | --- | --- | --- |
| `jsonObj` | object | Có | Dữ liệu key/value phẳng, ví dụ `{"serial":"SN0001","amount":150000}`. |
| `keyList` | string[] | Không | Các key cần đưa vào. Rỗng hoặc bỏ trống = tất cả key của `jsonObj`. |

Key luôn được sắp xếp theo thứ tự chữ cái, bất kể thứ tự trong `keyList`. Giá trị được in nguyên, không mã hóa URL.

### Ví dụ

```bash
curl -X POST "$BASE_URL/jsonstring" \
  -H "Content-Type: application/json" \
  -d '{"jsonObj":{"serial":"SN0001","amount":150000,"billId":"INV20260926"},"keyList":["serial","billId","amount"]}'
```

### Response

```json
{
  "code": 0,
  "data": { "result": "amount=150000&billId=INV20260926&serial=SN0001" }
}
```

| Trường | Ý nghĩa |
| --- | --- |
| `data.result` | Chuỗi cần ký. |

Route này giữ dạng đơn giản `{code, data}`, không dùng envelope `{"id","result"}` trong hướng dẫn API.

### Lỗi

HTTP status của mọi lỗi là `500`. Lý do thật nằm ở trường `code` trong body.

| HTTP | `code` / `message` | Ý nghĩa | Bạn nên làm gì |
| --- | --- | --- | --- |
| `500` | `400` / `invalid request body` | Body không phải JSON hợp lệ, hoặc một phần tử của `keyList` không phải key của `jsonObj`. Hai trường hợp cùng một thông báo. | Kiểm tra JSON và chính tả của key. |

### Cần biết

- Sắp xếp phân biệt hoa thường: key viết hoa đứng trước key viết thường (`Z` trước `a`).
- Số lớn có thể bị in dạng `1e+07` (với `10000000`). Hãy gửi số tiền lớn dưới dạng string.
- Giữ `jsonObj` phẳng. Đối tượng lồng nhau bị in theo kiểu Go (`map[k:v]`).
