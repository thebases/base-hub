## Get Device Config

Trả về một version của config template (bộ cài đặt có tên cho máy POS, xem *Create Device Config*) theo ID số. Kết quả kèm danh sách thiết bị đang được gán version đó và các bank app đã liên kết. Dùng cho trang chi tiết template, hoặc để tải một version trước khi sửa.

### Trước khi bắt đầu

Bạn cần bearer token (token đăng nhập, gửi trong header `Authorization`) của user có quyền đọc config template. Lời gọi cũng đọc trạng thái thiết bị và cấu hình bank app (TID/MID) bằng quyền của bạn, nên bạn cần quyền đọc cả hai, nếu không sẽ nhận `403`.

### Request

```http
GET /base/device/config/{id}
Authorization: Bearer <token>
```

| Tên | Kiểu | Bắt buộc | Ý nghĩa |
| --- | --- | --- | --- |
| `id` | int (path) | Có | ID của config version, ví dụ `12`. |

### Ví dụ

```bash
curl "$BASE_URL/base/device/config/12" \
  -H "Authorization: Bearer $TOKEN"
```

### Response

HTTP `200`. Version nằm trong envelope `result` chuẩn.

```json
{
  "id": "111020261535481234570001",
  "result": {
    "id": 12,
    "name": "default-retail",
    "version": 3,
    "configJson": {
      "owner": "THEBASE",
      "baseId": "B001",
      "pingTime": 60,
      "functions": { "qrFunc": true, "cardFunc": true, "mqttEnabled": true },
      "mqttConfig": { "mqttDomain": "mqtt.example.com", "mqttPort": "8883", "mqttTls": true }
    },
    "funcQr": true,
    "funcPrefixQr": false,
    "funcCard": true,
    "funcInst": false,
    "funcVoid": false,
    "funcApiPay": false,
    "funcBilling": false,
    "funcPromotion": false,
    "funcMqttEnabled": true,
    "mqttDomain": "mqtt.example.com",
    "mqttPort": "8883",
    "mqttTls": true,
    "owner": "THEBASE",
    "baseId": "B001",
    "pingTime": 60,
    "bypassSettleWhenUpdate": false,
    "isActive": true,
    "createdAt": "2026-09-26T08:00:00Z",
    "deviceStatuses": [
      {
        "id": 7,
        "serial": "SN0001",
        "deviceId": 42,
        "configId": 12,
        "deviceConfig": { "owner": "THEBASE", "baseId": "B001", "pingTime": 60 },
        "status": "ONLINE",
        "charging": false,
        "createdAt": "2026-09-20T03:00:00Z",
        "updatedAt": "2026-09-26T08:05:00Z"
      }
    ],
    "tidmid": [
      {
        "id": 3,
        "appBankInfo": "BIDV",
        "bincode": "970418",
        "defaultApp": true,
        "environment": "prod",
        "packageName": "com.wdvn.bidv",
        "tid": "12345678",
        "mid": "000000000123456",
        "createdAt": "2026-09-01T00:00:00Z",
        "updatedAt": "2026-09-01T00:00:00Z"
      }
    ]
  }
}
```

`configJson` ở trên đã được rút gọn; bản thật luôn chứa đầy đủ mọi khóa đã biết. Các trường giống *Create Device Config*, cộng thêm hai danh sách:

| Trường | Ý nghĩa |
| --- | --- |
| `deviceStatuses` | Các thiết bị đang được gán version này (các dòng trạng thái đơn giản). Bị bỏ khi rỗng. Đây là các thiết bị chặn việc xóa. |
| `tidmid` | Các bank app liên kết với version này. `tid` là terminal ID và `mid` là merchant ID do ngân hàng cấp. Bị bỏ khi rỗng. `secret` của bank app không bao giờ được trả về. |

### Lỗi

Lỗi dùng định dạng chuẩn được mô tả trong API guide (xem "Response" → "Errors").

| HTTP | `error.code` | Ý nghĩa | Bạn nên làm gì |
| --- | --- | --- | --- |
| `400` | `-32602` | `id` không phải số nguyên. | Gửi số trên path. |
| `401` | `-32001` | Thiếu token hoặc token không hợp lệ. | Đăng nhập lại rồi gọi lại. |
| `403` | `-32003` | User không có quyền đọc config template (hoặc trạng thái thiết bị / bank app). | Nhờ admin cấp quyền. |
| `404` | `-32004` | Không có config nào mang ID này. | Kiểm tra lại ID. |
| `500` | `-32603` | Lỗi server hoặc database không mong đợi. | Thử lại sau; báo kèm `id` của response. |

### Cần biết

- Các trường phẳng `mqttPassword`, `mqttClientKey` và `mqttClientPem` không bao giờ được trả về. Nhưng `configJson.mqttConfig` vẫn chứa chúng.
- Cũng có route cũ `POST /base/edc/device/config/{id}/detail` với `kwargs` rỗng. Route này trả bản ghi với tên snake_case cộng `bank_app_info_ids` và `assigned_device_count`, không có danh sách `tidmid` / `deviceStatuses`. ID không tồn tại trả `400`, và user không có quyền đọc nhận `{"result": null}` thay vì `403`.
