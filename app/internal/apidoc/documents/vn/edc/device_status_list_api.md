## List Device Statuses

Trả về một trang các bản ghi trạng thái trực tiếp: online, pin, mạng, phiên bản app và config mà mỗi terminal đang dùng. Dùng cho bảng theo dõi cả đội thiết bị.

### Trước khi bắt đầu

- Bạn cần bearer token (xem API guide) của user có quyền đọc `edc.device.status`.
- Mỗi dòng cũng đọc device và config của nó bằng quyền của bạn, nên cần quyền đọc `edc.device` và `edc.device.config`. Nếu không sẽ nhận `403`.

### Yêu cầu

```http
GET /base/device/status?limit={limit}&offset={offset}&serial={serial}&status={status}&appVersion={appVersion}&firmwareVersion={firmwareVersion}
Authorization: Bearer <token>
```

| Tên | Kiểu | Bắt buộc | Ý nghĩa |
| --- | --- | --- | --- |
| `serial` | string | Không | Một phần của serial, không phân biệt hoa thường, ví dụ `SN00`. |
| `status` | string | Không | Giá trị chính xác, viết hoa: `OFFLINE`, `ONLINE`, `BUSY`, `ERROR` hoặc `MAINTENANCE`. Giá trị lạ không khớp dòng nào. |
| `appVersion` | string | Không | Phiên bản app chính xác, ví dụ `1.4.2`. |
| `firmwareVersion` | string | Không | Phiên bản firmware chính xác, ví dụ `A920-2.1.0`. |
| `limit` | integer | Không | Số dòng mỗi trang, ví dụ `20`. Mặc định `50`. Giá trị `<= 0` hoặc `> 500` thành `50`. |
| `offset` | integer | Không | Số dòng bỏ qua. Mặc định `0`. |

Kết quả sắp xếp theo cập nhật gần nhất trước.

### Ví dụ

```bash
curl "$BASE_URL/base/device/status?status=ONLINE&serial=SN00&limit=20" \
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
    ],
    "total": 1
  }
}
```

| Field | Ý nghĩa |
| --- | --- |
| `items` | Các bản ghi trạng thái trong trang này. Mỗi bản ghi có các field giống *Get Device Status*. |
| `total` | Tổng số bản ghi khớp filter (không phụ thuộc `limit` và `offset`). |

Các field tùy chọn rỗng (`batteryPercent`, `networkType`, `configId`, `config`, `metadata`, ...) sẽ bị bỏ.

### Lỗi

Lỗi dùng định dạng chuẩn được mô tả trong API guide (xem "Response" → "Errors").

| HTTP | `error.code` | Ý nghĩa | Bạn nên làm gì |
| --- | --- | --- | --- |
| `401` | `-32001` | Thiếu hoặc sai token. | Lấy token mới. |
| `403` | `-32003` | Bạn không đọc được `edc.device.status`, `edc.device` hoặc `edc.device.config`. | Nhờ admin cấp quyền. |
| `500` | `-32603` | Lỗi server không mong đợi. | Thử lại sau và báo `id` của response. |

### Cần biết

- `configId` và `config` không có khi thiết bị dùng manual config hoặc chưa có config.
- `deviceConfig` là bản đã lưu. `owner`, `state` và `mqttConfig.mqttDeviceTopic` chỉ được thêm khi terminal đọc config của nó (xem *Get Resolved Device Config*).
- Để điền các filter phiên bản, gọi `GET /base/device/status/versions` (xem *List Device Software Versions*).
