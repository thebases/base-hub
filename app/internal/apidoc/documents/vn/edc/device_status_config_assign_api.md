## Assign Device Config

Chọn config mà terminal sẽ dùng. Hoặc trỏ nó tới một phiên bản config template đang active (`configId`), hoặc cho nó một manual config riêng (`deviceConfig`). Chỉ gửi đúng một trong hai.

### Trước khi bắt đầu

- Bạn cần bearer token (xem API guide) của user có quyền ghi `edc.device.status`.
- Với template, bạn cần id của phiên bản **active** của template đó.
- Cuối lệnh gọi, server đọc device và config bằng quyền của bạn, nên bạn cần quyền đọc `edc.device` và `edc.device.config`. Nếu không sẽ nhận `403` dù việc gán đã được lưu.

### Yêu cầu

```http
PUT /base/device/status/{serial}/config
Authorization: Bearer <token>
Content-Type: application/json
```

| Tên | Kiểu | Bắt buộc | Ý nghĩa |
| --- | --- | --- | --- |
| `serial` | string (path) | Có | Serial của thiết bị đã đăng ký, ví dụ `SN0001`. Khớp chính xác, phân biệt hoa thường. `serial` trong body bị bỏ qua. |
| `configId` | integer | Một trong hai | Id của một phiên bản template, ví dụ `14`. Phải là phiên bản active (`isActive: true`). `null` coi như không gửi. Cũng chấp nhận `config_id`. |
| `deviceConfig` | object | Một trong hai | JSON manual config chỉ cho thiết bị này. Lưu đúng như gửi; `{}` được phép. `null` hoặc giá trị không phải object coi như không gửi. Cũng chấp nhận `device_config`. |

### Ví dụ

Gán template:

```bash
curl -X PUT "$BASE_URL/base/device/status/SN0001/config" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"configId": 14}'
```

Đặt manual config:

```bash
curl -X PUT "$BASE_URL/base/device/status/SN0001/config" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"deviceConfig": {"owner": "THEBASE", "pingTime": 60, "functions": {"qrFunc": true, "mqttEnabled": true}}}'
```

### Phản hồi

`200 OK`. Body là bản ghi trạng thái đã cập nhật của thiết bị (cùng dạng với *Get Device Status*), **không** bọc trong `result`.

```json
{
  "id": 7,
  "serial": "SN0001",
  "deviceId": 42,
  "device": {
    "id": 42,
    "serial": "SN0001",
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
  "charging": false,
  "lastSeenAt": "2026-09-26T09:14:55Z",
  "createdAt": "2026-09-20T03:00:00Z",
  "updatedAt": "2026-09-26T09:20:00Z"
}
```

Với manual config, `configId` và `config` không có và `deviceConfig` là object của bạn.

### Lỗi

Lỗi dùng định dạng chuẩn được mô tả trong API guide (xem "Response" → "Errors").

| HTTP | `error.code` | Ý nghĩa | Bạn nên làm gì |
| --- | --- | --- | --- |
| `400` | `-32700` | Body không phải JSON hợp lệ. | Sửa JSON. |
| `400` | `-32602` | Gửi cả `configId` và `deviceConfig`, hoặc không gửi cái nào, hoặc `configId` không tồn tại hay không phải phiên bản active. | Chỉ gửi một cái và dùng phiên bản active. |
| `401` | `-32001` | Thiếu hoặc sai token. | Lấy token mới. |
| `403` | `-32003` | Bạn thiếu quyền ghi `edc.device.status` (hoặc quyền đọc device hay config). | Nhờ admin cấp quyền. |
| `404` | `-32004` | Không có thiết bị nào đăng ký với serial này. | Kiểm tra serial. |
| `500` | `-32603` | Lỗi server hoặc database không mong đợi. | Thử lại sau và báo `id` của response. |

### Cần biết

- **Template:** thiết bị theo template đó. Config được sao chép, và `cardConfig.tidmid` được dựng lại từ các bank app liên kết của template (nếu có). Khi một phiên bản mới hơn của cùng tên template được kích hoạt sau này, thiết bị tự chuyển sang phiên bản đó.
- **Manual config:** thiết bị không còn theo template nào. Hãy gửi object đầy đủ vì không có giá trị mặc định nào được thêm.
- Lệnh này chỉ lưu việc gán. Nó **không** gửi lệnh tới terminal. `owner`, `state` và `mqttConfig.mqttDeviceTopic` được thêm khi config được đọc, nên đừng gửi chúng.
- Hoạt động với thiết bị đã đăng ký nhưng chưa từng báo trạng thái. Bản ghi trạng thái được tạo với `OFFLINE`.
- Cũng có thể gọi bằng `POST /base/edc/device/status/assign-config` và `POST /rpc/edc.device.status/assign_config` (`kwargs`: `serial`, `configId` hoặc `config_id`, `deviceConfig` hoặc `device_config`). Chúng trả `{"result": <status row>}`.
