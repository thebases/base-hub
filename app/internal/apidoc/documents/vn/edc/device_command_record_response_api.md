## Record Device Command Response

Lưu reply của terminal vào command mà nó trả lời. Command được chuyển sang `ACKED` (đã nhận) hoặc trạng thái cuối (`SUCCESS`, `FAILED`, ...) và reply được lưu lại. Hệ thống tự gọi nó cho mọi reply đến qua MQTT. Bạn chỉ tự gọi khi cần phát lại một reply lấy từ nơi khác, ví dụ từ device log khi kết nối broker bị mất.

### Before you start

- Cần bearer token và quyền **write** trên `edc.device.command`.
- Cần message reply của terminal. Reply là một JSON object nhỏ ("envelope") gồm `c` (mã reply = mã command + 1000, nên reply của command `101` có `c=1101`), `i` (`commandId` của command), `sn` (serial) và `st` (số trạng thái, `0` = thành công).

### Request

```http
POST /base/edc/device/command/record-response
Authorization: Bearer <token>
Content-Type: application/json
```

Cũng gọi được bằng `POST /rpc/edc.device.command/record_response` với cùng body.

```json
{ "args": [], "kwargs": { "message": { "c": 1101, "i": "cmd-7f3a", "sn": "SN0001", "st": 0, "p": {} }, "lenient": false } }
```

| Name | Type | Required | Ý nghĩa |
| --- | --- | --- | --- |
| `kwargs.message` | object hoặc JSON string | Yes | Reply của terminal (EDC v1.3 envelope) với `c`, `i`, `sn`, `st` như mô tả trên. Key `response` được chấp nhận làm tên khác của `message`. |
| `kwargs.lenient` | boolean | No | Mặc định `false`. Với `true`, reply không áp dụng được (thiếu `st`, thiếu `i`/`sn`, JSON hỏng, không có command khớp) sẽ trả `{"ok": false, "reason": "..."}` thay vì lỗi. MQTT workflow gọi theo cách này. Lỗi database luôn là lỗi. Chuỗi `"true"`/`"false"` được chấp nhận; giá trị khác coi như `false`. |

### Example

```bash
curl -X POST "$BASE_URL/base/edc/device/command/record-response" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"kwargs":{"lenient":true,"message":{"c":1101,"i":"cmd-7f3a","sn":"SN0001","st":0,"p":{}}}}'
```

### Response

```json
{
  "id": "111020261535481234570001",
  "result": {
    "id": 42,
    "serial_number": "SN0001",
    "command_id": "cmd-7f3a",
    "status": "SUCCESS",
    "acked_at": 1791705601,
    "completed_at": 1791705603
  }
}
```

`result` là dòng command sau khi cập nhật (đã rút gọn). Tên field dạng snake_case. Ở chế độ lenient, reply bị từ chối trả về `{"result": {"ok": false, "reason": "no matching device command"}}`.

### Errors

Lỗi dùng định dạng chuẩn trong API guide (xem "Response" → "Errors").

| HTTP | `error.code` | Ý nghĩa | Bạn nên làm gì |
| --- | --- | --- | --- |
| `400` | `-32000` / `-32602` | Chỉ ở chế độ strict: `message` thiếu hoặc không phải JSON hợp lệ, reply không có `st`, thiếu `i` hoặc `sn`, không có command khớp (`device command not found`), hoặc lỗi database. | Sửa message, hoặc gửi `lenient: true`. |
| `401` | `-32001` | Thiếu hoặc sai token. | Lấy token mới. |
| `403` | `-32003` | Bạn không có quyền write `edc.device.command`. | Nhờ admin. |

### Good to know

- Phát lại cùng một reply hai lần không gây hại.
- Reply không khớp command nào, hoặc có `c` khác `command code + 1000`, bị bỏ qua âm thầm. Ở chế độ strict, việc này kết thúc bằng lỗi `device command not found` vì lời gọi đọc lại dòng command.
- Reply có `rc` = `req_duplicate` bị bỏ qua.
- Reply "đã nhận" đầu tiên (`st=1`, pending) chuyển command `CREATED`/`PUBLISHED`/`TIMEOUT`/`FAILED` (chưa có `completed_at`) sang `ACKED` và điền `acked_at`. Reply cuối đặt trạng thái cuối. Trạng thái cuối là "dính": một acknowledgement đến muộn không ghi đè được. Một reply thật đến muộn thì ghi đè được `TIMEOUT` do job quét đặt.
- Reply resync (command `120`) có kèm thứ tự item sẽ được nối vào danh sách item của command. Command hoàn tất khi đủ item.
