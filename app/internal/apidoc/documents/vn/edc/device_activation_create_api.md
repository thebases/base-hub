## Create Device Activation

Đặt mã kích hoạt dùng một lần để máy tự provision (máy đổi mã này lấy device secret). Dùng ngay sau khi tạo thiết bị. Đây là bước 2 của onboarding; xem **Create Device** (`POST /base/devices`) để biết toàn bộ quy trình.

### Trước khi bắt đầu

- Cần bearer token của user có quyền ghi `edc.device.activation`; nếu không sẽ nhận `403`.
- Tạo thiết bị trước (`POST /base/devices`).

### Request

```http
POST /base/device/activation
Authorization: Bearer <token>
Content-Type: application/json
```

| Tên | Kiểu | Bắt buộc | Ý nghĩa |
| --- | --- | --- | --- |
| `serial` | string | Có | Serial của máy, ví dụ `SN0001`. Tự động viết hoa. |
| `activationCode` | string | Có | **Đúng 8 chữ số**, ví dụ `48213907`. Định dạng khác vẫn được nhận nhưng không bao giờ dùng để provision được. |
| `expiresAt` | string | Không | Thời điểm mã hết hạn, dạng RFC 3339 (`2026-09-27T08:00:00Z`) hoặc Unix seconds. Nếu thiếu hoặc không đọc được, mặc định là bây giờ + 60 phút (cài đặt `edc.provisioning.activation_code_ttl_minutes`). Hãy ghi kèm múi giờ. |

### Ví dụ

```bash
curl -X POST "$BASE_URL/base/device/activation" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"serial":"SN0001","activationCode":"48213907","expiresAt":"2026-09-27T08:00:00Z"}'
```

### Response

`201 Created`:

```json
{
  "id": "111020261535481234570001",
  "result": {
    "id": 7,
    "serial": "SN0001",
    "expiresAt": "2026-09-27T08:00:00Z",
    "createdAt": "2026-09-26T08:05:00Z",
    "updatedAt": "2026-09-26T08:05:00Z"
  }
}
```

| Trường | Ý nghĩa |
| --- | --- |
| `id` (ngoài cùng) | ID của request, để truy vết. |
| `result.id` | ID activation. Dùng cho `GET`, `PUT`, `DELETE /base/device/activation/{id}`. |
| `result.serial` | Serial đã viết hoa. |
| `result.expiresAt` | Thời điểm mã hết hạn. |

Bản thân mã không bao giờ được trả lại. Hãy giữ mã cho đến khi đưa cho máy.

### Lỗi

Lỗi dùng định dạng chuẩn được mô tả trong hướng dẫn API (xem "Response" → "Errors"). Mã chuỗi cũ nằm trong `error.data.reason`.

| HTTP | `error.code` | Ý nghĩa | Bạn nên làm gì |
| --- | --- | --- | --- |
| `400` | `-32700` / `-32000` | JSON sai, hoặc `serial` / `activationCode` trống, hoặc không lưu được bản ghi. | Sửa body. |
| `401` | `-32001` | Thiếu token hoặc token không hợp lệ. | Lấy token mới. |
| `403` | `-32003` | User không được ghi model này. | Nhờ admin cấp quyền. |
| `500` | `-32603` | Server không có provisioning key (`provisioning_secret_not_configured`). | Liên hệ đội backend. |

### Cần biết

- **Mỗi serial một dòng.** Gọi lại cho cùng serial sẽ thay mã cũ và xóa `usedAt`.
- **Nó re-arm thiết bị.** Provisioning status quay về `PENDING`, secret, secret version và MQTT client ID bị xóa. Máy đang `ACTIVE` bị ngắt cho đến khi provision lại. `active` không đổi, nên thiết bị đã deactivate vẫn bị chặn.
- Serial chưa có thiết bị vẫn được tạo dòng activation, nhưng provision sau đó sẽ lỗi `device_not_registered`.
- Cũng có thể gọi: `POST /base/edc/device/activation/issue` với `{"kwargs":{"serial","code","expires_at"}}`. Nếu thiếu `code`, server tự tạo mã 8 chữ số và trả về một lần dưới tên `activation_code`.
