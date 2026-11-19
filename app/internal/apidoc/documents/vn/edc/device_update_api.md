## Update Device

Đổi IMEI, owner, holder hoặc cờ active của thiết bị. Dùng khi giao thiết bị cho người giữ khác, hoặc bật lại thiết bị đã tắt. Chỉ các khóa bạn gửi mới bị thay đổi. Không đổi được serial.

### Trước khi bắt đầu

Bạn cần bearer token (token đăng nhập, gửi trong header `Authorization`) của user có quyền sửa thiết bị. Không có quyền sẽ nhận `403`.

### Request

```http
PUT /base/devices/{serial}
Authorization: Bearer <token>
Content-Type: application/json
```

| Tên | Kiểu | Bắt buộc | Ý nghĩa |
| --- | --- | --- | --- |
| `serial` (path) | string | Có | Serial của thiết bị cần sửa, ví dụ `SN0001`. |
| `imei` | string | Không | IMEI mới, ví dụ `356938035643809`. Bỏ qua để giữ giá trị cũ; gửi `null` để xóa. |
| `ownerId` | int | Không | ID owner mới, ví dụ `10`. Bỏ qua để giữ giá trị cũ. |
| `holderId` | int | Không | ID holder mới, ví dụ `30`. Bỏ qua để giữ giá trị cũ. |
| `active` | bool | Không | `true` hoặc `false`. Bỏ qua để giữ giá trị hiện tại. |
| `serial` (body) | string | Không | Nếu gửi thì phải bằng serial trên path. Giá trị khác bị từ chối bằng `400` ("serial is immutable"). |

### Ví dụ

```bash
curl -X PUT "$BASE_URL/base/devices/SN0001" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"imei":"356938035643809","ownerId":10,"holderId":30,"active":true}'
```

### Response

HTTP `200`. Thiết bị sau khi lưu (được đọc lại sau khi ghi), nằm trong envelope `result` chuẩn.

```json
{
  "id": "111020261535481234570001",
  "result": {
    "id": 42,
    "serial": "SN0001",
    "imei": "356938035643809",
    "ownerId": 10,
    "holderId": 30,
    "groupId": 1,
    "active": true,
    "provisioningStatus": "ACTIVE",
    "mqttClientId": "SN0001",
    "secretVersion": 1,
    "lastProvisionedAt": "2026-09-26T08:05:00Z",
    "createdAt": "2026-09-26T08:00:00Z",
    "updatedAt": "2026-09-26T09:30:00Z"
  }
}
```

Các trường giống *Get Device*.

### Lỗi

Lỗi dùng định dạng chuẩn được mô tả trong API guide (xem "Response" → "Errors").

| HTTP | `error.code` | Ý nghĩa | Bạn nên làm gì |
| --- | --- | --- | --- |
| `400` | `-32700` | Body không phải JSON hợp lệ. | Sửa lại JSON. |
| `400` | `-32000` / `-32602` | `serial` trong body khác serial đã lưu, hoặc không lưu được thiết bị. | Bỏ `serial` khỏi body hoặc cho khớp; đọc `message`. |
| `401` | `-32001` | Thiếu token hoặc token không hợp lệ. | Đăng nhập lại rồi gọi lại. |
| `403` | `-32003` | User không có quyền sửa thiết bị. | Nhờ admin cấp quyền. |
| `404` | `-32004` | Không có thiết bị nào mang serial này. | Kiểm tra chính tả. |

### Cần biết

- Đây là cập nhật từng phần: khóa bạn không gửi giữ nguyên giá trị đã lưu.
- Để bật lại thiết bị đã bị tắt bằng `DELETE /base/devices/{serial}`, gửi `"active": true`.
- Các khóa khác (`groupId`, `provisioningStatus`, ...) bị bỏ qua. Đổi nhóm bằng `POST /base/groups/{id}/devices`.
- Dữ liệu provisioning (trạng thái, secret, MQTT client ID) không đổi được ở đây.
