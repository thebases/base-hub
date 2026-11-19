## Search Transaction Callback Logs

Liệt kê các lệnh gọi thô mà terminal đã gửi tới server (`init`, `result`, `settle`), mới nhất trước. Dùng để kiểm tra chính xác terminal đã gửi gì và server đã xử lý chưa.

### Before you start

- Một bearer token của Base HUB.
- Quyền đọc `edc.txn.log`.

### Request

```http
POST /base/transaction/logs/search
Authorization: Bearer <token>
Content-Type: application/json
```

Bộ lọc đặt ở cấp ngoài cùng của body **hoặc** trong object `filters` (camelCase hoặc snake_case). Body là tùy chọn. Body không rỗng mà không phải JSON object sẽ trả về `400`.

| Body field | Type | Required | Ý nghĩa |
| --- | --- | --- | --- |
| `transactionId` | string | No | Khớp một phần, không phân biệt hoa thường, ví dụ `TXN2026`. |
| `logType` | string | No | Khớp chính xác: `EDC_INIT_REQUEST`, `EDC_INIT_RESPONSE`, `EDC_RESULT_REQUEST`, `EDC_RESULT_RESPONSE`, `EDC_SETTLE_REQUEST`, `MANAGEMENT_INIT_REQUEST`, `MANAGEMENT_INIT_RESPONSE`, `MQTT_COMMAND`, `MQTT_ACK`, `MQTT_EVENT`, `SYSTEM_EVENT`. |
| `parseStatus` | string | No | Khớp chính xác: `PENDING`, `PROCESSING`, `SUCCESS`, `FAILED`, `IGNORED`. |
| `deviceId` | string | No | Serial terminal khớp chính xác, ví dụ `SN0001` (`device_id` cũng dùng được). |
| `type` | string | No | `QR`, `CARD`, hoặc một loại chính xác như `SALE` (không phân biệt hoa thường). Cùng quy tắc với transaction search. Alias `txnType`, `transactionType`. |
| `limit` | integer | No | Mặc định `50`. Giá trị `<= 0` hoặc `> 500` thành `50`. |
| `offset` | integer | No | Mặc định `0`. |

### Example

```bash
curl -X POST "$BASE_URL/base/transaction/logs/search" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"transactionId":"TXN20260926000123","logType":"EDC_RESULT_REQUEST"}'
```

### Response

HTTP `200`. `result` là danh sách các dòng log.

```json
{
  "id": "111020261535481234570001",
  "result": [
    {
      "id": 5531,
      "appVersion": "2.4.1",
      "bankApp": "BANKAPP",
      "deviceId": "SN0001",
      "gps": "21.0285,105.8542",
      "requestBody": "{\"transactionID\":\"TXN20260926000123\",\"transactionAmount\":\"150000\",\"currencyName\":\"VND\",\"tranxType\":\"SALE\"}",
      "response": "{\"Code\":\"200\",\"Desc\":\"Success\",\"tranxType\":\"SALE\",\"Response\":{\"isoResponseCode\":\"00\",\"totalAmount\":\"150000\"}}",
      "transactionId": "TXN20260926000123",
      "transactionTime": "2026-09-26T01:15:00Z",
      "type": "SALE",
      "logType": "EDC_RESULT_REQUEST",
      "idempotencyKey": "result:SN0001:SALE:TXN20260926000123",
      "parseStatus": "SUCCESS",
      "parseAttempts": 1,
      "sourceIp": "10.0.4.17",
      "userAgent": "okhttp/4.12.0",
      "receivedAt": "2026-09-26T01:15:02.401Z",
      "processedAt": "2026-09-26T01:15:02.470Z"
    }
  ]
}
```

| Field | Ý nghĩa |
| --- | --- |
| `requestBody`, `response` | Chuỗi JSON thô của terminal, đúng như đã nhận. Tự parse nếu cần. |
| `logType` | Dòng này ghi lại lệnh gọi nào (xem danh sách bộ lọc). |
| `parseStatus` | Server đã áp dụng chưa? Xem "Good to know". |
| `parseAttempts` | Số lần server đã thử xử lý. |
| `receivedAt`, `processedAt` | Lúc nhận và lúc xử lý. |

### Errors

Lỗi dùng định dạng chuẩn mô tả trong API guide (xem "Response" → "Errors").

| HTTP | `error.code` | Ý nghĩa | Bạn nên làm gì |
| --- | --- | --- | --- |
| `400` | `-32602` | Body không phải JSON object hợp lệ. | Sửa body. |
| `401` | `-32001` | Thiếu hoặc sai token. | Lấy token mới. |
| `403` | `-32003` | Bạn không có quyền đọc model này. | Nhờ admin cấp quyền. |
| `500` | `-32603` | Lỗi database. | Thử lại sau. |

### Good to know

- Sắp xếp theo `receivedAt`, mới nhất trước. Không khớp gì thì `[]`. Không trả về tổng số.
- `parseStatus`: `SUCCESS` = đã áp dụng vào giao dịch hoặc settlement. `PENDING` = đang chờ áp dụng (cũng là void đang chờ giao dịch bán của nó). `FAILED` = không áp dụng được; xem `parseError`. `IGNORED` = chỉ để audit (lệnh init, sự kiện đổi trạng thái, hoặc void/settle mà ngân hàng không duyệt).
- Lệnh result và settle được xử lý trong cùng database transaction với lệnh gọi, nên thường bạn thấy `SUCCESS`, `FAILED` hoặc `IGNORED` ngay. Log `PENDING` được thử lại tự động (tối đa 10 lần, sau đó `FAILED`). Operator có thể xếp lại log `FAILED` bằng "Requeue Failed Transaction Log".
- Cũng gọi được bằng `POST /base/edc/transaction/logs/search` (RPC: `POST /rpc/edc.txn.log/log_search`). Gửi bộ lọc trong `kwargs`. Các dòng trả về dùng key snake_case (`request_body`, `parse_status`).
