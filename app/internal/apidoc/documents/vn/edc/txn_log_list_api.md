## List Transaction Logs

Trả về một trang log thô: mọi request và response trao đổi với terminal, cùng các sự kiện MQTT và hệ thống liên quan. Mới nhất trước. Dùng để lần theo xem terminal thực sự đã gửi gì cho một giao dịch.

### Before you start

- Một bearer token của Base HUB.
- Quyền đọc `edc.txn.log`.

### Request

```http
GET /base/txn-logs?limit={limit}&offset={offset}&transactionId={transactionId}&device_id={device_id}&log_type={log_type}&parse_status={parse_status}
Authorization: Bearer <token>
```

| Query parameter | Type | Required | Ý nghĩa |
| --- | --- | --- | --- |
| `limit` | integer | No | Kích thước trang. Mặc định `50`, tối đa `500`. Giá trị `<= 0`, `> 500` hoặc không phải số thành `50`. |
| `offset` | integer | No | Số dòng bỏ qua. Mặc định `0`. |
| `transactionId` | string | No | Khớp chính xác, ví dụ `TXN20260926000123`. `transaction_id` cũng dùng được. |
| `device_id` | string | No | Serial terminal khớp chính xác, ví dụ `SN0001`. |
| `log_type` | string | No | `EDC_INIT_REQUEST`, `EDC_INIT_RESPONSE`, `EDC_RESULT_REQUEST`, `EDC_RESULT_RESPONSE`, `EDC_SETTLE_REQUEST`, `MANAGEMENT_INIT_REQUEST`, `MANAGEMENT_INIT_RESPONSE`, `MQTT_COMMAND`, `MQTT_ACK`, `MQTT_EVENT` hoặc `SYSTEM_EVENT`. |
| `parse_status` | string | No | `PENDING`, `PROCESSING`, `SUCCESS`, `FAILED` hoặc `IGNORED`. |

### Example

```bash
curl "$BASE_URL/base/txn-logs?transactionId=TXN20260926000123&limit=20" \
  -H "Authorization: Bearer $TOKEN"
```

### Response

HTTP `200`.

```json
{
  "id": "111020261535481234570001",
  "result": {
    "items": [
      {
        "id": 88231,
        "deviceId": "SN0001",
        "requestBody": "{\"transactionId\":\"TXN20260926000123\",\"amount\":150000}",
        "response": "{\"code\":\"00\",\"message\":\"OK\"}",
        "transactionId": "TXN20260926000123",
        "type": "SALE",
        "logType": "EDC_RESULT_REQUEST",
        "parseStatus": "SUCCESS",
        "parseAttempts": 1,
        "receivedAt": "2026-09-26T08:04:06Z",
        "processedAt": "2026-09-26T08:04:06Z"
      }
    ],
    "total": 1
  }
}
```

Ví dụ đã rút gọn. Mỗi item có cùng các field như "Get Transaction Log".

| Field | Ý nghĩa |
| --- | --- |
| `items` | Trang các dòng log. |
| `total` | Số dòng khớp bộ lọc. Không phụ thuộc `limit` và `offset`. |
| `requestBody`, `response` | Payload thô dạng chuỗi. Tự parse nếu cần. |
| `parseError` | Vì sao xử lý lỗi. Chỉ có khi `parseStatus` là `FAILED`. |

### Errors

Lỗi dùng định dạng chuẩn mô tả trong API guide (xem "Response" → "Errors").

| HTTP | `error.code` | Ý nghĩa | Bạn nên làm gì |
| --- | --- | --- | --- |
| `401` | `-32001` | Thiếu hoặc sai token. | Lấy token mới. |
| `403` | `-32003` | Bạn không có quyền đọc model này. | Nhờ admin cấp quyền. |
| `500` | `-32603` | Lỗi database. | Thử lại sau. |

### Good to know

- Giá trị bộ lọc phân biệt hoa thường. Gửi `log_type` và `parse_status` viết hoa. Giá trị không biết sẽ không khớp dòng nào.
- Sắp xếp theo `receivedAt`, mới nhất trước.
- Để khớp một phần `transactionId` hoặc lọc QR/CARD, dùng "Search Transaction Callback Logs".
