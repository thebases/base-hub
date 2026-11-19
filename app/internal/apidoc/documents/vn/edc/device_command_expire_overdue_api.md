## Expire Overdue Device Commands

Chuyển các command sang `TIMEOUT` khi terminal không trả lời kịp. Một job nền đã tự làm việc này; chỉ gọi endpoint này khi bạn muốn chạy ngay lập tức (ví dụ trong script hỗ trợ hoặc khi test).

### Before you start

- Cần bearer token (xem API guide).
- User cần quyền **write** trên `edc.device.command` (model lưu các command). Thiếu quyền sẽ nhận `403`.

### Request

```http
POST /base/edc/device/command/expire-overdue
Authorization: Bearer <token>
Content-Type: application/json
```

Không có tham số. Gửi `{}`. Cũng gọi được bằng `POST /rpc/edc.device.command/expire_overdue`.

### Example

```bash
curl -X POST "$BASE_URL/base/edc/device/command/expire-overdue" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" -d '{}'
```

### Response

```json
{ "id": "111020261535481234570001", "result": { "expired": 3 } }
```

| Field | Ý nghĩa |
| --- | --- |
| `result.expired` | Số command được chuyển sang `TIMEOUT` trong lần gọi này. `0` nghĩa là không có command nào quá hạn. |

### Errors

Lỗi dùng định dạng chuẩn trong API guide (xem "Response" → "Errors").

| HTTP | `error.code` | Ý nghĩa | Bạn nên làm gì |
| --- | --- | --- | --- |
| `401` | `-32001` | Thiếu hoặc sai token. | Lấy token mới. |
| `403` | `-32003` | User không có quyền write `edc.device.command`. | Nhờ admin cấp quyền. |
| `400` / `503` | `-32000` / `-32050` | Database không khả dụng hoặc cập nhật thất bại. | Đọc `message` và thử lại sau. |

### Good to know

- Một command chỉ bị quét khi đủ các điều kiện: status là `CREATED` hoặc `PUBLISHED`, terminal chưa từng acknowledge, và `expired_at` đã qua hơn 2 phút. Command được gán `error_code = device_no_response`.
- Command `ACKED` không bao giờ bị quét. Một command thanh toán đã được nhận có thể chạy lâu hơn thời gian hết hạn.
- `completed_at` cố ý để trống. Nếu terminal trả lời muộn, kết quả thật vẫn được ghi (xem "Record Device Command Response").
- Gọi lại nhiều lần vẫn an toàn. Job tự động là cron job `edc.expire_overdue_commands`.
