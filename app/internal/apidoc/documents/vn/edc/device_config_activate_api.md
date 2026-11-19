## Activate Device Config

Biến một version thành version đang hoạt động (active) của config template. Config template là một bộ cài đặt có tên cho máy POS, được lưu theo từng version (xem *Create Device Config*). Dùng khi muốn triển khai bản sửa đã lưu, hoặc quay về version cũ. Các thiết bị đang theo template sẽ chuyển sang version bạn kích hoạt.

### Trước khi bắt đầu

Bạn cần bearer token (token đăng nhập, gửi trong header `Authorization`) của user có quyền sửa config template. Không có quyền sẽ nhận `403`.

### Request

```http
POST /base/device/config/{id}/activate
Authorization: Bearer <token>
```

Không có body.

| Tên | Kiểu | Bắt buộc | Ý nghĩa |
| --- | --- | --- | --- |
| `id` | int (path) | Có | ID của config version cần kích hoạt, ví dụ `14`. |

### Ví dụ

```bash
curl -X POST "$BASE_URL/base/device/config/14/activate" \
  -H "Authorization: Bearer $TOKEN"
```

### Response

HTTP `200`. Version vừa kích hoạt, nằm trong envelope `result` chuẩn.

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
      "functions": { "qrFunc": true, "cardFunc": true, "mqttEnabled": true }
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
    "mqttTls": true,
    "owner": "THEBASE",
    "baseId": "B001",
    "pingTime": 30,
    "bypassSettleWhenUpdate": false,
    "isActive": true,
    "createdAt": "2026-09-26T09:00:00Z"
  }
}
```

`configJson` ở trên đã được rút gọn; bản thật luôn chứa đầy đủ mọi khóa đã biết. Ý nghĩa các trường xem ở *Create Device Config*.

### Lỗi

Lỗi dùng định dạng chuẩn được mô tả trong API guide (xem "Response" → "Errors").

| HTTP | `error.code` | Ý nghĩa | Bạn nên làm gì |
| --- | --- | --- | --- |
| `400` | `-32602` | `id` không phải số nguyên. | Gửi số trên path. |
| `401` | `-32001` | Thiếu token hoặc token không hợp lệ. | Đăng nhập lại rồi gọi lại. |
| `403` | `-32003` | User không có quyền sửa config template. | Nhờ admin cấp quyền. |
| `404` | `-32004` | Không có config nào mang ID này. | Liệt kê config và chọn ID hợp lệ. |
| `500` | `-32603` | Lỗi server hoặc database không mong đợi. | Thử lại sau; báo kèm `id` của response. |

### Cần biết

- Chỉ các version có **cùng tên** bị tắt. Template khác giữ version active của chúng.
- Các thiết bị đang dùng version active trước đó được chuyển sang version này. Cấu hình đã lưu của chúng bị thay bằng `configJson` của version này (và `cardConfig.tidmid` được dựng lại từ các bank app đã liên kết, nếu có). Thiết bị ở version khác, hoặc có cấu hình thủ công, không bị đổi. Nếu trước đó không có version nào của tên này active thì không thiết bị nào bị chuyển.
- Kích hoạt version đã active không gây hại gì.
- Cũng có route cũ `POST /base/edc/device/config/{id}/activate` với `kwargs` rỗng. Route này trả bản ghi với tên snake_case, mọi lỗi là `400` với thông báo dạng văn bản thuần, và user không có quyền ghi nhận `403`.
