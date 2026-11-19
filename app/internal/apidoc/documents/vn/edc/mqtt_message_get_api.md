## Get MQTT Message

Trả về một MQTT message đã được ghi lại (message server gửi tới terminal, hoặc nhận từ terminal), gồm payload đã parse và payload thô. Dùng khi bạn cần đúng nội dung đã đi qua đường truyền.

### Before you start

Cần bearer token và quyền **read** trên `edc.mqtt.message`. Lấy `id` dạng số từ "List MQTT Messages".

### Request

```http
GET /base/mqtt-messages/{id}
Authorization: Bearer <token>
```

| Name | Type | Required | Ý nghĩa |
| --- | --- | --- | --- |
| `id` (path) | integer | Yes | Id của message, ví dụ `90413`. |

### Example

```bash
curl "$BASE_URL/base/mqtt-messages/90413" \
  -H "Authorization: Bearer $TOKEN"
```

### Response

```json
{
  "id": "111020261535481234570001",
  "result": {
    "id": 90413,
    "serialNumber": "SN0001",
    "direction": "OUTBOUND",
    "topic": "base/default/SN0001/cmd",
    "payload": { "v": 1, "c": 200, "i": "6f1c2b8e-3a4d-4f5e-9b7a-1c2d3e4f5a6b", "s": 1790412001, "e": 60, "u": "backend", "h": "9f2c...e71a", "p": {} },
    "rawPayload": "{\"v\":1,\"c\":200,\"i\":\"6f1c2b8e-3a4d-4f5e-9b7a-1c2d3e4f5a6b\",\"s\":1790412001,\"e\":60,\"u\":\"backend\",\"h\":\"9f2c...e71a\",\"p\":{}}",
    "qos": 1,
    "retained": false,
    "sync": false,
    "receivedAt": 1790409601
  }
}
```

| Field | Ý nghĩa |
| --- | --- |
| `serialNumber` | Serial của terminal. Bỏ qua khi message không có serial nhận diện được. |
| `direction` | `OUTBOUND` (server gửi tới terminal) hoặc `INBOUND` (terminal gửi tới server). |
| `topic` | MQTT topic (tên "kênh" mà message dùng). |
| `payload` | Message đã parse thành JSON. Bỏ qua khi rỗng. |
| `rawPayload` | Đúng nội dung text trên đường truyền. Dùng khi cần định dạng gốc. Bỏ qua khi rỗng. |
| `qos` | Mức giao nhận. `1` nghĩa là "ít nhất một lần". |
| `retained` | Broker có giữ message cho subscriber mới hay không. |
| `sync` | Hiện luôn là `false`: Base HUB không chuyển tiếp message tới webhook. |
| `receivedAt` | Thời điểm message được ghi (Unix seconds). |

### Errors

Lỗi dùng định dạng chuẩn trong API guide (xem "Response" → "Errors").

| HTTP | `error.code` | Ý nghĩa | Bạn nên làm gì |
| --- | --- | --- | --- |
| `400` | `-32602` | `id` không phải số nguyên. | Dùng id dạng số. |
| `401` | `-32001` | Thiếu hoặc sai token. | Lấy token mới. |
| `403` | `-32003` | Bạn không có quyền read `edc.mqtt.message`. | Nhờ admin. |
| `404` | `-32004` | Không có message nào với `id` này. | Kiểm tra lại id. |
| `500` | `-32603` | Lỗi server không mong đợi. | Thử lại; báo kèm `id` của response. |

### Good to know

- Với dòng `OUTBOUND`, `rawPayload` là command đã ký mà backend publish. Nó được lưu trong cùng transaction với việc đánh dấu command là `PUBLISHED`.
