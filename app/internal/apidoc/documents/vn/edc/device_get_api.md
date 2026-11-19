## Get Device

Trả về một thiết bị theo serial (số định danh duy nhất in trên máy POS). Dùng cho trang chi tiết thiết bị, hoặc để kiểm tra máy đã provisioning xong chưa (provisioning là bước cài đặt lần đầu để máy nhận khóa bí mật).

### Trước khi bắt đầu

Bạn cần bearer token (token đăng nhập, gửi trong header `Authorization`) của user có quyền đọc thiết bị. Không có quyền sẽ nhận `403`.

### Request

```http
GET /base/devices/{serial}
Authorization: Bearer <token>
```

| Tên | Kiểu | Bắt buộc | Ý nghĩa |
| --- | --- | --- | --- |
| `serial` | string (path) | Có | Serial chính xác, phân biệt hoa thường. Serial thường viết hoa, ví dụ `SN0001`. Khoảng trắng hai đầu bị cắt bỏ. |

### Ví dụ

```bash
curl "$BASE_URL/base/devices/SN0001" \
  -H "Authorization: Bearer $TOKEN"
```

### Response

HTTP `200`. Thiết bị nằm trong envelope `result` chuẩn.

```json
{
  "id": "111020261535481234570001",
  "result": {
    "id": 42,
    "serial": "SN0001",
    "imei": "356938035643809",
    "ownerId": 10,
    "holderId": 25,
    "groupId": 1,
    "active": true,
    "provisioningStatus": "ACTIVE",
    "mqttClientId": "SN0001",
    "secretVersion": 1,
    "lastProvisionedAt": "2026-09-26T08:05:00Z",
    "createdAt": "2026-09-26T08:00:00Z",
    "updatedAt": "2026-09-26T08:05:00Z"
  }
}
```

| Trường | Ý nghĩa |
| --- | --- |
| `id` | ID nội bộ của thiết bị trong database. |
| `serial` | Serial của thiết bị. |
| `imei` | Số IMEI của modem. Bị bỏ khi rỗng. |
| `ownerId` | ID chủ sở hữu thiết bị. |
| `holderId` | ID người đang giữ thiết bị. |
| `groupId` | ID nhóm của thiết bị. Bị bỏ khi thiết bị chưa thuộc nhóm nào. |
| `active` | `true` nếu thiết bị đang bật. Thiết bị đã tắt (`false`) vẫn được trả về. |
| `provisioningStatus` | `PENDING` cho đến khi máy được provisioning, sau đó là `ACTIVE`. Giá trị khác: `SUSPENDED`, `REVOKED`. |
| `mqttClientId` | Tên máy dùng để kết nối message broker. Bị bỏ khi rỗng. |
| `secretVersion` | Bộ đếm, tăng mỗi lần đổi khóa bí mật của thiết bị. |
| `lastProvisionedAt`, `revokedAt` | Lần provisioning gần nhất / thời điểm bị thu hồi. Bị bỏ khi rỗng. |
| `createdAt`, `updatedAt` | Thời điểm tạo / sửa gần nhất. |

Khóa bí mật của thiết bị không bao giờ được trả về.

### Lỗi

Lỗi dùng định dạng chuẩn được mô tả trong API guide (xem "Response" → "Errors").

| HTTP | `error.code` | Ý nghĩa | Bạn nên làm gì |
| --- | --- | --- | --- |
| `401` | `-32001` | Thiếu token hoặc token không hợp lệ. | Đăng nhập lại rồi gọi lại. |
| `403` | `-32003` | User không có quyền đọc thiết bị. | Nhờ admin cấp quyền. |
| `404` | `-32004` | Không có thiết bị nào mang serial này. | Kiểm tra chính tả (serial viết hoa). |
| `500` | `-32603` | Lỗi server không mong đợi. | Thử lại sau; báo kèm `id` của response. |

### Cần biết

- Cũng có route cũ `POST /base/edc/devices/by-serial` với body `{"kwargs":{"serial":"SN0001"}}`. Route này trả tên snake_case (`owner_id`, `provisioning_status`) và với thiết bị không tồn tại trả `400` thay vì `404`. User không có quyền đọc nhận `200` với kết quả rỗng thay vì `403`.
