## EDC Transaction Result

Terminal gọi API này sau khi bank app trả kết quả của một giao dịch (sale, void, QR...). Lần gọi này làm giao dịch xuất hiện, với status và số tiền cuối cùng, trong danh sách giao dịch của admin.

> **Warning:** Đây là callback của thiết bị với body bị đóng băng (frozen). Firmware của terminal đọc response này, nên tên field và cấu trúc không thay đổi.

### In a nutshell

1. Terminal gọi **init** (bắt đầu thanh toán), rồi ngân hàng trả lời.
2. Terminal gọi **result** với request và response của ngân hàng, gửi dưới dạng chuỗi JSON.
3. Server quyết định status: `APPROVED`, `FAILED` hoặc `TIMEOUT`. Một **VOID** (hủy giao dịch bán trước đó) được duyệt sẽ chuyển giao dịch bán đó sang `REVERSED`.
4. Sau đó terminal gọi **settle** (xem "EDC Batch Settle"). Settlement là tổng kết cuối lô của ngân hàng.

Các status của giao dịch, giải thích đơn giản:

| Status | Ý nghĩa |
| --- | --- |
| `INITIATED` | Giao dịch đã bắt đầu; chưa có phản hồi. |
| `PENDING_DEVICE` | Lệnh bắt đầu đã gửi tới terminal và server đang chờ nó. |
| `PROCESSING` | Terminal đang xử lý giao dịch. |
| `APPROVED` | Ngân hàng đã chấp nhận giao dịch. |
| `DECLINED` / `FAILED` | Ngân hàng từ chối, hoặc giao dịch thất bại. |
| `TIMEOUT` | Không có phản hồi kịp thời. |
| `REVERSED` | Giao dịch bán đã duyệt bị hủy bằng void. |
| `SETTLED` | Lô chứa giao dịch này đã được chốt với ngân hàng. |

Status chỉ đi tiến. `FAILED` đến muộn sau `APPROVED` sẽ bị bỏ qua.

### Before you start

Xác thực thiết bị giống "EDC Transaction Init": chế độ `public` (mặc định, không cần token, server tìm thiết bị từ `deviceId`) hoặc `token` (`Authorization: Bearer <serial>:<secretVersion>:<deviceSecret>`). Header bạn gửi luôn được kiểm tra. `X-Device-Serial`, `X-Timestamp`, `X-Nonce`, `X-Signature` bị bỏ qua.

### Request

```http
POST /base/transaction/result
Content-Type: application/json
```

Body giống `/transaction/init`.

| Body field | Type | Required | Ý nghĩa |
| --- | --- | --- | --- |
| `deviceId` | string (tối đa 128) | Yes | Serial terminal, ví dụ `SN0001`. Key cũ `device_id`. |
| `transactionId` | string (tối đa 128) | Yes | Cùng ID đã gửi cho init. Với **VOID**, gửi `transactionId` của giao dịch bán gốc. Key cũ `transaction_id`. |
| `requestBody` | string | Xem ghi chú | Request của bank app **dưới dạng chuỗi JSON**: `transactionID`, `transactionAmount` (chuỗi số nguyên), `currencyName`, `tranxType`, `transDateTime`, `thirdPartyId`. Key cũ `request_body`. |
| `response` | string | Xem ghi chú | Response của bank app **dưới dạng chuỗi JSON**: `{"Code","Desc","tranxType","Response":{isoResponseCode, approveCode, totalAmount, currency, cardNumber (đã che), cardHolder, cardType, swipeType, aid, appName, merchantId, terminalId, refNo, traceNo, batchNo, invoiceNo, merchantTransId, transDateTime}}`. Một object phẳng có các field của `Response` cũng được chấp nhận. |
| `type` | string (tối đa 32) | No | `SALE`, `VOID`... Chỉ dùng khi các chuỗi JSON không có `tranxType`. |
| `gps`, `appVersion`, `bankApp` | string (tối đa 128) | No | Thông tin ngữ cảnh của terminal. |

Ghi chú: `requestBody` và `response` không bắt buộc khi validate, nhưng nếu không đọc được `response` thì giao dịch được lưu là `FAILED`. Nếu một trong hai không phải JSON hợp lệ, giao dịch sẽ không bao giờ xuất hiện trong danh sách admin.

Cách server quyết định:

- **Status:** `APPROVED` khi `Code` là `"200"` hoặc `isoResponseCode` là `"00"`. `TIMEOUT` khi `Desc` chứa "timeout". Còn lại là `FAILED`.
- **Số tiền:** `Response.totalAmount`, nếu không có thì `requestBody.transactionAmount`. Chuỗi số nguyên theo đơn vị nguyên (`"150000"` = 150.000 VND). Số thập phân hoặc dấu phân cách bị bỏ qua (số tiền `0`).
- **Thời gian:** `Response.transDateTime`, nếu không có thì `requestBody.transDateTime` (RFC 3339, `2006-01-02 15:04:05+07:00`, hoặc `20060102150405` theo UTC), nếu không có nữa thì lấy thời điểm server nhận lệnh gọi.

### Example

```bash
curl -X POST "$BASE_URL/base/transaction/result" \
  -H "Content-Type: application/json" \
  -d '{
    "deviceId": "SN0001",
    "transactionId": "TXN20260926000123",
    "type": "SALE",
    "requestBody": "{\"transactionID\":\"TXN20260926000123\",\"transactionAmount\":\"150000\",\"currencyName\":\"VND\",\"tranxType\":\"SALE\"}",
    "response": "{\"Code\":\"200\",\"Desc\":\"Success\",\"tranxType\":\"SALE\",\"Response\":{\"isoResponseCode\":\"00\",\"approveCode\":\"123456\",\"totalAmount\":\"150000\",\"currency\":\"VND\",\"cardNumber\":\"411111******1111\",\"cardType\":\"CHIP\",\"merchantId\":\"000000012345678\",\"terminalId\":\"70010001\",\"refNo\":\"626908123456\",\"traceNo\":\"000321\",\"batchNo\":\"000012\",\"invoiceNo\":\"000045\",\"transDateTime\":\"2026-09-26T08:15:00+07:00\"}}",
    "bankApp": "BANKAPP"
  }'
```

### Response

HTTP `200`, định dạng riêng (không có envelope `result`).

```json
{
  "success": true,
  "data": {
    "accepted": true,
    "duplicate": false,
    "transactionId": "TXN20260926000123",
    "status": "APPROVED",
    "receivedAt": "2026-09-26T08:15:02.401+07:00",
    "logId": "5531"
  }
}
```

| Field | Ý nghĩa |
| --- | --- |
| `accepted` | `true` khi server đã lưu lần gọi này. |
| `duplicate` | `true` khi result này đã được ghi trước đó (gọi lại). Không có gì thay đổi. |
| `transactionId` | ID giao dịch. |
| `status` | Status đã lưu sau lần gọi này: `APPROVED`, `FAILED`, `TIMEOUT`, hoặc `REVERSED` với void đã duyệt. |
| `receivedAt` | Lúc server nhận result này lần đầu. |
| `logId` | ID của log callback thô. |

### Errors

Lỗi của callback giữ định dạng cũ của thiết bị: `{"code": "...", "message": "..."}`. Lỗi validation có dạng `{"errors":[{"field":"TransactionID","message":"..."}]}`.

| HTTP | `code` | Ý nghĩa | Bạn nên làm gì |
| --- | --- | --- | --- |
| `400` | `invalid_json` | Body không phải JSON hợp lệ. | Sửa JSON. |
| `400` | không có (danh sách `errors`) | Thiếu `deviceId` hoặc `transactionId`, hoặc một field quá dài. | Gửi đủ các field bắt buộc. |
| `401` | không có | Thiếu hoặc sai token (chế độ `token`, hoặc có gửi header bearer). Body: `{"error":"missing_device_token"` hoặc `"invalid_device_token", ...}`. | Sửa token. |
| `404` | `device_not_found` | Không có thiết bị nào có serial này. | Kiểm tra serial (viết hoa). |
| `409` | không có | Đã có result cùng loại cho thiết bị và `transactionId` này với **result code khác**, hoặc `transactionId` thuộc thiết bị khác. | Result đầu tiên được giữ. Nhờ operator sửa; đừng gửi lại. |
| `500` | `internal_error` | Lỗi server hoặc database. | Có thể thử lại an toàn. |

### Good to know

- **Gọi lại là an toàn.** Mỗi `deviceId` + loại (`SALE`, `VOID`...) + `transactionId` chỉ giữ một result. Gửi lại cùng result code trả về `200` với `duplicate: true`. **Số tiền hoặc dữ liệu thẻ thay đổi khi gọi lại sẽ bị bỏ qua**; gọi lại không phải là cập nhật.
- `VOID` là một result riêng trên `transactionId` của giao dịch bán gốc. Void được duyệt chuyển giao dịch bán từ `APPROVED` sang `REVERSED`. Void bị từ chối để nguyên giao dịch bán. Nếu giao dịch bán chưa được lưu, log của void giữ `PENDING` và server sẽ thử lại.
- Result không có init trước vẫn được chấp nhận. Giao dịch được tạo từ result.
- Log, thay đổi status và dòng journal được lưu trong cùng một database transaction, nên chúng thấy được ngay khi lệnh gọi trả về. Nếu payload không xử lý được (ví dụ JSON sai bên trong `requestBody`), bạn vẫn nhận `200 accepted: true` nhưng log là `FAILED`. Kiểm tra `POST /base/transaction/logs/search` (`parseStatus`, `parseError`).
- Với token hợp lệ, serial lấy từ token; nếu không thì lấy từ `deviceId`, và thiết bị này phải đã đăng ký.
