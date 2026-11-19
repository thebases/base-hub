## Update Device Config

"Sửa" một config template bằng cách lưu một **version mới** dựa trên version bạn chỉ định. Version trong `{id}` không bị đổi, nên bạn luôn giữ được lịch sử. Config template là một bộ cài đặt có tên cho máy POS (xem *Create Device Config*).

### Tóm tắt

1. Tải version hiện tại bằng *Get Device Config*.
2. Sửa những gì cần, rồi gửi lại **toàn bộ** `configJson` và **toàn bộ** `bankAppInfoIds`.
3. Server lưu một version mới (`id` mới, `version` + 1).
4. Với `isActive: true` thiết bị chuyển sang ngay. Nếu không, hãy kích hoạt sau bằng *Activate Device Config*.

### Trước khi bắt đầu

Bạn cần bearer token (token đăng nhập, gửi trong header `Authorization`) của user có quyền sửa config template. Không có quyền sẽ nhận `403`.

### Request

```http
PUT /base/device/config/{id}
Authorization: Bearer <token>
Content-Type: application/json
```

| Tên | Kiểu | Bắt buộc | Ý nghĩa |
| --- | --- | --- | --- |
| `id` | int (path) | Có | Version bạn đang sửa, ví dụ `13`. Phải tồn tại. |
| `name` | string | Không | Để trống để giữ tên của `{id}`. Tên khác sẽ bắt đầu (hoặc nối tiếp) lịch sử version của template kia. |
| `configJson` | object | Không | Object cài đặt **đầy đủ**. Không được gộp với bản cũ: khóa bạn bỏ sẽ quay về mặc định. Nếu bỏ hẳn, version mới sao chép `configJson` từ `{id}`. |
| `isActive` | bool | Không (mặc định `false`) | `true` làm version mới thành version active ngay. |
| `bankAppInfoIds` | int[] | Không | Danh sách **đầy đủ** ID cấu hình bank app (TID/MID) cần liên kết, ví dụ `[3]`. Không được sao chép từ version cũ: nếu bỏ, version mới không có liên kết nào. |
| `checksum` | string | Không | Văn bản tự do, lưu nguyên. |
| `createdBy` | string | Không | Người sửa, ví dụ `operator@thebase.vn`. |

Giá trị sai kiểu bị bỏ qua (`configJson` không phải object, `isActive` không phải boolean, `bankAppInfoIds` không phải mảng). `base_id` hoặc `id` trong body bị bỏ qua: `{id}` trên path luôn là version gốc.

### Ví dụ

```bash
curl -X PUT "$BASE_URL/base/device/config/13" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "isActive": true,
    "createdBy": "operator@thebase.vn",
    "bankAppInfoIds": [3],
    "configJson": {
      "owner": "THEBASE",
      "baseId": "B001",
      "pingTime": 30,
      "functions": { "qrFunc": true, "cardFunc": true, "voidFunc": true },
      "mqttConfig": { "mqttDomain": "mqtt.example.com", "mqttPort": "8883", "mqttUsername": "edc" }
    }
  }'
```

### Response

HTTP `200`. Version **mới**, nằm trong envelope `result` chuẩn. Chú ý `id` và `version` mới.

```json
{
  "id": "111020261535481234570001",
  "result": {
    "id": 14,
    "name": "default-retail",
    "version": 2,
    "configJson": {
      "owner": "THEBASE",
      "baseId": "B001",
      "pingTime": 30,
      "functions": { "qrFunc": true, "cardFunc": true, "voidFunc": true, "mqttEnabled": true },
      "mqttConfig": { "mqttDomain": "mqtt.example.com", "mqttPort": "8883", "mqttUsername": "edc", "mqttTls": true }
    },
    "funcQr": true,
    "funcPrefixQr": false,
    "funcCard": true,
    "funcInst": false,
    "funcVoid": true,
    "funcApiPay": false,
    "funcBilling": false,
    "funcPromotion": false,
    "funcMqttEnabled": true,
    "mqttDomain": "mqtt.example.com",
    "mqttPort": "8883",
    "mqttUsername": "edc",
    "mqttTls": true,
    "owner": "THEBASE",
    "baseId": "B001",
    "pingTime": 30,
    "bypassSettleWhenUpdate": false,
    "createdBy": "operator@thebase.vn",
    "isActive": true,
    "createdAt": "2026-09-26T09:00:00Z"
  }
}
```

`configJson` ở trên đã được rút gọn; bản thật luôn chứa đầy đủ mọi khóa đã biết. Ý nghĩa mọi trường xem ở *Create Device Config*.

### Lỗi

Lỗi dùng định dạng chuẩn được mô tả trong API guide (xem "Response" → "Errors").

| HTTP | `error.code` | Ý nghĩa | Bạn nên làm gì |
| --- | --- | --- | --- |
| `400` | `-32602` | `id` không phải số nguyên. | Gửi số trên path. |
| `400` | `-32700` | Body không phải JSON hợp lệ. | Sửa lại JSON. |
| `400` | `-32000` / `-32602` | Tên kết quả bị rỗng, một phần tử `bankAppInfoIds` không tồn tại, hoặc lưu thất bại. | Đọc `message`; sửa tên hoặc các ID. |
| `401` | `-32001` | Thiếu token hoặc token không hợp lệ. | Đăng nhập lại rồi gọi lại. |
| `403` | `-32003` | User không có quyền sửa config template. | Nhờ admin cấp quyền. |
| `404` | `-32004` | Không có config nào mang `id` này. | Kiểm tra lại ID. |

### Cần biết

- Bỏ `bankAppInfoIds` sẽ làm mất các liên kết bank app. Bỏ `configJson` sẽ dùng lại bản cũ.
- Với `isActive: false` (mặc định) version mới được lưu, nhưng thiết bị vẫn dùng version cũ.
- Với `isActive: true`, các version khác cùng tên thành inactive, và thiết bị đang ở version active trước đó chuyển ngay sang version mới.
- Sau lời gọi, hãy dùng `id` được trả về cho các lần sửa tiếp, không dùng `id` cũ.
- Cũng có route cũ `POST /base/edc/device/config/{id}/update-version` với cùng `kwargs` như *Create Device Config* (khóa `id` bị bỏ qua). Route này trả bản ghi mới với tên snake_case, mọi lỗi là `400` với thông báo dạng văn bản thuần, và user không có quyền ghi nhận `403`.
