## Tạo mã VietQR

Tạo mã QR chuyển khoản ngân hàng VietQR (NAPAS) cho một tài khoản và một số tiền. Trả về chuỗi QR (để bạn tự vẽ) và ảnh PNG dựng sẵn dạng base64.

### Request

```http
GET /qr?bankAccount={account}&binCode={bin}&amount={amount}&billId={billId}&narrative={text}
```

Không cần đăng nhập.

| Tên | Kiểu | Bắt buộc | Ý nghĩa |
| --- | --- | --- | --- |
| `bankAccount` | string | Có (không được kiểm tra) | Số tài khoản ngân hàng hoặc tài khoản ảo nhận tiền, ví dụ `0071000123456`. |
| `binCode` | string | Có | BIN ngân hàng 6 chữ số, ví dụ `970436`. Tra bằng `GET /banklist/{bankCode}`. |
| `amount` | string | Có | Số tiền VND, chỉ gồm chữ số, ví dụ `150000`. |
| `billId` | string | Không | Mã hóa đơn hoặc đơn hàng, đưa vào nội dung chuyển khoản, ví dụ `INV20260926`. |
| `narrative` | string | Không | Phần đầu của nội dung. Mặc định `CT`. |
| `logo` | string | Không | Logo PNG dạng base64 thô (không có tiền tố `data:`, đã mã hóa URL). Được vẽ nhỏ và tròn ở giữa. Không hỗ trợ URL. |

Nội dung chuyển khoản trong QR là `<narrative> BAS<độ dài billId, 2 chữ số><billId>`, ví dụ `CT BAS11INV20260926`.

### Ví dụ

```bash
curl -G "$BASE_URL/qr" \
  --data-urlencode "bankAccount=0071000123456" \
  --data-urlencode "binCode=970436" \
  --data-urlencode "amount=150000" \
  --data-urlencode "billId=INV20260926"
```

### Response

JSON thô, không có envelope:

```json
{
  "qrBase64": "iVBORw0KGgoAAAANSUhEUgAAAQAAAAEACAIAAADT...",
  "qrString": "00020101021238570010A00000072701270006970436011300710001234560208QRIBFTTA530370454061500005802VN62230819CT BAS11INV20260926630473CD"
}
```

| Trường | Ý nghĩa |
| --- | --- |
| `qrBase64` | Ảnh PNG 256 x 256. Hiển thị bằng `<img src="data:image/png;base64,{qrBase64}">`. |
| `qrString` | Chuỗi EMVCo/VietQR. Dùng thư viện QR bất kỳ để vẽ ở kích thước khác. |

### Lỗi

Route này không trả về status lỗi. Lỗi với `logo` vẫn cho HTTP `200`, với nội dung lỗi nằm trong `qrBase64`.

| HTTP | Giá trị `qrBase64` | Ý nghĩa | Bạn nên làm gì |
| --- | --- | --- | --- |
| `200` | `Base64 decode error` | `logo` không phải base64 hợp lệ. | Gửi base64 thô, đã mã hóa URL. |
| `200` | `Decode image error` | `logo` giải mã được nhưng không phải PNG. | Gửi ảnh PNG. |

### Cần biết

- Tham số thiếu không bị từ chối. `amount` hoặc `bankAccount` rỗng vẫn trả về một QR mà ứng dụng ngân hàng sẽ từ chối. Hãy tự kiểm tra ở phía bạn.
- Kiểm tra `qrBase64` bắt đầu bằng `iVBOR` (chữ ký PNG) trước khi hiển thị.
- Hãy mã hóa URL cho `logo` và `narrative` (`--data-urlencode` hoặc `encodeURIComponent`). Dấu `+` thô trong base64 sẽ thành khoảng trắng và làm hỏng logo.
