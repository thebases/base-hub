## EDC Batch Settle

Terminal gọi API này sau khi bank app chốt (settle) một lô giao dịch. Server lưu tổng của lô thành một **settlement** (quyết toán) và đánh dấu các giao dịch của lô đó là đã settle.

> **Warning:** Đây là callback của thiết bị với body bị đóng băng (frozen). Firmware của terminal đọc response này, nên tên field và cấu trúc không thay đổi.

### In a nutshell

- Một **batch** (lô) là nhóm giao dịch của một terminal kể từ lần chốt trước. Nó có `batchNo`, ví dụ `000012`.
- **Settlement** nghĩa là ngân hàng và terminal thống nhất tổng của lô (bao nhiêu giao dịch bán, bao nhiêu void, bao nhiêu tiền).
- Sau lần gọi này, các giao dịch đã duyệt của lô chuyển từ `APPROVED` sang `SETTLED`. Giao dịch `REVERSED` giữ nguyên.
- Chỉ gửi settle sau khi tất cả result của lô đã đến. Xem sơ đồ ở "EDC Transaction Init".

### Before you start

Xác thực thiết bị giống "EDC Transaction Init": chế độ `public` (mặc định; server tìm thiết bị từ `deviceId`) hoặc `token` (`Authorization: Bearer <serial>:<secretVersion>:<deviceSecret>`). Header bạn gửi luôn được kiểm tra. `X-Device-Serial`, `X-Timestamp`, `X-Nonce`, `X-Signature` bị bỏ qua.

### Request

```http
POST /base/transaction/settle
Content-Type: application/json
```

| Body field | Type | Required | Ý nghĩa |
| --- | --- | --- | --- |
| `batchNo` | string (tối đa 32) | Yes | Số lô, ví dụ `000012`. |
| `deviceId` | string (tối đa 128) | Yes | Serial terminal, ví dụ `SN0001`. Key cũ `device_id`. |
| `requestBody` | string | Yes | Request settle của bank app **dưới dạng chuỗi JSON**; phải là JSON hợp lệ. Server đọc `transactionID` (hoặc `merchant_transID`) và `tranxType`. Key cũ `request_body`. |
| `response` | string | Yes | Response settle của bank app **dưới dạng chuỗi JSON**; phải là JSON hợp lệ. Server đọc `batchNo`, `currency`, `isoResponseCode`, `merchantId`, `terminalId`, `acqr_name`, `merchantName`, `quantity`, `totalAmount`, `saleQuantity`, `saleTotalAmount`, `voidQuantity`, `voidTotalAmount`. Mảng `Issuer` (tùy chọn) có thể chứa `totalQuantity`, `totalAmount`, `saleQuantity`, `saleTotalAmount`, `voidQuantity`, `voidTotalAmount` theo từng issuer (dùng khi giá trị ở cấp ngoài bị thiếu). |
| `transactionTime` | string (tối đa 64) | Yes | Thời gian settle: RFC 3339, `2006-01-02 15:04:05+07:00`, hoặc `20060102150405` (UTC). Nếu không đọc được, dùng thời điểm nhận. Key cũ `transaction_time`. |

Số lượng và số tiền là **chuỗi số nguyên**. Số tiền theo đơn vị tiền nguyên (`"3750000"` = 3.750.000 VND). Giá trị có số thập phân hoặc dấu phân cách thành `0`.

### Example

```bash
curl -X POST "$BASE_URL/base/transaction/settle" \
  -H "Content-Type: application/json" \
  -d '{
    "batchNo": "000012",
    "deviceId": "SN0001",
    "transactionTime": "2026-09-26T22:00:00+07:00",
    "requestBody": "{\"transactionID\":\"STL20260926000012\",\"tranxType\":\"SETTLE\"}",
    "response": "{\"batchNo\":\"000012\",\"currency\":\"VND\",\"isoResponseCode\":\"00\",\"merchantId\":\"000000012345678\",\"terminalId\":\"70010001\",\"acqr_name\":\"BANKAPP\",\"quantity\":\"25\",\"totalAmount\":\"3750000\",\"saleQuantity\":\"26\",\"saleTotalAmount\":\"3900000\",\"voidQuantity\":\"1\",\"voidTotalAmount\":\"150000\",\"transDateTime\":\"2026-09-26T22:00:00+07:00\"}"
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
    "batchNo": "000012",
    "transactionId": "STL20260926000012",
    "receivedAt": "2026-09-26T22:00:01.207+07:00",
    "logId": "5602"
  }
}
```

| Field | Ý nghĩa |
| --- | --- |
| `accepted` | `true` khi server đã lưu lần gọi này. |
| `duplicate` | `true` khi settle này đã được ghi trước đó (gọi lại). |
| `batchNo` | Số lô. |
| `transactionId` | `requestBody.transactionID` (hoặc `merchant_transID`); nếu thiếu thì là `batchNo`. |
| `receivedAt` / `logId` | Lúc server nhận, và ID của log callback thô. |

### Errors

Lỗi của callback giữ định dạng cũ của thiết bị: `{"code": "...", "message": "..."}`. Lỗi validation có dạng `{"errors":[{"field":"BatchNo","message":"..."}]}`.

| HTTP | `code` | Ý nghĩa | Bạn nên làm gì |
| --- | --- | --- | --- |
| `400` | `invalid_json` | Body không phải JSON hợp lệ. | Sửa JSON. |
| `400` | không có (danh sách `errors`) | Thiếu field bắt buộc, field rỗng hoặc quá dài. | Gửi đủ các field bắt buộc. |
| `401` | không có | Thiếu hoặc sai token (chế độ `token`, hoặc có gửi header bearer). | Sửa token. |
| `404` | `device_not_found` | Không có thiết bị nào có serial này. | Kiểm tra serial (viết hoa). |
| `500` | `internal_error` | `requestBody` hoặc `response` không phải JSON hợp lệ, hoặc lỗi server/database. | JSON sai thì không thử lại được: hãy sửa payload. |

### Good to know

- **Gọi lại là an toàn nhưng là cuối cùng.** Một settle được xác định bởi `deviceId` + `batchNo` + `transactionId` của settle. Gửi lại trả về `200` với `duplicate: true`, và **tổng mới trong lần gọi lại bị bỏ qua**. Để sửa tổng, dùng `transactionId` settle mới hoặc nhờ operator (`PUT /base/settlement`).
- Settlement có `settleId = "<deviceId>:<batchNo>:<transactionId>"`, ví dụ `SN0001:000012:STL20260926000012`. Tìm bằng `POST /base/settlement/search`.
- Mọi dòng giao dịch của **terminal này** có cùng `batchNo` (lấy từ `response.batchNo`, nếu không thì `batchNo` trong body) nhận `settleId` đó, `isSettled: true` và `settledAt`. Dòng đã settle dưới `settleId` khác thì không bị đụng tới.
- Nếu `response.isoResponseCode` có mặt và khác `00`, settlement vẫn được lưu (với `de39AuthCode` = mã đó) nhưng không giao dịch nào được đánh dấu settled. Log là `IGNORED`.
- Settlement, việc cập nhật các dòng, và bước đối chiếu tổng của terminal với các dòng đã settle (`MATCHED` hoặc `MISMATCH`) được lưu trong cùng một database transaction. Kết quả đối chiếu được lưu trên settlement và đọc được qua `/rpc`.
