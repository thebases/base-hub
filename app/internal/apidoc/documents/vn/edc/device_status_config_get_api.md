## Get Resolved Device Config

Chỉ trả về JSON config cuối cùng mà terminal nhận: bản sao của template được gán, hoặc manual config đặt riêng cho nó. Dùng để trả lời "SN0001 đang có những cài đặt nào?".

### Trước khi bắt đầu

- Bạn cần bearer token (xem API guide).
- Method này không được đánh dấu chỉ-đọc, nên quyền được kiểm tra là quyền **ghi** `edc.device.status`, không phải đọc. User chỉ có quyền đọc sẽ nhận `403`.

### Yêu cầu

```http
GET /base/device/status/{serial}/config
Authorization: Bearer <token>
```

| Tên | Kiểu | Bắt buộc | Ý nghĩa |
| --- | --- | --- | --- |
| `serial` | string (path) | Có | Serial, ví dụ `SN0001`. Khớp chính xác, phân biệt hoa thường. |

### Ví dụ

```bash
curl "$BASE_URL/base/device/status/SN0001/config" \
  -H "Authorization: Bearer $TOKEN"
```

### Phản hồi

`200 OK`. Config nằm trong envelope `result` chuẩn.

```json
{
  "id": "111020261535481234570001",
  "result": {
    "owner": "10",
    "baseId": "B001",
    "pingTime": 30,
    "bypassSettleWhenUpdate": false,
    "state": "ACTIVE",
    "ignoreAppBankErrorCodes": [],
    "appConfig": { "appPath": "https://cdn.example.com/edc/app-1.4.2.apk", "versionCode": 142, "forceInstall": false },
    "functions": { "qrFunc": true, "cardFunc": true, "voidFunc": true, "mqttEnabled": true },
    "qrConfig": { "bankAccount": "0123456789", "bankBincode": "970418", "qrTimeInterval": 180 },
    "mqttConfig": { "mqttDomain": "mqtt.example.com", "mqttPort": "8883", "mqttTls": true, "mqttDeviceTopic": "base/default/SN0001/cmd" },
    "merchantInfo": { "mmName": "ABC Coffee", "storeName": "ABC Coffee Q1" }
  }
}
```

Ví dụ đã rút gọn. Các key là những gì đã gán cho thiết bị, cộng thêm ba key server thêm vào mỗi lần đọc (chúng ghi đè giá trị đã lưu):

| Key | Giá trị |
| --- | --- |
| `owner` | `ownerId` của thiết bị dạng string. |
| `state` | Trạng thái provisioning của thiết bị, ví dụ `ACTIVE`. |
| `mqttConfig.mqttDeviceTopic` | MQTT topic (tên kênh) để gửi lệnh tới thiết bị: `base/<group name>/<serial>/cmd`. Thiết bị không có group dùng tên group mặc định. |

Thiết bị chưa có config đã lưu chỉ trả ba key này.

### Lỗi

Lỗi dùng định dạng chuẩn được mô tả trong API guide (xem "Response" → "Errors").

| HTTP | `error.code` | Ý nghĩa | Bạn nên làm gì |
| --- | --- | --- | --- |
| `401` | `-32001` | Thiếu hoặc sai token. | Lấy token mới. |
| `403` | `-32003` | User không có quyền ghi `edc.device.status`. | Nhờ admin cấp quyền. |
| `404` | `-32004` | Không có bản ghi trạng thái cho serial này. | Kiểm tra chính tả. Ở đây serial phân biệt hoa thường. |
| `500` | `-32603` | Lỗi server không mong đợi. | Thử lại sau và báo `id` của response. |

### Cần biết

- Manual config được trả đúng như đã lưu. Giá trị mặc định không được điền thêm (config từ template thì có).
- `appConfig.versionName` bị bỏ nếu config cũ đã lưu còn có nó.
- Chính terminal đọc cùng JSON này từ `GET /base/devices/config/{serial}` (xem *Get Device Config*). Route đó không phân biệt hoa thường, và body là config thô, không có envelope `result`.
- Cũng có thể gọi bằng `POST /base/edc/device/status/get-config` và `POST /rpc/edc.device.status/get_config` với `{"kwargs": {"serial": "SN0001"}}`.
