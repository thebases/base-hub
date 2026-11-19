## List Device Commands

Trả về một trang các command đã gửi tới terminal, mới nhất trước. Dùng cho màn hình lịch sử command, hoặc để xem điều gì đã xảy ra với các command của một terminal.

### Before you start

Cần bearer token và quyền **read** trên `edc.device.command`.

### Request

```http
GET /base/device/command?serial={serial}&status={status}&commandType={commandType}&limit={limit}&offset={offset}
Authorization: Bearer <token>
```

Tất cả tham số đều không bắt buộc.

| Name | Type | Required | Ý nghĩa |
| --- | --- | --- | --- |
| `serial` | string | No | Serial của terminal (số duy nhất in trên máy). Khớp một phần, không phân biệt hoa thường: `SN00` khớp `SN0001`. `serial_number` cũng dùng được. |
| `status` | string | No | Khớp chính xác, chữ hoa: `CREATED`, `PUBLISHED`, `ACKED`, `SUCCESS`, `FAILED`, `TIMEOUT`, `EXPIRED`. |
| `commandType` | string | No | Khớp chính xác: `START_TRANSACTION` (code `100`-`199`) hoặc `SYNC_CONFIG` (code `200`-`299`). `command_type` cũng dùng được. Model còn liệt kê `PUSH_CONFIG`, `REBOOT`, `OTA_UPDATE`, `PRINT_TEST`, `LOCK_DEVICE`, `UNLOCK_DEVICE` và `CUSTOM`, nhưng backend hiện chưa tạo các loại này. |
| `limit` | integer | No | Kích thước trang. Mặc định `50`, tối đa `500`. Giá trị lớn hơn 500, bằng 0 hoặc không phải số sẽ quay về `50`. (`__limit` cũng dùng được.) |
| `offset` | integer | No | Số dòng bỏ qua. Mặc định `0`. (`__offset` cũng dùng được.) |

Giá trị rỗng sẽ bị bỏ qua.

### Example

```bash
curl "$BASE_URL/base/device/command?serial=SN0001&status=PUBLISHED&limit=20" \
  -H "Authorization: Bearer $TOKEN"
```

### Response

```json
{
  "id": "111020261535481234570001",
  "result": {
    "items": [
      {
        "id": 128,
        "commandId": "6f1c2b8e-3a4d-4f5e-9b7a-1c2d3e4f5a6b",
        "serialNumber": "SN0001",
        "commandType": "SYNC_CONFIG",
        "commandCode": 200,
        "payload": { "id": "20260926080000" },
        "status": "PUBLISHED",
        "mqttTopic": "base/default/SN0001/cmd",
        "publishedAt": 1790409601,
        "expiredAt": 1790409660,
        "createdBy": "7",
        "createdAt": 1790409600,
        "updatedAt": 1790409601
      }
    ],
    "total": 1
  }
}
```

| Field | Ý nghĩa |
| --- | --- |
| `result.items` | Các command của trang này, mới nhất trước. Mỗi item có các field như "Get Device Command". Field rỗng bị bỏ qua. |
| `result.total` | Tổng số command khớp bộ lọc (không phụ thuộc `limit` và `offset`). |

### Errors

Lỗi dùng định dạng chuẩn trong API guide (xem "Response" → "Errors").

| HTTP | `error.code` | Ý nghĩa | Bạn nên làm gì |
| --- | --- | --- | --- |
| `401` | `-32001` | Thiếu hoặc sai token. | Lấy token mới. |
| `403` | `-32003` | Bạn không có quyền read `edc.device.command`. | Nhờ admin. |
| `500` | `-32603` | Lỗi server không mong đợi. | Thử lại; báo kèm `id` của response. |

### Good to know

- `status` hoặc `commandType` không hợp lệ không gây lỗi, bạn chỉ nhận danh sách rỗng (hai field này khớp chính xác, phân biệt hoa thường; chỉ `serial` khớp một phần).
- Để biết vì sao command thất bại, lọc `status=FAILED` rồi đọc `errorCode` và `errorMessage` của từng item.
- Để gửi command mới, dùng `POST /base/devices/{serial}/commands` (xem "Device Commands API").
- Cũng gọi được bằng `POST /base/edc/device/command/list` (RPC `POST /rpc/edc.device.command/list_commands`). Gửi bộ lọc trong `kwargs` (`serial`, `status`, `command_type`, `limit`, `offset`). Các item dùng tên field snake_case. Caller không có quyền read nhận `{"result": null}` thay vì `403`, và khi lỗi trả `400` kèm message dạng text thuần.
