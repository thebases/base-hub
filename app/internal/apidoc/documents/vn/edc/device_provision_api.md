## Provision Device

Máy gọi lệnh này để đổi serial và mã kích hoạt dùng một lần lấy device secret lâu dài. Đây là bước 3 của onboarding; xem **Create Device** (`POST /base/devices`) để biết quy trình tạo → mã kích hoạt → provision.

### Trước khi bắt đầu

- Operator phải tạo thiết bị và đặt mã kích hoạt trước (`POST /base/devices`, rồi `POST /base/device/activation`).
- Không cần token. Mã kích hoạt chính là thông tin xác thực.

### Request

```http
POST /device/provision
Content-Type: application/json
```

Đường dẫn **không** nằm dưới `/base`. CORS mở cho mọi origin.

| Tên | Kiểu | Bắt buộc | Ý nghĩa |
| --- | --- | --- | --- |
| `serialNo` | string | Có | Serial của máy, tối đa 128 ký tự, ví dụ `SN0001`. Được cắt khoảng trắng, viết hoa, không phân biệt hoa thường. |
| `activationCode` | string | Có | Đúng 8 chữ số, ví dụ `48213907`. Do operator đặt. |

Mã QR cũ có thể gửi `serial_no` / `activation_code`; vẫn được chấp nhận (key camelCase thắng nếu gửi cả hai).

### Ví dụ

```bash
curl -X POST "$BASE_URL/device/provision" \
  -H "Content-Type: application/json" \
  -d '{"serialNo":"SN0001","activationCode":"48213907"}'
```

### Response

`200 OK`. Đây là định dạng cố định của thiết bị, nên không có envelope `result`:

```json
{
  "deviceId": "SN0001",
  "deviceSecret": "9f1c2e7a4b...64 ký tự hex...",
  "secretVersion": 1
}
```

| Trường | Ý nghĩa |
| --- | --- |
| `deviceId` | Serial đã viết hoa. Cũng là MQTT client ID. |
| `deviceSecret` | Secret lâu dài (64 ký tự hex). **Chỉ hiện một lần.** |
| `secretVersion` | Luôn là `1` sau khi provision. |

> **Warning:** Hãy lưu `deviceSecret` an toàn ngay lập tức. Không thể lấy lại.

Sau đó, gọi các route thiết bị với `Authorization: Bearer <deviceId>:<secretVersion>:<deviceSecret>` và kết nối MQTT với client ID `<deviceId>`.

### Lỗi

Route thiết bị này giữ body lỗi riêng: `{"error":"<tên>","message":"..."}`. Lỗi validation trả về `{"errors":[...]}`.

| HTTP | `error` | Ý nghĩa | Bạn nên làm gì |
| --- | --- | --- | --- |
| `400` | `invalid_json` | Body không phải JSON hợp lệ. | Sửa request. |
| `400` | `errors: [...]` | `serialNo` trống hoặc dài quá 128 ký tự, hoặc `activationCode` không đúng 8 chữ số. | Sửa các trường. |
| `401` | `activation_invalid` | Sai mã, hoặc serial này chưa có mã. | Kiểm tra mã, hoặc xin mã mới (**Create Device Activation**). |
| `404` | `device_not_registered` | Không có thiết bị với serial này. | Nhờ operator tạo (`POST /base/devices`). |
| `409` | `activation_already_used` | Mã đã được dùng. | Nhờ operator đặt mã mới. |
| `410` | `activation_expired` | Mã đã hết hạn. | Nhờ operator đặt mã mới. |
| `423` | `device_not_eligible` | Thiết bị inactive, bị revoke, không ở trạng thái `PENDING`, hoặc đã có secret. | Bật lại và/hoặc đặt mã mới. |
| `500` | `secret_persistence_failed` | Server không có provisioning key (`app.encryptKey`). | Liên hệ đội backend. |
| `500` | `internal_error` | Lỗi server khác. Không có gì thay đổi. | Thử lại sau. |
| `503` | `provisioning_disabled` | Provisioning đang tắt trên server (`app.deviceProvisioningEnable` là `false`). | Liên hệ đội backend. |

### Cần biết

- Thử lại sau lỗi là an toàn; khi lỗi không có gì thay đổi.
- Nếu lệnh đã thành công nhưng phản hồi bị mất, thử lại sẽ trả `409` và không thể khôi phục secret. Nhờ operator đặt mã mới, rồi provision lại.
- Sai mã không bao giờ lộ việc thiết bị có đủ điều kiện hay không: mã được kiểm tra trước khi kiểm tra thiết bị.
- Khi thành công, thiết bị thành `ACTIVE` với `secretVersion = 1`, MQTT client ID được đặt bằng serial, và mã được đánh dấu đã dùng. Một bản ghi audit được ghi (không chứa secret).
