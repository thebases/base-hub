## List MQTT Messages

Trả về một trang các MQTT message trao đổi giữa server và terminal, cả nhận (`INBOUND`) lẫn gửi (`OUTBOUND`), mới nhất trước. Dùng để điều tra terminal đã gửi hoặc nhận gì.

### Before you start

Cần bearer token và quyền **read** trên `edc.mqtt.message`.

### Request

```http
GET /base/mqtt-messages?serial={serial}&direction={direction}&topic={topic}&sync={sync}&limit={limit}&offset={offset}
Authorization: Bearer <token>
```

Tất cả tham số đều không bắt buộc. Giá trị rỗng bị bỏ qua.

| Name | Type | Required | Ý nghĩa |
| --- | --- | --- | --- |
| `serial` | string | No | Serial của terminal. Khớp một phần, không phân biệt hoa thường, ví dụ `SN0001`. |
| `direction` | string | No | `INBOUND` hoặc `OUTBOUND`. Được đổi sang chữ hoa trước khi so khớp, nên `inbound` cũng được. |
| `topic` | string | No | MQTT topic (tên kênh). Khớp một phần, không phân biệt hoa thường, ví dụ `txn` hoặc `SN0001/cmd`. |
| `sync` | boolean | No | `true` / `false` (hoặc `1` / `0`). Lọc theo cờ `sync` đã lưu, hiện luôn là `false`. |
| `limit` | integer | No | Kích thước trang. Mặc định `50`, tối đa `500`. Giá trị lớn hơn 500, bằng 0 hoặc không phải số sẽ quay về `50`. |
| `offset` | integer | No | Số dòng bỏ qua. Mặc định `0`. |

### Example

```bash
curl "$BASE_URL/base/mqtt-messages?serial=SN0001&direction=INBOUND&limit=20" \
  -H "Authorization: Bearer $TOKEN"
```

### Response

```json
{
  "id": "111020261535481234570001",
  "result": {
    "items": [
      {
        "id": 90412,
        "serialNumber": "SN0001",
        "direction": "INBOUND",
        "topic": "base/txn",
        "payload": { "serial": "SN0001", "c": 1102, "p": { "amount": 150000 } },
        "rawPayload": "{\"serial\":\"SN0001\",\"c\":1102,\"p\":{\"amount\":150000}}",
        "qos": 1,
        "retained": false,
        "sync": false,
        "receivedAt": 1790409600
      }
    ],
    "total": 1
  }
}
```

| Field | Ý nghĩa |
| --- | --- |
| `result.items` | Các message của trang này, mới nhất trước (theo `receivedAt`). Mỗi item có các field như mô tả ở "Get MQTT Message". |
| `result.total` | Tổng số message khớp bộ lọc (không phụ thuộc `limit` và `offset`). |

### Errors

Lỗi dùng định dạng chuẩn trong API guide (xem "Response" → "Errors").

| HTTP | `error.code` | Ý nghĩa | Bạn nên làm gì |
| --- | --- | --- | --- |
| `400` | `-32602` | `sync` không phải boolean (`error.data.reason` = `invalid_sync`). | Gửi `true` hoặc `false`. |
| `401` | `-32001` | Thiếu hoặc sai token. | Lấy token mới. |
| `403` | `-32003` | Bạn không có quyền read `edc.mqtt.message`. | Nhờ admin. |
| `500` | `-32603` | Lỗi server không mong đợi. | Thử lại; báo kèm `id` của response. |

### Good to know

- Message không có serial nhận diện được thì không tìm được bằng `serial`. Hãy dùng `topic`.
- `direction` không hợp lệ trả danh sách rỗng, không phải lỗi.
- Topic của command có dạng `base/<group name>/<serial>/cmd` (hoặc `<serial>/cmd` với terminal đi qua launcher bridge). Terminal gửi event trên `base/evt` và `base/txn`. Lọc `topic=/cmd` để chỉ xem command do server gửi.
- Cũng gọi được bằng `POST /base/edc/mqtt/message/list` (RPC `POST /rpc/edc.mqtt.message/list_messages`). Gửi bộ lọc trong `kwargs` (`serial`, `direction`, `topic`, `sync`, `limit`, `offset`). Các item dùng tên field snake_case. Caller không có quyền read nhận `{"result": null}` thay vì `403`, và `sync` sai trả `400` kèm message dạng text thuần.
