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
- Chính terminal đọc cùng JSON này từ `GET /base/devices/config/{serial}` (xem *Get Device Config* bên dưới). Route đó không phân biệt hoa thường.

---

## Get Device Config

Trả về JSON config hiện đang gán cho một thiết bị. Terminal gọi route này để lấy cài đặt của chính nó. Công cụ admin có thể dùng để xem chính xác thiết bị nhận gì.

### Trước khi bắt đầu

- Route này do terminal gọi, không phải user session. Cài đặt server `edc.device_http_auth` quyết định cách bảo vệ:
  - Chế độ `token`: bắt buộc có device token.
  - Chế độ `public` (mặc định): có thể bỏ header.
- Device token có dạng `<serial>:<secretVersion>:<deviceSecret>`. Secret được cấp khi terminal tự thiết lập lần đầu (provisioning, xem *Provision Device*).
- Nếu bạn gửi token thì nó luôn được kiểm tra, và serial trong token phải bằng `{serial}`. Một thiết bị không đọc được config của thiết bị khác.

### Yêu cầu

```http
GET /base/devices/config/{serial}
Authorization: Bearer <serial>:<secretVersion>:<deviceSecret>
```

| Tên | Kiểu | Bắt buộc | Ý nghĩa |
| --- | --- | --- | --- |
| `serial` | string (path) | Có | Serial, ví dụ `SN0001`. Khoảng trắng được cắt. Không phân biệt hoa thường. |

### Ví dụ

```bash
curl "$BASE_URL/base/devices/config/SN0001" \
  -H "Authorization: Bearer SN0001:1:$DEVICE_SECRET"
```

### Phản hồi

`200 OK`. Route này là hợp đồng cố định cho firmware terminal, nên body là object config thô **không** có envelope `result`. Nó cùng JSON với *Get Resolved Device Config*.

```json
{
  "owner": "10",
  "baseId": "B-10025",
  "pingTime": 60,
  "state": "ACTIVE",
  "bypassSettleWhenUpdate": false,
  "ignoreAppBankErrorCodes": [],
  "appConfig": { "appPath": "https://cdn.example.com/edc/app-1.4.2.apk", "versionCode": 142, "forceInstall": false },
  "functions": { "qrFunc": true, "cardFunc": true, "voidFunc": true, "mqttEnabled": true },
  "hiddenConfig": { "lockConfig": false },
  "qrConfig": { "bankBincode": "970436", "bankAccount": "0011001234567", "bankAccountHolder": "BASE STORE", "numberOfCopies": 1, "qrTimeInterval": 5 },
  "cardConfig": { "maxTip": 0, "numberOfCopies": 1 },
  "mqttConfig": { "mqttDomain": "mqtt.example.com", "mqttPort": "8883", "mqttTls": true, "mqttDeviceTopic": "base/default/SN0001/cmd" }
}
```

Các key là những gì đã gán cho thiết bị, cộng thêm `owner`, `state` và `mqttConfig.mqttDeviceTopic` do server thêm vào mỗi lần đọc. Thiết bị không có config chỉ trả ba key đó.

### Lỗi

Route này giữ body lỗi riêng: `{"error": "<code>", "message": "..."}`.

| HTTP | `error` | Ý nghĩa | Bạn nên làm gì |
| --- | --- | --- | --- |
| `401` | `missing_device_token` / `invalid_device_token` | Cần token (chế độ `token`) nhưng thiếu, hoặc token sai hay thuộc serial khác. | Gửi đúng device token. |
| `404` | `not_found` | Serial không tồn tại (không có bản ghi trạng thái cho thiết bị này). | Kiểm tra serial. |
| `500` | `internal_error` | Lỗi server không mong đợi. | Thử lại sau. |

---

## Call as RPC method: `edc.device.status.get_config`

Dùng từ code phía server. Nó trả cùng config với *Get Resolved Device Config*.

```http
POST /rpc/edc.device.status/get_config
Authorization: Bearer <token>
Content-Type: application/json
```

Cũng có ở `POST /base/edc/device/status/get-config`. Không có path id. Gửi `args` là `[]` và đặt các giá trị trong `kwargs`.

| Key trong `kwargs` | Kiểu | Bắt buộc | Ý nghĩa |
| --- | --- | --- | --- |
| `serial` | string | Có | Serial, ví dụ `SN0001`. Khớp chính xác. Cũng chấp nhận `serial_number`. |

```bash
curl -X POST "$BASE_URL/rpc/edc.device.status/get_config" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"args": [], "kwargs": {"serial": "SN0001"}}'
```

Response `200`: `{"id": "...", "result": {<object config>}}`. Thiếu `serial` trả `400` / `-32602`. Quyền được kiểm tra là quyền ghi `edc.device.status`.
