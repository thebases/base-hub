## Get Device Command

Trả về một command đã gửi tới terminal, gồm acknowledgement và kết quả của terminal khi đã có. Dùng để theo dõi command sau khi gửi, hoặc xem vì sao nó thất bại.

### Before you start

- Cần bearer token và quyền **read** trên `edc.device.command`.
- Cần `id` dạng số của command, lấy từ "List Device Commands". Đây **không** phải `commandId` (UUID).

### Request

```http
GET /base/device/command/{id}
Authorization: Bearer <token>
```

| Name | Type | Required | Ý nghĩa |
| --- | --- | --- | --- |
| `id` (path) | integer | Yes | Id số của command, ví dụ `128`. |

### Example

```bash
curl "$BASE_URL/base/device/command/128" \
  -H "Authorization: Bearer $TOKEN"
```

### Response

```json
{
  "id": "111020261535481234570001",
  "result": {
    "id": 128,
    "commandId": "6f1c2b8e-3a4d-4f5e-9b7a-1c2d3e4f5a6b",
    "serialNumber": "SN0001",
    "commandType": "SYNC_CONFIG",
    "commandCode": 200,
    "payload": { "id": "20260926080000" },
    "status": "SUCCESS",
    "mqttTopic": "base/default/SN0001/cmd",
    "publishedAt": 1790409601,
    "ackedAt": 1790409602,
    "completedAt": 1790409609,
    "expiredAt": 1790409660,
    "ackPayload": { "c": 1200, "st": 1, "s": 1790409602, "p": { "deviceId": "SN0001" } },
    "resultPayload": { "c": 1200, "st": 0, "s": 1790409609, "p": { "deviceId": "SN0001" } },
    "createdBy": "7",
    "createdAt": 1790409600,
    "updatedAt": 1790409609
  }
}
```

| Field | Ý nghĩa |
| --- | --- |
| `id` | Id số của dòng. |
| `commandId` | Id duy nhất (UUID) của message command. Terminal chép nó vào reply (field `i`). |
| `serialNumber` | Serial của terminal đích. |
| `commandType` | `START_TRANSACTION` cho code `100`-`199`, `SYNC_CONFIG` cho code `200`-`299`. |
| `commandCode` | Mã command số đã gửi. |
| `payload` | Dữ liệu gửi kèm command. |
| `status` | Command đang ở đâu trong vòng đời: `CREATED`, `PUBLISHED`, `ACKED`, `SUCCESS`, `FAILED`, `TIMEOUT` hoặc `EXPIRED`. Xem "Device Commands API". |
| `mqttTopic` | MQTT topic mà command được publish tới. |
| `publishedAt`, `ackedAt`, `completedAt`, `expiredAt` | Thời điểm publish, acknowledge, hoàn tất và hết hạn. Bị bỏ qua cho đến khi có giá trị. |
| `ackPayload`, `resultPayload` | Bản sao acknowledgement và reply cuối của terminal (`c` = command code + 1000, `st` = status, `s` = thời gian, có thể có `rc` là lý do và `p` là dữ liệu). |
| `errorCode`, `errorMessage` | Lý do command thất bại. Bỏ qua khi rỗng. |
| `createdBy` | Id của user gửi command, dạng string. |
| `createdAt`, `updatedAt` | Thời gian của dòng. |

Các field rỗng bị bỏ khỏi JSON. Thời gian là Unix seconds (xem API guide).

### Errors

Lỗi dùng định dạng chuẩn trong API guide (xem "Response" → "Errors").

| HTTP | `error.code` | Ý nghĩa | Bạn nên làm gì |
| --- | --- | --- | --- |
| `400` | `-32602` | `id` không phải số nguyên, ví dụ bạn truyền `commandId` UUID. | Dùng `id` dạng số. |
| `401` | `-32001` | Thiếu hoặc sai token. | Lấy token mới. |
| `403` | `-32003` | Bạn không có quyền read `edc.device.command`. | Nhờ admin. |
| `404` | `-32004` | Không có command nào với `id` này. | Kiểm tra lại id. |
| `500` | `-32603` | Lỗi server không mong đợi. | Thử lại; báo kèm `id` của response. |

### Good to know

- Command thất bại có `errorCode` và `errorMessage`. Lý do có thể là lỗi publish, lỗi do terminal báo, hoặc command bị bỏ (event `c=1899`).
- `mqttTopic` là `base/<group name>/<serial>/cmd`, hoặc `<serial>/cmd` với terminal đi qua launcher bridge.
