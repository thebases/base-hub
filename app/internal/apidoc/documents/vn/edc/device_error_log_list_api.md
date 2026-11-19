## List Device Error Logs

Trả về một trang các lỗi do terminal báo (event c=1899 `EVT_DEVICE_ERROR`, gửi qua MQTT trên topic `base/evt`), lỗi xảy ra gần nhất trước. Dùng để theo dõi sức khỏe thiết bị theo `errorCode`, hoặc để tìm vì sao một lệnh từ xa cứ `PENDING` mà không có kết quả (khớp theo `requestId`).

### Trước khi bắt đầu

- Bạn cần bearer token (xem API guide).
- User cần quyền truy cập `edc.device.error.log`. Hiện chưa có dòng quyền nào cấp model này cho user thường, nên chỉ superadmin đọc được cho đến khi thêm.

### Yêu cầu

```http
GET /base/device-error-logs?serial={serial}&error_code={errorCode}&request_id={requestId}&txn_id={txnId}&limit={limit}&offset={offset}
Authorization: Bearer <token>
```

| Tên | Kiểu | Bắt buộc | Ý nghĩa |
| --- | --- | --- | --- |
| `serial` | string | Không | Một phần của serial, không phân biệt hoa thường, ví dụ `A9101`. |
| `error_code` | string | Không | Một phần của mã lỗi, không phân biệt hoa thường. `REMOTE_TXN_` tìm cả nhóm lỗi lệnh từ xa. |
| `request_id` | string | Không | Id chính xác của lệnh từ xa gốc, ví dụ `req-20260926-0001`. |
| `txn_id` | string | Không | Id giao dịch chính xác từ app terminal, ví dụ `20260926101530`. |
| `limit` | integer | Không | Số dòng mỗi trang, ví dụ `20`. Mặc định `50`, tối đa `500`. Giá trị 0, lớn hơn 500 hoặc không phải số sẽ thành `50`. |
| `offset` | integer | Không | Số dòng bỏ qua. Mặc định `0`. |

Filter để trống sẽ bị bỏ qua. Kết quả sắp xếp theo `occurredAt` (mới nhất trước), rồi `id`.

### Ví dụ

```bash
curl "$BASE_URL/base/device-error-logs?serial=A910123456&error_code=REMOTE_TXN_&limit=20" \
  -H "Authorization: Bearer $TOKEN"
```

### Phản hồi

`200 OK`.

```json
{
  "id": "111020261535481234570001",
  "result": {
    "items": [
      {
        "id": 512,
        "serialNumber": "A910123456",
        "errorCode": "REMOTE_TXN_PENDING_EXPIRED",
        "errorMsg": "c=100 got no terminal result within 30 min",
        "requestId": "req-20260926-0001",
        "txnId": "20260926101530",
        "appVersion": "2.3.0",
        "occurredAt": "2026-09-26T08:05:45.678Z",
        "sentAt": "2026-09-26T08:05:45Z",
        "payload": {
          "serialNumber": "A910123456",
          "errorCode": "REMOTE_TXN_PENDING_EXPIRED",
          "errorMsg": "c=100 got no terminal result within 30 min",
          "requestId": "req-20260926-0001",
          "txnId": "20260926101530",
          "appVersion": "2.3.0",
          "occurredAt": 1790412345678,
          "deviceId": "A910123456"
        },
        "createdAt": "2026-09-26T08:05:46Z"
      }
    ],
    "total": 1
  }
}
```

| Field | Ý nghĩa |
| --- | --- |
| `items` | Các lỗi trong trang này. Mỗi item có các field giống *Get Device Error Log*. |
| `total` | Tổng số lỗi khớp filter (không phụ thuộc `limit` và `offset`). |

`requestId`, `txnId`, `appVersion` và `sentAt` bị bỏ khi terminal không gửi.

### Lỗi

Lỗi dùng định dạng chuẩn được mô tả trong API guide (xem "Response" → "Errors").

| HTTP | `error.code` | Ý nghĩa | Bạn nên làm gì |
| --- | --- | --- | --- |
| `401` | `-32001` | Thiếu hoặc sai token. | Lấy token mới. |
| `403` | `-32003` | User không có quyền truy cập `edc.device.error.log`. | Nhờ admin cấp quyền. |
| `500` | `-32603` | Lỗi server không mong đợi. | Thử lại sau và báo `id` của response. |

### Cần biết

- `occurredAt` là lúc lỗi xảy ra trên terminal. Có thể sớm hơn `sentAt`: lỗi phát sinh trước khi MQTT kết nối (ví dụ lúc khởi động) được giữ lại và gửi sau c=1800 kế tiếp.
- Terminal gửi cùng một cặp `errorCode` và `errorMsg` tối đa một lần mỗi 30 giây, nên danh sách này không đếm hết mọi lần xảy ra. Lịch sử đầy đủ nằm trong file device log.
- Khi một lỗi có `requestId` khớp với một lệnh còn mở của thiết bị đó, server cũng đánh dấu lệnh đó là `FAILED` (xem `GET /base/device/command/{id}`, các field `errorCode` và `errorMessage`).
- Gửi theo kiểu best-effort, tối đa một lần. Không có dòng nào không chứng minh là lỗi không xảy ra.
