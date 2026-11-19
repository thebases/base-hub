## Get Device Log

Trả về một log mà terminal đã upload, kèm thông tin về file đã upload. Dùng khi bạn đã biết `id` của log (ví dụ lấy từ danh sách) và muốn xem đầy đủ bản ghi.

### Trước khi bắt đầu

- Bạn cần bearer token (xem API guide) của user có quyền đọc device log (`edc.device.log`).
- Lấy `id` từ *List Device Logs* (`GET /base/device/log`).

### Yêu cầu

```http
GET /base/device/log/{id}
Authorization: Bearer <token>
```

| Tên | Kiểu | Bắt buộc | Ý nghĩa |
| --- | --- | --- | --- |
| `id` | integer (path) | Có | Id của log, ví dụ `311`. |

### Ví dụ

```bash
curl "$BASE_URL/base/device/log/311" \
  -H "Authorization: Bearer $TOKEN"
```

### Phản hồi

`200 OK`. Log nằm trong envelope `result` chuẩn.

```json
{
  "id": "111020261535481234570001",
  "result": {
    "id": 311,
    "deviceSerial": "SN0001",
    "level": "ERROR",
    "tag": "payment",
    "message": "uploaded device log file edc-20260926.zip",
    "metadata": { "appVersion": "1.3.2" },
    "originalFileName": "edc-20260926.zip",
    "storedFileName": "20260926T080000Z_edc-20260926.zip",
    "fileExtension": ".zip",
    "contentType": "application/zip",
    "compressionType": "zip",
    "fileSize": 48213,
    "storageProvider": "attachment",
    "storagePath": "device-logs/SN0001/2026/09/26/20260926T080000Z_edc-20260926.zip",
    "storageUrl": "/web/content/5120",
    "bankBincode": "970436",
    "loggedAt": "2026-09-26T07:59:30Z",
    "createdAt": "2026-09-26T08:00:00Z"
  }
}
```

| Field | Ý nghĩa |
| --- | --- |
| `deviceSerial` | Serial (số duy nhất in trên terminal) đã gửi log. |
| `level`, `tag` | Mức độ (ví dụ `ERROR`) và nhóm tự do (ví dụ `payment`). File upload không có tag sẽ có tag `file`. |
| `message` | Nội dung ngắn. Với file upload là `uploaded device log file <tên file>`. |
| `metadata` | Object JSON bổ sung do terminal gửi. |
| `originalFileName`, `storedFileName`, `fileExtension`, `contentType`, `compressionType`, `fileSize` | Thông tin file đã upload. `fileSize` tính bằng byte. |
| `storageProvider`, `storagePath`, `storageUrl` | Nơi lưu file. `storageUrl` là đường dẫn tải về trên host Base HUB. |
| `bankBincode` | Mã BIN ngân hàng liên quan, nếu terminal có gửi. |
| `loggedAt` / `createdAt` | Lúc terminal tạo log / lúc server lưu. |

Các field tùy chọn rỗng sẽ bị bỏ. Route này không có key `severity`; hãy đọc `level`. `storageBucket` chỉ xuất hiện ở dòng tạo bằng lệnh record và chỉ khi có đặt giá trị.

> **Check:** thời gian ở trên hiển thị dạng text; API guide nói response dùng Unix seconds. Hãy kiểm tra định dạng trên server của bạn.

### Lỗi

Lỗi dùng định dạng chuẩn được mô tả trong API guide (xem "Response" → "Errors").

| HTTP | `error.code` | Ý nghĩa | Bạn nên làm gì |
| --- | --- | --- | --- |
| `400` | `-32602` | `id` không phải số. | Gửi id là số nguyên. |
| `401` | `-32001` | Thiếu hoặc sai token. | Lấy token mới. |
| `403` | `-32003` | User không có quyền đọc device log. | Nhờ admin cấp quyền. |
| `404` | `-32004` | Không có log nào với `id` này. | Kiểm tra lại id. |
| `500` | `-32603` | Lỗi server không mong đợi. | Thử lại sau và báo `id` của response. |
