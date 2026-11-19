## Lấy một audit log

Trả về một dòng audit log. Audit log là lịch sử ai đã làm gì với resource nào. Dùng để hiển thị chi tiết một dòng lịch sử.

### Trước khi bắt đầu

- Bạn cần bearer token.
- User của bạn phải có quyền đọc audit log (`edc.audit.log`). User thường (`base.group_user`) có quyền này. Nếu không, bạn nhận `403`.
- Lấy `id` số từ "Danh sách audit log".

### Request

```http
GET /base/audit-logs/{id}
Authorization: Bearer <token>
```

| Path parameter | Kiểu | Bắt buộc | Ý nghĩa |
| --- | --- | --- | --- |
| `id` | integer | Có | Id của audit log, ví dụ `1024`. Phải là số, không phải UUID. |

### Ví dụ

```bash
curl "$BASE_URL/base/audit-logs/1024" \
  -H "Authorization: Bearer $TOKEN"
```

### Response

HTTP `200`:

```json
{
  "id": "111020261535481234570001",
  "result": {
    "id": 1024,
    "action": "device.provisioned",
    "resourceType": "device",
    "resourceId": "SN0001",
    "afterData": {
      "provisioningStatus": "ACTIVE",
      "secretVersion": 1,
      "mqttClientId": "SN0001"
    },
    "createdAt": "2026-09-26T08:15:02Z"
  }
}
```

| Field | Ý nghĩa |
| --- | --- |
| `result.id` | Id audit log. |
| `result.action` | Điều đã xảy ra, ví dụ `device.provisioned`. |
| `result.resourceType` | Loại đối tượng bị thay đổi, ví dụ `device`. |
| `result.resourceId` | Đối tượng nào bị thay đổi, ví dụ serial `SN0001`. Bị bỏ khi rỗng. |
| `result.actor` | Ai thực hiện, ví dụ một email. Bị bỏ khi không được ghi lại. |
| `result.beforeData` / `result.afterData` | Giá trị trước và sau khi đổi. JSON tự do. Các key phụ thuộc vào action. Bị bỏ khi rỗng. |
| `result.ipAddress` / `result.userAgent` | Call đến từ đâu. Bị bỏ khi không được ghi lại. |
| `result.createdAt` | Thời điểm xảy ra (UTC, ISO 8601). |

### Lỗi

Lỗi dùng định dạng chuẩn được mô tả trong API guide (xem "Response" → "Errors").

| HTTP | `error.code` | Ý nghĩa | Bạn nên làm gì |
| --- | --- | --- | --- |
| `400` | `-32602` | `id` không phải số. | Gửi id là số nguyên. |
| `401` | `-32001` | Thiếu token hoặc token không hợp lệ. | Lấy token mới. |
| `403` | `-32003` | User của bạn không được đọc audit log. | Nhờ admin cấp quyền. |
| `404` | `-32004` | Không có audit log nào với id này. | Kiểm tra lại id. |
| `500` | `-32603` | Lỗi database. | Thử lại sau. Báo kèm `id` của response. |

### Cần biết

- Audit log chỉ được thêm vào (append-only). Không có route tạo, sửa hay xóa.
- Field tùy chọn rỗng bị bỏ khỏi JSON. Đừng mong đợi key nào cũng có mặt.
