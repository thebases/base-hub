## Create or Update Settlement

Tạo một settlement thủ công, hoặc sửa một settlement đã có. **Settlement** (quyết toán) là tổng kết cuối lô của ngân hàng cho một terminal. Dùng để operator chỉnh sửa. Bình thường settlement được tạo bởi lệnh "EDC Batch Settle" của terminal.

### Before you start

- Một bearer token của Base HUB.
- Quyền ghi `edc.settlement.journal`.

### Request

```http
PUT /base/settlement
Authorization: Bearer <token>
Content-Type: application/json
```

- Không có `id` (hoặc `id: 0`) nghĩa là **tạo mới**.
- Có `id` khác nghĩa là **cập nhật**. Chỉ các field bạn gửi mới bị đổi. Để xóa giá trị một field, gửi `""` hoặc `0`.
- Mỗi field cũng dùng được dưới tên snake_case (ví dụ `settle_id`). Key không biết sẽ bị bỏ qua.

| Body field | Type | Required | Ý nghĩa |
| --- | --- | --- | --- |
| `id` | integer | No | ID settlement cần cập nhật. Bỏ trống để tạo mới. |
| `settleId` | string | Yes (trên thực tế) | Khóa duy nhất, ví dụ `SN0001:000012:STL20260926000012`. |
| `settleDate` | string (RFC 3339) | Yes (trên thực tế) | Thời gian settle, ví dụ `2026-09-26T22:00:00+07:00`. Nếu bỏ trống là `0001-01-01T00:00:00Z`. |
| `batchNo` | string | No | Số lô, ví dụ `000012`. |
| `reqId`, `billId` | string | No | Request ID và bill ID. |
| `txnQuant`, `txnTotal` | integer | No | Số lượng ròng và số tiền ròng. |
| `saleQuant`, `saleAmount` | integer | No | Số lượng và số tiền bán. |
| `voidQuant`, `voidAmount` | integer | No | Số lượng và số tiền void. |
| `tid`, `mid` | string | No | Terminal ID và merchant ID. |
| `aliasValue` | string | No | Tên acquirer hoặc merchant. |
| `de37RefCode`, `de39AuthCode` | string | No | Mã tham chiếu và mã phản hồi (`00` = thành công). |

Số tiền là số nguyên theo đơn vị tiền nguyên (VND), ví dụ `3750000`. Tổng được lưu đúng như bạn gửi. Server không kiểm tra chúng với các giao dịch.

### Example

```bash
curl -X PUT "$BASE_URL/base/settlement" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"id":88,"reqId":"STL20260926000012","billId":"000012","settleId":"SN0001:000012:STL20260926000012","settleDate":"2026-09-26T22:00:00+07:00","batchNo":"000012","txnQuant":25,"txnTotal":3750000,"saleQuant":26,"saleAmount":3900000,"voidQuant":1,"voidAmount":150000,"tid":"70010001","mid":"000000012345678","aliasValue":"BANKAPP","de39AuthCode":"00"}'
```

### Response

HTTP `200` cho cả tạo mới và cập nhật. Dòng đã lưu nằm trong `result`.

```json
{
  "id": "111020261535481234570001",
  "result": {
    "id": 88,
    "settleId": "SN0001:000012:STL20260926000012",
    "settleDate": "2026-09-26T22:00:00+07:00",
    "batchNo": "000012",
    "txnQuant": 25,
    "txnTotal": 3750000,
    "saleQuant": 26,
    "saleAmount": 3900000,
    "voidQuant": 1,
    "voidAmount": 150000,
    "tid": "70010001",
    "mid": "000000012345678",
    "aliasValue": "BANKAPP",
    "de39AuthCode": "00",
    "createdAt": "2026-09-26T15:00:01.310Z",
    "updatedAt": "2026-09-26T16:02:44.018Z"
  }
}
```

Ví dụ đã rút gọn. Response thật có đủ mọi field của "Get Settlement".

### Errors

Lỗi dùng định dạng chuẩn mô tả trong API guide (xem "Response" → "Errors").

| HTTP | `error.code` | Ý nghĩa | Bạn nên làm gì |
| --- | --- | --- | --- |
| `400` | `-32602` | Body không phải JSON object hợp lệ. | Sửa body. |
| `401` | `-32001` | Thiếu hoặc sai token. | Lấy token mới. |
| `403` | `-32003` | Bạn không có quyền ghi model này. | Nhờ admin cấp quyền. |
| `404` | `-32004` | Bạn gửi `id` nhưng không có settlement đó. | Kiểm tra ID. |
| `500` | `-32603` | Lỗi database hoặc validation, ví dụ `settleId` đã được settlement khác dùng, hoặc không đọc được `settleDate`. | Sửa field rồi thử lại. |

### Good to know

- Không idempotent, nhưng tạo lại với cùng `settleId` sẽ lỗi `500` thay vì tạo trùng.
- Cập nhật là một phần. Bạn không cần gửi cả dòng.
- Đổi `settleId` khi cập nhật không đổi các giao dịch đang trỏ tới `settleId` cũ.
- Lệnh "EDC Batch Settle" sau đó cho cùng `settleId` sẽ ghi đè các tổng bạn đặt ở đây.
- Cũng gọi được bằng `POST /base/edc/settlement/upsert` (RPC: `POST /rpc/edc.settlement.journal/upsert`). Gửi dòng trong `kwargs`. Các dòng trả về dùng key snake_case, và `id` không tồn tại hoặc lỗi khác trả về `400` với thông báo dạng text.
