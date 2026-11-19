## Get Device Error Log

Trả về một lỗi mà terminal đã báo (event c=1899), kèm toàn bộ payload gốc. Dùng khi bạn đã có `id` của error log từ danh sách.

### Trước khi bắt đầu

- Bạn cần bearer token (xem API guide).
- User cần quyền truy cập `edc.device.error.log`. Hiện chưa có dòng quyền nào cấp model này cho user thường, nên chỉ superadmin đọc được cho đến khi thêm.

### Yêu cầu

```http
GET /base/device-error-logs/{id}
Authorization: Bearer <token>
```

| Tên | Kiểu | Bắt buộc | Ý nghĩa |
| --- | --- | --- | --- |
| `id` | integer (path) | Có | Id của error log, ví dụ `512`. |

### Ví dụ

```bash
curl "$BASE_URL/base/device-error-logs/512" \
  -H "Authorization: Bearer $TOKEN"
```

### Phản hồi

`200 OK`. Error log nằm trong envelope `result` chuẩn.

```json
{
  "id": "111020261535481234570001",
  "result": {
    "id": 512,
    "serialNumber": "A910123456",
    "errorCode": "REMOTE_CMD_DROPPED",
    "errorMsg": "Inbound MQTT payload dropped: not a parseable EdcMessage (137 chars)",
    "appVersion": "2.3.0",
    "occurredAt": "2026-09-26T08:05:45.678Z",
    "sentAt": "2026-09-26T08:05:45Z",
    "payload": {
      "serialNumber": "A910123456",
      "errorCode": "REMOTE_CMD_DROPPED",
      "errorMsg": "Inbound MQTT payload dropped: not a parseable EdcMessage (137 chars)",
      "appVersion": "2.3.0",
      "occurredAt": 1790412345678,
      "deviceId": "A910123456"
    },
    "createdAt": "2026-09-26T08:05:46Z"
  }
}
```

| Field | Ý nghĩa |
| --- | --- |
| `serialNumber` | Serial của terminal. Luôn lấy từ envelope của message, không lấy từ payload. |
| `errorCode`, `errorMsg` | Mã lỗi ngắn và thông báo dễ đọc. |
| `requestId` | Id của lệnh từ xa mà lỗi này thuộc về, nếu có. Bị bỏ khi không gửi. |
| `txnId` | Id giao dịch trên terminal, nếu có. Bị bỏ khi không gửi. |
| `appVersion` | Phiên bản app trên terminal. Bị bỏ khi không gửi. |
| `occurredAt` / `sentAt` / `createdAt` | Lúc lỗi xảy ra trên terminal / lúc terminal gửi / lúc server lưu. |
| `payload` | Event gốc đúng như terminal gửi, gồm cả các field từ app phiên bản mới hơn. |

Log chỉ ghi thêm (append-only): không có cách sửa hay xóa.

> **Check:** thời gian ở trên hiển thị dạng text; API guide nói response dùng Unix seconds. Hãy kiểm tra định dạng trên server của bạn.

### Lỗi

Lỗi dùng định dạng chuẩn được mô tả trong API guide (xem "Response" → "Errors").

| HTTP | `error.code` | Ý nghĩa | Bạn nên làm gì |
| --- | --- | --- | --- |
| `400` | `-32602` | `id` không phải số. | Gửi id là số nguyên. |
| `401` | `-32001` | Thiếu hoặc sai token. | Lấy token mới. |
| `403` | `-32003` | User không có quyền truy cập `edc.device.error.log`. | Nhờ admin cấp quyền. |
| `404` | `-32004` | Không có error log nào với `id` này. | Kiểm tra lại id. |
| `500` | `-32603` | Lỗi server không mong đợi. | Thử lại sau và báo `id` của response. |
