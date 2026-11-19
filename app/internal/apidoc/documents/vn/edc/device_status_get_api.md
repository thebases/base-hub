## Get Device Status

Trả về bản ghi trạng thái trực tiếp của một terminal theo serial: online hay không, telemetry về pin và mạng, và config đang dùng. Dùng cho màn hình chi tiết thiết bị.

### Trước khi bắt đầu

- Bạn cần bearer token (xem API guide) của user có quyền đọc `edc.device.status`.
- Lệnh gọi cũng đọc device và config liên kết bằng quyền của bạn, nên cần quyền đọc `edc.device` và `edc.device.config`. Nếu không sẽ nhận `403`.

### Yêu cầu

```http
GET /base/device/status/{serial}
Authorization: Bearer <token>
```

| Tên | Kiểu | Bắt buộc | Ý nghĩa |
| --- | --- | --- | --- |
| `serial` | string (path) | Có | Serial (số duy nhất in trên terminal), ví dụ `SN0001`. Khớp chính xác, phân biệt hoa thường. Khoảng trắng hai đầu được cắt. |

### Ví dụ

```bash
curl "$BASE_URL/base/device/status/SN0001" \
  -H "Authorization: Bearer $TOKEN"
```

### Phản hồi

`200 OK`. Body là chính bản ghi trạng thái, **không** bọc trong `result`.

```json
{
  "id": 7,
  "serial": "SN0001",
  "deviceId": 42,
  "device": {
    "id": 42,
    "serial": "SN0001",
    "imei": "356938035643809",
    "ownerId": 10,
    "holderId": 25,
    "groupId": 1,
    "active": true,
    "provisioningStatus": "ACTIVE",
    "secretVersion": 1,
    "createdAt": "2026-09-20T03:00:00Z",
    "updatedAt": "2026-09-20T03:10:00Z"
  },
  "configId": 14,
  "config": {
    "id": 14,
    "name": "default-retail",
    "version": 2,
    "configJson": { "owner": "THEBASE", "pingTime": 30 },
    "isActive": true,
    "createdAt": "2026-09-26T09:00:00Z"
  },
  "deviceConfig": { "owner": "THEBASE", "pingTime": 30 },
  "status": "ONLINE",
  "batteryPercent": 85,
  "charging": false,
  "networkType": "WIFI",
  "signalStrength": -60,
  "appVersion": "1.4.2",
  "firmwareVersion": "A920-2.1.0",
  "printerStatus": "OK",
  "paperStatus": "OK",
  "lastSeenAt": "2026-09-26T09:14:55Z",
  "metadata": { "ip": "192.168.1.20" },
  "createdAt": "2026-09-20T03:00:00Z",
  "updatedAt": "2026-09-26T09:14:55Z"
}
```

`config` và `deviceConfig` đã được rút gọn ở trên.

| Field | Ý nghĩa |
| --- | --- |
| `device` | Thiết bị đã đăng ký (`groupId` chỉ có khi thiết bị thuộc một group). |
| `configId`, `config` | Phiên bản config template mà thiết bị đang theo. Không có khi thiết bị dùng manual config hoặc chưa có config. |
| `deviceConfig` | Bản config đã lưu cho thiết bị này. |
| `status` | `OFFLINE`, `ONLINE`, `BUSY`, `ERROR` hoặc `MAINTENANCE`. |
| `batteryPercent`, `charging`, `networkType`, `signalStrength`, `appVersion`, `firmwareVersion`, `printerStatus`, `paperStatus`, `metadata` | Telemetry do terminal báo. |
| `lastSeenAt` | Lần cuối terminal báo cáo. Dùng để đánh giá dữ liệu còn mới hay không. |

Các field tùy chọn rỗng sẽ bị bỏ.

> **Check:** thời gian ở trên hiển thị dạng text; API guide nói response dùng Unix seconds. Hãy kiểm tra định dạng trên server của bạn. Route này cũng trả bản ghi không có envelope `{"id","result"}` mà API guide mô tả cho các route REST.

### Lỗi

Lỗi dùng định dạng chuẩn được mô tả trong API guide (xem "Response" → "Errors").

| HTTP | `error.code` | Ý nghĩa | Bạn nên làm gì |
| --- | --- | --- | --- |
| `401` | `-32001` | Thiếu hoặc sai token. | Lấy token mới. |
| `403` | `-32003` | Bạn không đọc được `edc.device.status`, `edc.device` hoặc `edc.device.config`. | Nhờ admin cấp quyền. |
| `404` | `-32004` | Không có bản ghi trạng thái cho serial này. | Kiểm tra chính tả. Serial phân biệt hoa thường. |
| `500` | `-32603` | Lỗi server không mong đợi. | Thử lại sau và báo `id` của response. |

### Cần biết

- API này chỉ đọc. Terminal tự cập nhật telemetry của nó.
- Nếu chỉ cần JSON config, dùng *Get Resolved Device Config*.
