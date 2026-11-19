## Purge Expired MQTT Nonces (internal)

Xóa các dòng cũ trong sổ chống phát lại (replay protection). Nonce là một giá trị ngẫu nhiên dùng một lần mà mỗi message bảo mật mang theo; server ghi nhớ nó để cùng một message không thể bị phát lại. Nonce cũ có thể quên đi. Một cron job (`edc.purge_expired_nonces`) đã dọn định kỳ. Chỉ gọi endpoint này khi operator muốn dọn ngay.

### Before you start

Cần bearer token và quyền **write** trên `edc.mqtt.nonce` (method này không phải read-only).

### Request

```http
POST /base/edc/mqtt/nonce/purge-expired
Authorization: Bearer <token>
Content-Type: application/json
```

Cũng gọi được bằng `POST /rpc/edc.mqtt.nonce/purge_expired`. Body là RPC envelope chuẩn:

| Name | Type | Required | Ý nghĩa |
| --- | --- | --- | --- |
| `kwargs.retention_hours` | integer | No | Xóa các dòng cũ hơn số giờ này, ví dụ `48`. Mặc định `24`. Giá trị 0, âm hoặc không phải số sẽ quay về `24`. `retentionHours` cũng dùng được. |

### Example

```bash
curl -X POST "$BASE_URL/base/edc/mqtt/nonce/purge-expired" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"args": [], "kwargs": {"retention_hours": 48}}'
```

### Response

```json
{ "id": "111020261535481234570001", "result": { "deleted": 120 } }
```

| Field | Ý nghĩa |
| --- | --- |
| `result.deleted` | Số dòng nonce đã bị xóa. |

### Errors

Lỗi dùng định dạng chuẩn trong API guide (xem "Response" → "Errors").

| HTTP | `error.code` | Ý nghĩa | Bạn nên làm gì |
| --- | --- | --- | --- |
| `401` | `-32001` | Thiếu hoặc sai token. | Lấy token mới. |
| `403` | `-32003` | Bạn không có quyền write `edc.mqtt.nonce` (`permission denied`). | Nhờ admin. |
| `400` | `-32000` | EDC service chưa được nạp (`edc transport service is not initialized`), hoặc xóa thất bại (nội dung lỗi database). | Thử lại sau; đọc `message`. |

### Good to know

- Gọi lại nhiều lần vẫn an toàn.
- Nonce cũ hơn cửa sổ replay không thể khớp nữa, nên xóa chúng không làm yếu việc chống phát lại. Đừng đặt `retention_hours` thấp hơn cửa sổ replay của secure event.
- Mốc cắt được so với `created_at` của từng dòng.
