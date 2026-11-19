## List Device Configs

Trả về một trang config template, gồm mọi version của mọi template. Config template là một bộ cài đặt có tên cho máy POS (xem *Create Device Config*). Dùng để đổ dữ liệu cho bảng admin hoặc dropdown "chọn template".

### Trước khi bắt đầu

Bạn cần bearer token (token đăng nhập, gửi trong header `Authorization`) của user có quyền đọc config template. Không có quyền sẽ nhận `403`.

### Request

```http
GET /base/device/config?limit={limit}&offset={offset}&name={name}&is_active={is_active}
Authorization: Bearer <token>
```

| Tên | Kiểu | Bắt buộc | Ý nghĩa |
| --- | --- | --- | --- |
| `limit` | int | Không | Số dòng mỗi trang, ví dụ `20`. Mặc định `50`. Giá trị `<= 0` hoặc lớn hơn `500` sẽ thành `50`. |
| `offset` | int | Không | Số dòng bỏ qua, ví dụ `40`. Mặc định `0`. |
| `name` | string | Không | Tìm tên template chứa chuỗi này, không phân biệt hoa thường, ví dụ `retail`. |
| `is_active` | bool | Không | `true` hoặc `false` (cũng nhận `1`/`0`). Giá trị không đọc được sẽ bị bỏ qua (không lọc). |

Kết quả sắp xếp theo `name` từ A đến Z, rồi `version` mới nhất trước.

### Ví dụ

```bash
curl "$BASE_URL/base/device/config?name=retail&is_active=true&limit=20" \
  -H "Authorization: Bearer $TOKEN"
```

### Response

HTTP `200`.

```json
{
  "id": "111020261535481234570001",
  "result": {
    "items": [
      {
        "id": 12,
        "name": "default-retail",
        "version": 3,
        "configJson": {
          "owner": "THEBASE",
          "baseId": "B001",
          "pingTime": 60,
          "appConfig": { "appPath": "https://cdn.example.com/edc/app-1.4.2.apk", "versionCode": 142, "forceInstall": false },
          "functions": { "qrFunc": true, "cardFunc": true, "voidFunc": true, "mqttEnabled": true }
        },
        "appPath": "https://cdn.example.com/edc/app-1.4.2.apk",
        "appVersionCode": 142,
        "appForceInstall": false,
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
        "mqttTls": true,
        "merchantName": "ABC Coffee",
        "owner": "THEBASE",
        "baseId": "B001",
        "pingTime": 60,
        "bypassSettleWhenUpdate": false,
        "createdBy": "admin@thebase.vn",
        "isActive": true,
        "createdAt": "2026-09-26T08:00:00Z"
      }
    ],
    "total": 1
  }
}
```

| Trường | Ý nghĩa |
| --- | --- |
| `items` | Các version template trong trang này. |
| `total` | Tổng số version khớp bộ lọc trên mọi trang, không chỉ trang này. |

`configJson` ở trên đã được rút gọn; bản thật luôn chứa đầy đủ mọi khóa đã biết. Các trường phẳng như `appPath` hay `qrBankAccount` bị bỏ khi rỗng. Ý nghĩa mọi trường xem ở *Create Device Config*.

### Lỗi

Lỗi dùng định dạng chuẩn được mô tả trong API guide (xem "Response" → "Errors").

| HTTP | `error.code` | Ý nghĩa | Bạn nên làm gì |
| --- | --- | --- | --- |
| `401` | `-32001` | Thiếu token hoặc token không hợp lệ. | Đăng nhập lại rồi gọi lại. |
| `403` | `-32003` | User không có quyền đọc config template. | Nhờ admin cấp quyền. |
| `500` | `-32603` | Lỗi server hoặc database không mong đợi. | Thử lại sau; báo kèm `id` của response. |

### Cần biết

- Mỗi lần sửa tạo ra một version mới, nên một tên template có thể xuất hiện nhiều lần. Dùng `is_active=true` để chỉ xem version mà thiết bị đang theo.
- Các item không có `deviceStatuses` hay `tidmid` (bank app đã liên kết). Hãy gọi *Get Device Config* để lấy chúng.
- `limit` hoặc `offset` không phải số nguyên được coi là `0` (khi đó `limit` thành `50`).
