## Get Transaction

Trả về một giao dịch từ transaction journal (danh sách giao dịch của admin). Dùng khi bạn đã biết `id` dạng số từ kết quả search.

### Before you start

- Một bearer token của Base HUB (`Authorization: Bearer <token>`).
- Quyền đọc `edc.transaction.journal`.

### Request

```http
GET /base/transaction/{id}
Authorization: Bearer <token>
```

| Path parameter | Type | Required | Ý nghĩa |
| --- | --- | --- | --- |
| `id` | integer | Yes | ID journal, ví dụ `1024`. Đây **không** phải chuỗi `transactionId` của terminal. |

### Example

```bash
curl "$BASE_URL/base/transaction/1024" \
  -H "Authorization: Bearer $TOKEN"
```

### Response

HTTP `200`. Giao dịch nằm trong `result`.

```json
{
  "id": "111020261535481234570001",
  "result": {
    "id": 1024,
    "reqId": "TXN20260926000123",
    "billId": "TXN20260926000123",
    "transactionId": "TXN20260926000123",
    "txnDate": "2026-09-26T08:15:00+07:00",
    "txnType": "SALE",
    "txnStatus": "APPROVED",
    "txnAmount": 150000,
    "cardNo": "411111******1111",
    "edcSerial": "SN0001",
    "edcId": "SN0001",
    "tid": "70010001",
    "mid": "000000012345678",
    "aliasValue": "BANKAPP",
    "aid": "A0000000031010",
    "appName": "VISA CREDIT",
    "invoiceNo": "000045",
    "batchNo": "000012",
    "stan": "000321",
    "cardHolder": "NGUYEN VAN A",
    "cardType": "CHIP",
    "settleId": "SN0001:000012:STL20260926000012",
    "de37RefCode": "626908123456",
    "de39AuthCode": "123456",
    "isSettled": true,
    "settledAt": "2026-09-26T15:00:00Z",
    "createdAt": "2026-09-26T01:15:02.481Z",
    "updatedAt": "2026-09-26T08:00:03.120Z"
  }
}
```

| Field | Ý nghĩa |
| --- | --- |
| `id` | ID journal (số bạn truyền trên URL). |
| `transactionId` | ID do terminal tạo cho giao dịch này. |
| `txnDate`, `txnType`, `txnStatus` | Thời điểm xảy ra, loại (`SALE`, `VOID`, `QR`...) và status (`APPROVED`, `FAILED`, `TIMEOUT`). |
| `txnAmount` | Số tiền theo đơn vị nguyên: `150000` = 150.000 VND. |
| `cardNo`, `cardHolder`, `cardType` | Số thẻ đã che, tên chủ thẻ, loại thẻ. |
| `edcSerial`, `tid`, `mid` | Serial terminal, terminal ID (TID) và merchant ID (MID). |
| `batchNo`, `invoiceNo`, `stan` | Số lô, số hóa đơn, số trace trên biên lai. |
| `de37RefCode`, `de39AuthCode` | Mã tham chiếu của ngân hàng và mã duyệt/phản hồi. |
| `settleId` | Settlement mà giao dịch này thuộc về. `null` cho đến khi lô được settle. |
| `isSettled`, `settledAt`, `isVoid`, `voidAt`, `voidJson` | Các dấu settle và void. Bị bỏ khi chưa có. |

### Errors

Lỗi dùng định dạng chuẩn mô tả trong API guide (xem "Response" → "Errors").

| HTTP | `error.code` | Ý nghĩa | Bạn nên làm gì |
| --- | --- | --- | --- |
| `400` | `-32602` | `id` không phải số nguyên. | Gửi một số. |
| `401` | `-32001` | Thiếu hoặc sai token. | Lấy token mới. |
| `403` | `-32003` | Bạn không có quyền đọc model này. | Nhờ admin cấp quyền. |
| `404` | `-32004` | Không có giao dịch nào có ID này. | Search trước để tìm đúng ID. |
| `500` | `-32603` | Lỗi database. | Thử lại sau. |

### Good to know

- `id` là số thứ tự dòng journal, không phải `transactionId`. Để tìm dòng theo `transactionId`, dùng "Search Transactions".
