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

---

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

---

## Call as RPC method: `edc.device.status.search_read`

Dùng method tìm kiếm chuẩn từ code phía server. Nó trả các dòng trạng thái thuần, không có `device` và `config` liên kết như các route REST gắn thêm.

```http
POST /rpc/edc.device.status/search_read
Authorization: Bearer <token>
Content-Type: application/json
```

Không có path id. Gửi `args` là `[]` và đặt các tùy chọn trong `kwargs`.

| Key trong `kwargs` | Kiểu | Bắt buộc | Ý nghĩa |
| --- | --- | --- | --- |
| `domain` | array | Không | Filter dạng danh sách `[field, operator, value]`, ví dụ `[["status", "=", "ONLINE"], ["serial", "ilike", "SN00"]]`. API guide còn mô tả object `filter`. |
| `fields` | array of strings | Không | Tên field cần trả về, ví dụ `["serial", "status", "last_seen_at"]`. |
| `limit` | integer | Không | Số dòng tối đa, ví dụ `20`. |
| `offset` | integer | Không | Số dòng bỏ qua. |
| `order` | string | Không | Sắp xếp, ví dụ `updated_at desc`. |

```bash
curl -X POST "$BASE_URL/rpc/edc.device.status/search_read" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"args": [], "kwargs": {"domain": [["status", "=", "ONLINE"]], "limit": 20, "offset": 0, "order": "updated_at desc"}}'
```

`result` là danh sách bản ghi với tên field snake_case (không phải `{items, total}`). Dùng `search_count` với cùng `domain` để lấy tổng. Nếu bị từ chối đọc thì trả `200` với `[]`.
