## Publish MQTT Message

Gửi một command tới một terminal bằng dạng request cũ `{topic, message}`. Dùng để test, hoặc cho client vẫn gọi route cũ. Code mới nên gọi `POST /base/devices/{serial}/commands` (xem "Device Commands API"). Route này chỉ là lớp bọc mỏng quanh nó: command được kiểm tra, ký, lưu, publish và theo dõi giống hệt mọi command khác.

### In a nutshell

1. Bạn gửi `topic` (chỉ để tìm terminal) và `message` chứa mã command.
2. Backend bỏ qua mọi thứ trong `message` ngoài `c`, `p` và `e`. Nó tự dựng và ký message riêng với request id mới.
3. Nó lưu command, publish lên broker và trả `202`.
4. `202` nghĩa là broker đã nhận message. Nó **không** có nghĩa là terminal đã nhận hay đã chạy.

### Before you start

- Cần bearer token và quyền **write** trên `edc.device.command`.
- Terminal phải tồn tại và có signing secret.
- Kết nối broker phải hoạt động. Nếu không bạn nhận `503` khi publish.

### Request

```http
POST /mqtt/publish
Authorization: Bearer <token>
Content-Type: application/json
```

| Name | Type | Required | Ý nghĩa |
| --- | --- | --- | --- |
| `topic` | string | Yes | Command topic của terminal. Chỉ dùng để chọn terminal: `base/<group name>/<serial>/cmd`, `base/<serial>/cmd` hoặc `<serial>/cmd`, ví dụ `base/default/SN0001/cmd`. Khoảng trắng hai đầu bị cắt. Topic khác bị từ chối. |
| `message` | object | Yes | Command dạng JSON object (một string chứa JSON object cũng được chấp nhận). Không được là `null`. Các key ở dưới. |

Các key được đọc từ `message` (tên ngắn v1.3 hoặc tên dài):

| Key | Aliases | Required | Ý nghĩa |
| --- | --- | --- | --- |
| `c` | `command_code`, `commandCode` | Yes | Mã command, một trong các mã hỗ trợ ở "Device Commands API" (ví dụ `100`, `112`, `200`). |
| `p` | `payload` | No | Object dữ liệu của command. Gửi đúng thứ terminal mong đợi cho mã đó. |
| `e` | `ttl_seconds`, `ttlSeconds` | No | Thời gian sống tính bằng giây, `30` đến `3600`. Mặc định `60`. Sau thời gian này terminal từ chối command vì hết hạn. |

Mọi key khác, gồm `v`, `i`, `s`, `u` và `h` (version, request id, thời gian, người gửi, chữ ký), đều bị bỏ qua. Backend luôn dựng message mới, chọn request id (`i`) mới và ký bằng secret riêng của terminal. Người gọi không thể giả mạo hay phát lại command.

### Example

```bash
curl -X POST "$BASE_URL/mqtt/publish" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"topic":"base/default/SN0001/cmd","message":{"c":200,"p":{},"e":60}}'
```

### Response

HTTP `202`. Body này **không** được bọc trong `{"id","result"}`.

```json
{
  "status": "accepted",
  "topic": "base/default/SN0001/cmd",
  "message": { "c": 200, "p": {}, "e": 60 },
  "qos": 1,
  "brokerAcked": true,
  "deliveryState": "accepted_by_broker"
}
```

| Field | Ý nghĩa |
| --- | --- |
| `status` | Luôn là `accepted`. |
| `topic` | Topic thực sự được dùng: group name hiện tại của terminal, hoặc `<serial>/cmd` với terminal qua launcher bridge. Có thể khác `topic` bạn gửi. |
| `message` | `message` của bạn, được echo lại đúng như đã gửi. |
| `qos` | Luôn là `1` (giao ít nhất một lần). Message không bao giờ được retain. |
| `brokerAcked` | `true`: broker đã xác nhận nhận message. |
| `deliveryState` | `accepted_by_broker` khi thành công. |

Để theo dõi command sau đó, gọi `GET /base/device/command?serial=SN0001` hoặc `GET /base/mqtt-messages`.

### Errors

Lỗi dùng định dạng chuẩn trong API guide (xem "Response" → "Errors"). Nội dung lỗi gốc nằm trong `error.message`. Với lỗi broker, `error.data` còn chứa `topic`, `deliveryState` và `qos`, ví dụ `{"deliveryState": "mqtt_client_unavailable", "qos": 1, "topic": "base/default/SN0001/cmd"}`.

| HTTP | `error.message` | Ý nghĩa | Bạn nên làm gì |
| --- | --- | --- | --- |
| `400` | `invalid_json` | Body không phải JSON hợp lệ. | Sửa body. |
| `400` | `Both topic and message are required` | `topic` rỗng, hoặc `message` thiếu hay là `null`. | Gửi cả hai. |
| `400` | `message must be a JSON object (a v1.3 command envelope)` | `message` không phải object. | Gửi object. |
| `400` | `topic must be base/<group>/<serial>/cmd, base/<serial>/cmd or <serial>/cmd` | Không phải command topic của terminal. | Sửa topic. |
| `400` | `command_code is required to publish a v1.3 command` | `message` không có `c`. | Thêm `c`. |
| `400` | `command code is not supported by the current lite payment runtime` | Mã command không được hỗ trợ. | Dùng mã được hỗ trợ. |
| `400` | `ttl_seconds must be between 30 and 3600` | `e` không hợp lệ. | Dùng 30 đến 3600. |
| `400` | `payload.id is required for this command code` | Mã `109`, `113` hoặc `114` thiếu `p.id`. | Thêm `p.id`. |
| `401` | | Thiếu hoặc sai token. | Lấy token mới. |
| `403` | `forbidden` | Không có quyền write `edc.device.command`. | Nhờ admin. |
| `404` | `device not found` | Không có terminal nào với serial lấy từ `topic`. | Kiểm tra serial (chữ hoa). |
| `409` | `device command config missing: ...` | Terminal không có signing secret dùng được. | Provision hoặc cấu hình terminal trước. |
| `500` | `edc provisioning secret encryption key not configured (app.encryptKey)` | Server không giải mã được secret của terminal. | Liên hệ đội nền tảng. |
| `503` | `mqtt client unavailable` | Server chưa kết nối broker (`deliveryState: mqtt_client_unavailable`). | Thử lại sau. |
| `504` | `mqtt publish timeout waiting for broker ack: ...` | Broker không xác nhận kịp (`broker_ack_timeout`). | Xem cảnh báo bên dưới. |
| `502` | `mqtt broker rejected publish: ...` | Broker từ chối publish, ví dụ do ACL của broker (`broker_rejected_publish`). | Kiểm tra ACL của broker. |
| `500` | nội dung lỗi | Lỗi publish khác (`publish_failed`), hoặc lỗi chưa phân loại như lỗi database (khi đó không có `deliveryState`). | Thử lại; báo kèm `id` của response. |

> **Warning:** sau `504`, message vẫn có thể đã được giao. Dòng command là `FAILED`, nhưng một reply muộn của terminal vẫn có thể ghi kết quả thật. Đừng gửi lại một cách mù quáng các command liên quan tiền (`100`-`106`).

### Good to know

- Route cũ publish JSON bất kỳ tới topic bất kỳ. Điều đó không còn nữa: topic chỉ để nhận diện terminal, và chỉ command được hỗ trợ mới được gửi.
- Command được lưu trước khi publish. Nếu publish lỗi, dòng vẫn còn và được đánh dấu `FAILED` kèm `errorCode` (xem "Get Device Command").
- `X-Idempotency-Key` **không** được xử lý ở đây. Mỗi lời gọi tạo một command mới với request id mới.
- Cũng gọi được bằng `POST /base/edc/device/command/publish` (RPC `POST /rpc/edc.device.command/publish`) với `topic` và `message` trong `kwargs`. Nó trả `{"result": {...}}` với dòng command dạng snake_case cùng `delivery_state` (không phải body `202` ở trên). Mọi lỗi là `400` kèm message dạng text thuần; không có quyền thì `403`.
