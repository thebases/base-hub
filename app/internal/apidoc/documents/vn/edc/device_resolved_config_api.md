## Get Device Config

Trả về JSON cấu hình đang được gán cho một thiết bị. Chính máy POS gọi route này để tải cấu hình của nó. Công cụ admin nên dùng `GET /base/device/status/{serial}/config` (với Base HUB bearer token) để xem thiết bị nhận được gì.

### Trước khi bắt đầu

Route này dành cho máy POS, không dành cho user Base HUB. Nó không có đăng nhập Base HUB và không kiểm tra quyền. Thiết bị chứng minh danh tính bằng device token. Token của user Base HUB không phải device token và sẽ nhận `401`.

Cách kiểm tra thiết bị phụ thuộc vào cài đặt server `edc.device_http_auth`:

- `token`: bắt buộc gửi `Authorization: Bearer <serial>:<secretVersion>:<deviceSecret>`. Serial trong token phải bằng `{serial}` trên path (không phân biệt hoa thường), nên một thiết bị chỉ đọc được cấu hình của chính nó.
- `public` (mặc định): có thể bỏ header. Nếu vẫn gửi header thì nó được kiểm tra như chế độ `token`.

### Request

```http
GET /base/devices/config/{serial}
Authorization: Bearer <serial>:<secretVersion>:<deviceSecret>
```

| Tên | Kiểu | Bắt buộc | Ý nghĩa |
| --- | --- | --- | --- |
| `serial` | string (path) | Có | Serial thiết bị, ví dụ `SN0001`. Khoảng trắng bị cắt; so khớp không phân biệt hoa thường. |

### Ví dụ

```bash
curl "$BASE_URL/base/devices/config/SN0001" \
  -H "Authorization: Bearer SN0001:1:$DEVICE_SECRET"
```

### Response

HTTP `200`. Đây là hợp đồng cố định dành cho thiết bị: object cấu hình được trả nguyên trạng, **không** có envelope `{"id","result"}`.

```json
{
  "owner": "10",
  "baseId": "B-10025",
  "pingTime": 60,
  "state": "ACTIVE",
  "bypassSettleWhenUpdate": false,
  "ignoreAppBankErrorCodes": [],
  "appConfig": { "appPath": "https://cdn.example.com/edc/app-1.4.2.apk", "versionCode": 142, "forceInstall": false },
  "mqttConfig": { "mqttDomain": "mqtt.example.com", "mqttPort": "8883", "mqttTls": true, "mqttDeviceTopic": "base/default/SN0001/cmd" },
  "functions": { "qrFunc": true, "cardFunc": true, "voidFunc": true, "mqttEnabled": true },
  "hiddenConfig": { "lockConfig": false },
  "qrConfig": { "bankBincode": "970436", "bankAccount": "0011001234567", "bankAccountHolder": "BASE STORE", "numberOfCopies": 1, "qrTimeInterval": 5 },
  "cardConfig": { "maxTip": 0, "numberOfCopies": 1 }
}
```

Các khóa là những gì đã được gán cho thiết bị (bản sao của config template, hoặc cấu hình thủ công). Sau đó server đặt thêm ba giá trị khi bạn đọc:

| Khóa | Ý nghĩa |
| --- | --- |
| `owner` | `ownerId` của thiết bị, dạng chuỗi. |
| `state` | `provisioningStatus` của thiết bị (ví dụ `ACTIVE`). |
| `mqttConfig.mqttDeviceTopic` | MQTT topic (tên kênh) mà thiết bị nhận lệnh: `base/<group name>/<serial>/cmd`. Tên nhóm là `default` khi thiết bị chưa thuộc nhóm nào. |

Thiết bị chưa được gán cấu hình chỉ nhận các khóa này (`owner`, `state`, `mqttConfig`). `appConfig.versionName` không còn được gửi.

### Lỗi

Body lỗi của route này là `{"error": "<name>", "message": "..."}`, không phải định dạng lỗi chuẩn.

| HTTP | `error` | Ý nghĩa | Bạn nên làm gì |
| --- | --- | --- | --- |
| `401` | `missing_device_token` | Chế độ token và không có header `Authorization: Bearer ...`. | Gửi device token. |
| `401` | `invalid_device_token` | Token sai định dạng, sai secret hoặc version, thiết bị inactive hoặc chưa provisioning, hoặc serial trong token khác `{serial}`. | Kiểm tra token; provisioning lại thiết bị nếu cần. |
| `404` | `not_found` | Serial không tồn tại (chế độ public không có token), hoặc thiết bị chưa có bản ghi trạng thái. | Kiểm tra serial. |
| `500` | `internal_error` | Lỗi server không mong đợi. | Thử lại sau. |

### Cần biết

- Để đổi cấu hình thiết bị nhận, hãy sửa template (*Create Device Config*, *Update Device Config*) hoặc gán cấu hình khác cho thiết bị.
