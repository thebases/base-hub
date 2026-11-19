## Search Transactions

Liệt kê các giao dịch (thẻ và QR) trong transaction journal, mới nhất trước. Dùng để dựng danh sách giao dịch cho admin hoặc tìm giao dịch theo terminal hay status.

### Before you start

- Một bearer token của Base HUB.
- Quyền đọc `edc.transaction.journal`.

### Request

```http
POST /base/transaction/search
Authorization: Bearer <token>
Content-Type: application/json
```

Bạn có thể đặt mỗi bộ lọc ở cấp ngoài cùng của body **hoặc** trong object `filters`. Key camelCase và snake_case đều dùng được (ví dụ `filters.edc_serial`). Body là tùy chọn: body rỗng trả về trang đầu không lọc. Body không rỗng mà không phải JSON object sẽ trả về `400`.

| Body field | Type | Required | Ý nghĩa |
| --- | --- | --- | --- |
| `transactionId` | string | No | Khớp một phần, không phân biệt hoa thường, ví dụ `TXN2026`. Tên cũ `txnId`. |
| `edcSerial` | string | No | Serial terminal khớp chính xác, ví dụ `SN0001`. |
| `txnStatus` | string | No | Khớp chính xác và phân biệt hoa thường: `APPROVED`, `FAILED` hoặc `TIMEOUT`. |
| `cardNo` | string | No | Số thẻ đã che, khớp chính xác như đã lưu, ví dụ `411111******1111`. |
| `txnType` | string | No | `QR` = mọi loại chứa "QR". `CARD` = mọi loại không phải QR. Giá trị khác khớp chính xác, không phân biệt hoa thường (`SALE`, `VOID`). Alias `transactionType`. |
| `limit` | integer | No | Kích thước trang. Mặc định `50`. Giá trị `<= 0` hoặc `> 500` thành `50`. |
| `offset` | integer | No | Số dòng bỏ qua. Mặc định `0`. |

### Example

```bash
curl -X POST "$BASE_URL/base/transaction/search" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"edcSerial":"SN0001","txnType":"CARD","limit":20,"offset":0}'
```

### Response

HTTP `200`. `result` là danh sách giao dịch. Không có tổng số.

```json
{
  "id": "111020261535481234570001",
  "result": [
    {
      "id": 1024,
      "transactionId": "TXN20260926000123",
      "txnDate": "2026-09-26T08:15:00+07:00",
      "txnType": "SALE",
      "txnStatus": "APPROVED",
      "txnAmount": 150000,
      "cardNo": "411111******1111",
      "edcSerial": "SN0001",
      "tid": "70010001",
      "mid": "000000012345678",
      "batchNo": "000012",
      "invoiceNo": "000045",
      "settleId": null,
      "de39AuthCode": "123456",
      "createdAt": "2026-09-26T01:15:02.481Z",
      "updatedAt": "2026-09-26T01:15:02.481Z"
    }
  ]
}
```

Ví dụ đã rút gọn. Mỗi dòng có cùng các field như "Get Transaction" (còn có `reqId`, `billId`, `edcId`, `aliasValue`, `aid`, `appName`, `stan`, `cardHolder`, `cardType`, `de37RefCode`).

### Errors

Lỗi dùng định dạng chuẩn mô tả trong API guide (xem "Response" → "Errors").

| HTTP | `error.code` | Ý nghĩa | Bạn nên làm gì |
| --- | --- | --- | --- |
| `400` | `-32602` | Body không phải JSON object hợp lệ. | Sửa body. |
| `401` | `-32001` | Thiếu hoặc sai token. | Lấy token mới. |
| `403` | `-32003` | Bạn không có quyền đọc model này. | Nhờ admin cấp quyền. |
| `500` | `-32603` | Lỗi database. | Thử lại sau. |

### Good to know

- `txnAmount` là số nguyên theo đơn vị nguyên: `150000` = 150.000 VND.
- `settleId` là `null` cho đến khi lô của terminal được settle (**settlement** là tổng kết cuối lô của ngân hàng). Khi đó `isSettled: true` và `settledAt` xuất hiện. Giao dịch bán đã void có `isVoid: true` và `voidAt`.
- Sắp xếp theo `id`, mới nhất trước. Không khớp gì thì `result: []`.
- Thiếu một giao dịch ở đây? Kiểm tra `POST /base/transaction/logs/search` xem callback có bị `FAILED` hay `PENDING` không.
- Cũng gọi được bằng `POST /base/edc/transaction/search` (RPC: `POST /rpc/edc.transaction.journal/txn_search`). Gửi bộ lọc trong `kwargs`. Các dòng trả về dùng key snake_case.
