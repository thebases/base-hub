## Get Transaction Log

Trả về một dòng log callback thô theo ID dạng số. Dùng để xem chính xác terminal đã gửi gì và server đã xử lý ra sao.

### Before you start

- Một bearer token của Base HUB.
- Quyền đọc `edc.txn.log`.
- `id` của log lấy từ kết quả list hoặc search (hoặc `logId` do callback của terminal trả về).

### Request

```http
GET /base/txn-logs/{id}
Authorization: Bearer <token>
```

| Path parameter | Type | Required | Ý nghĩa |
| --- | --- | --- | --- |
| `id` | integer | Yes | ID log, ví dụ `88231`. Không phải UUID và không phải ID giao dịch. |

### Example

```bash
curl "$BASE_URL/base/txn-logs/88231" \
  -H "Authorization: Bearer $TOKEN"
```

### Response

HTTP `200`. Dòng log nằm trong `result`.

```json
{
  "id": "111020261535481234570001",
  "result": {
    "id": 88231,
    "appVersion": "1.3.0",
    "bankApp": "VCB",
    "deviceId": "SN0001",
    "gps": "10.7769,106.7009",
    "requestBody": "{\"transactionId\":\"TXN20260926000123\",\"amount\":150000}",
    "response": "{\"code\":\"00\",\"message\":\"OK\"}",
    "transactionId": "TXN20260926000123",
    "transactionTime": "2026-09-26 15:04:05",
    "type": "SALE",
    "logType": "EDC_RESULT_REQUEST",
    "idempotencyKey": "SN0001-TXN20260926000123-RESULT",
    "parseStatus": "SUCCESS",
    "parseAttempts": 1,
    "sourceIp": "10.20.1.15",
    "userAgent": "okhttp/4.12.0",
    "receivedAt": "2026-09-26T08:04:06Z",
    "processedAt": "2026-09-26T08:04:06Z"
  }
}
```

| Field | Ý nghĩa |
| --- | --- |
| `requestBody`, `response` | Payload thô dạng chuỗi. Tự parse nếu cần. |
| `logType` | Đây là lệnh gọi nào: `EDC_INIT_REQUEST`, `EDC_RESULT_REQUEST`, `EDC_SETTLE_REQUEST`, `MQTT_COMMAND`, ... |
| `parseStatus` | `PENDING`, `PROCESSING`, `SUCCESS`, `FAILED` hoặc `IGNORED`. |
| `parseError` | Vì sao xử lý lỗi. Chỉ có khi `parseStatus` là `FAILED`. |

Các field tùy chọn rỗng (`idempotencyKey`, `parseError`, `sourceIp`, `userAgent`, `lockedAt`, `processedAt`) bị bỏ.

### Errors

Lỗi dùng định dạng chuẩn mô tả trong API guide (xem "Response" → "Errors").

| HTTP | `error.code` | Ý nghĩa | Bạn nên làm gì |
| --- | --- | --- | --- |
| `400` | `-32602` | `id` không phải số nguyên. | Gửi một số. |
| `401` | `-32001` | Thiếu hoặc sai token. | Lấy token mới. |
| `403` | `-32003` | Bạn không có quyền đọc model này. | Nhờ admin cấp quyền. |
| `404` | `-32004` | Không có log nào có ID này. | Kiểm tra ID. |
| `500` | `-32603` | Lỗi database. | Thử lại sau. |

### Good to know

- Để tìm log theo ID giao dịch, dùng `GET /base/txn-logs?transactionId=...`.
