## Create or Update Transaction

Tạo một dòng journal thủ công, hoặc sửa một dòng đã có. Dùng để operator chỉnh sửa. Giao dịch bình thường được tạo tự động từ callback của terminal.

### Before you start

- Một bearer token của Base HUB.
- Quyền ghi `edc.transaction.journal`.

### Request

```http
PUT /base/transaction
Authorization: Bearer <token>
Content-Type: application/json
```

- Không có `id` (hoặc `id: 0`) nghĩa là **tạo mới**.
- Có `id` khác nghĩa là **cập nhật**. Chỉ các field bạn gửi mới bị đổi. Để xóa giá trị một field, gửi `""` hoặc `0`.
- Mỗi field cũng dùng được dưới tên snake_case (ví dụ `edc_serial`). Key không biết sẽ bị bỏ qua.

| Body field | Type | Required | Ý nghĩa |
| --- | --- | --- | --- |
| `id` | integer | No | ID journal cần cập nhật. Bỏ trống để tạo mới. |
| `transactionId` | string | Yes khi tạo | ID giao dịch của terminal, ví dụ `TXN20260926000123`. Không duy nhất: tạo hai lần sẽ ra hai dòng. |
| `reqId`, `billId` | string | No | Request ID và bill ID. |
| `txnDate` | string (RFC 3339) | No | Thời gian giao dịch, ví dụ `2026-09-26T08:15:00+07:00`. Nếu bỏ trống là `0001-01-01T00:00:00Z`. |
| `txnType` | string | No | `SALE`, `VOID`, `QR`... |
| `txnStatus` | string | No | `APPROVED`, `FAILED`, `TIMEOUT`... Không được kiểm tra. |
| `txnAmount` | integer | No | Đơn vị tiền nguyên, ví dụ `150000`. Không có số thập phân. |
| `cardNo` | string | No | Chỉ số thẻ đã che, ví dụ `411111******1111`. |
| `edcSerial` | string | No | Serial terminal, ví dụ `SN0001`. |
| `edcId` | string | No | Terminal ID do thiết bị gửi. |
| `tid`, `mid` | string | No | Terminal ID và merchant ID. |
| `aliasValue`, `aid`, `appName`, `invoiceNo`, `batchNo`, `stan`, `cardHolder`, `cardType` | string | No | Thông tin thẻ và biên lai. |
| `settleId` | string | No | Khóa settlement mà dòng này thuộc về. Không được kiểm tra với settlement thật. Rỗng được lưu là `null` (chưa settle). |
| `de37RefCode` | string | No | Mã tham chiếu của ngân hàng (RRN, ISO 8583 field 37). |
| `de39AuthCode` | string | No | Mã duyệt/phản hồi (field 39). |

Bạn không thể đặt `isVoid`, `isSettled`, `settledAt` hay `voidAt` ở đây. Khi cập nhật, chúng giữ nguyên giá trị.

### Example

```bash
curl -X PUT "$BASE_URL/base/transaction" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"transactionId":"TXN20260926000123","txnDate":"2026-09-26T08:15:00+07:00","txnType":"SALE","txnStatus":"APPROVED","txnAmount":150000,"cardNo":"411111******1111","edcSerial":"SN0001","tid":"70010001","mid":"000000012345678","batchNo":"000012","invoiceNo":"000045"}'
```

### Response

HTTP `200` cho cả tạo mới và cập nhật. Dòng đã lưu nằm trong `result`.

```json
{
  "id": "111020261535481234570001",
  "result": {
    "id": 1025,
    "transactionId": "TXN20260926000123",
    "txnDate": "2026-09-26T08:15:00+07:00",
    "txnType": "SALE",
    "txnStatus": "APPROVED",
    "txnAmount": 150000,
    "cardNo": "411111******1111",
    "edcSerial": "SN0001",
    "tid": "70010001",
    "mid": "000000012345678",
    "invoiceNo": "000045",
    "batchNo": "000012",
    "settleId": null,
    "createdAt": "2026-09-26T02:00:00.100Z",
    "updatedAt": "2026-09-26T02:00:00.100Z"
  }
}
```

Ví dụ đã rút gọn. Response thật có đủ mọi field của "Get Transaction"; field bạn không gửi là chuỗi rỗng.

### Errors

Lỗi dùng định dạng chuẩn mô tả trong API guide (xem "Response" → "Errors").

| HTTP | `error.code` | Ý nghĩa | Bạn nên làm gì |
| --- | --- | --- | --- |
| `400` | `-32602` | Body không phải JSON object hợp lệ. | Sửa body. |
| `401` | `-32001` | Thiếu hoặc sai token. | Lấy token mới. |
| `403` | `-32003` | Bạn không có quyền ghi model này. | Nhờ admin cấp quyền. |
| `404` | `-32004` | Bạn gửi `id` nhưng không có giao dịch đó. | Kiểm tra ID. |
| `500` | `-32603` | Lỗi database hoặc validation, ví dụ thiếu `transactionId` khi tạo, hoặc không đọc được `txnDate`. | Sửa field rồi thử lại. |

### Good to know

- **Không idempotent.** Gọi lại lệnh tạo sẽ thêm dòng thứ hai. Sau khi timeout, hãy search theo `transactionId` trước khi gọi lại.
- Cập nhật là một phần. Bạn không cần gửi cả dòng.
- Callback result sau đó của terminal cho cùng `transactionId` sẽ ghi đè các chỉnh sửa thủ công của bạn.
- Cũng gọi được bằng `POST /base/edc/transaction/upsert` (RPC: `POST /rpc/edc.transaction.journal/upsert`). Gửi dòng trong `kwargs`. Các dòng trả về dùng key snake_case, và `id` không tồn tại hoặc lỗi khác trả về `400` với thông báo dạng text.
