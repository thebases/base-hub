## Update Device Activation

Cấp mã kích hoạt mới (và thời hạn) cho một bản ghi activation đã có. Dùng khi mã hết hạn hoặc máy cần provision lại.

> **Warning:** Lệnh này reset thiết bị. Máy bị đăng xuất và phải provision lại.

### Trước khi bắt đầu

- Cần bearer token của user có quyền ghi `edc.device.activation`; nếu không sẽ nhận `403`.
- Lấy `id` của activation từ phản hồi create hoặc list.
- Quy trình onboarding đầy đủ ở **Create Device** (`POST /base/devices`).

### Request

```http
PUT /base/device/activation/{id}
Authorization: Bearer <token>
Content-Type: application/json
```

| Tên | Kiểu | Bắt buộc | Ý nghĩa |
| --- | --- | --- | --- |
| `id` (path) | int | Có | ID activation, ví dụ `7`. |
| `activationCode` | string | Không, nhưng luôn nên gửi | Mã mới, đúng 8 chữ số, ví dụ `48213907`. Nếu thiếu hoặc là `""`, server tạo mã ngẫu nhiên và **không trả về ở đâu cả**. |
| `expiresAt` | string | Không | Hạn mới, ví dụ `2026-09-27T08:00:00Z` (RFC 3339 hoặc Unix seconds). Nếu thiếu hoặc không đọc được, mặc định là bây giờ + 60 phút (`edc.provisioning.activation_code_ttl_minutes`). Hãy ghi kèm múi giờ. |

### Ví dụ

Đặt mã mới có hiệu lực đến thời điểm đã chọn:

```bash
curl -X PUT "$BASE_URL/base/device/activation/7" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"activationCode":"48213907","expiresAt":"2026-09-27T08:00:00Z"}'
```

### Response

`200`:

```json
{
  "id": "111020261535481234570001",
  "result": {
    "id": 7,
    "serial": "SN0001",
    "expiresAt": "2026-09-27T08:00:00Z",
    "createdAt": "2026-09-26T08:00:00Z",
    "updatedAt": "2026-09-26T10:00:00Z"
  }
}
```

`usedAt` bị xóa bởi lệnh này. Mã không được trả lại, nên hãy đưa cho máy đúng mã bạn đã gửi.

### Lỗi

Lỗi dùng định dạng chuẩn được mô tả trong hướng dẫn API (xem "Response" → "Errors"). Mã chuỗi cũ nằm trong `error.data.reason`.

| HTTP | `error.code` | Ý nghĩa | Bạn nên làm gì |
| --- | --- | --- | --- |
| `400` | `-32602` / `-32000` | `id` không phải số, body không phải JSON hợp lệ, hoặc không lưu được bản ghi. | Sửa request. |
| `401` | `-32001` | Thiếu token hoặc token không hợp lệ. | Lấy token mới. |
| `403` | `-32003` | User không được ghi model này. | Nhờ admin cấp quyền. |
| `404` | `-32004` | Không có activation với ID này. | Kiểm tra ID. |
| `500` | `-32603` | Server không có provisioning key (`provisioning_secret_not_configured`). Không có gì thay đổi. | Liên hệ đội backend. |

### Cần biết

- Mỗi lần gọi thành công đều cấp lại mã cho serial của dòng đó. Device secret, secret version và MQTT client ID bị xóa, provisioning status quay về `PENDING`.
- Không có lệnh "chỉ gia hạn". Nếu chỉ gửi `expiresAt`, mã bị thay bằng một mã ngẫu nhiên không ai biết. Luôn gửi `activationCode` cùng với `expiresAt`.
- Tác dụng giống như gọi lại **Create Device Activation** cho cùng serial.
