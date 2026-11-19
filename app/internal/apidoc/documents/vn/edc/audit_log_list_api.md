## Danh sách audit log

Trả về một trang các dòng audit log, mới nhất trước. Audit log là lịch sử ai đã làm gì với resource nào. Dùng để làm màn hình hoạt động hoặc lịch sử.

### Trước khi bắt đầu

- Bạn cần bearer token.
- User của bạn phải có quyền đọc audit log (`edc.audit.log`). User thường (`base.group_user`) có quyền này. Nếu không, bạn nhận `403`.

### Request

```http
GET /base/audit-logs?limit={limit}&offset={offset}&actor={actor}&action={action}&resource_type={resource_type}&resource_id={resource_id}
Authorization: Bearer <token>
```

| Query parameter | Kiểu | Bắt buộc | Ý nghĩa |
| --- | --- | --- | --- |
| `limit` | integer | Không | Kích thước trang. Mặc định `50`, tối đa `500`. Giá trị `<= 0`, lớn hơn `500` hoặc không phải số sẽ thành `50`. |
| `offset` | integer | Không | Số dòng bỏ qua. Mặc định `0`. |
| `actor` | string | Không | Khớp một phần với người thực hiện, không phân biệt hoa thường. Ví dụ `admin` khớp `admin@thebase.vn`. |
| `action` | string | Không | Khớp chính xác, phân biệt hoa thường. Ví dụ `device.provisioned`. |
| `resource_type` | string | Không | Khớp chính xác. Ví dụ `device`, `config`, hoặc tên model như `edc.transaction`. |
| `resource_id` | string | Không | Khớp chính xác với định danh của resource. Ví dụ serial `SN0001`. |

Các bộ lọc được kết hợp bằng AND. Khoảng trắng quanh giá trị bị bỏ qua. Giá trị trống bị bỏ qua.

### Ví dụ

```bash
curl "$BASE_URL/base/audit-logs?limit=20&offset=0&resource_type=device&resource_id=SN0001" \
  -H "Authorization: Bearer $TOKEN"
```

### Response

HTTP `200`:

```json
{
  "id": "111020261535481234570001",
  "result": {
    "items": [
      {
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
    ],
    "total": 1
  }
}
```

| Field | Ý nghĩa |
| --- | --- |
| `result.items` | Các dòng của trang này, sắp xếp theo `createdAt`, mới nhất trước. |
| `result.total` | Tổng số dòng khớp bộ lọc trên mọi trang. Không tính `limit` và `offset`. Dùng để phân trang. |
| `items[].action` / `resourceType` / `resourceId` | Điều đã xảy ra, với loại đối tượng nào, và đối tượng nào. |
| `items[].actor`, `beforeData`, `afterData`, `ipAddress`, `userAgent` | Bị bỏ khi rỗng. `beforeData` và `afterData` là JSON tự do. Các key phụ thuộc vào action. |
| `items[].createdAt` | Thời điểm xảy ra (UTC, ISO 8601). |

### Lỗi

Lỗi dùng định dạng chuẩn được mô tả trong API guide (xem "Response" → "Errors").

| HTTP | `error.code` | Ý nghĩa | Bạn nên làm gì |
| --- | --- | --- | --- |
| `401` | `-32001` | Thiếu token hoặc token không hợp lệ. | Lấy token mới. |
| `403` | `-32003` | User của bạn không được đọc audit log. | Nhờ admin cấp quyền. |
| `500` | `-32603` | Lỗi database. | Thử lại sau. Báo kèm `id` của response. |

### Cần biết

- Audit log chỉ được thêm vào (append-only). Không có route tạo, sửa hay xóa.
- Để xem đầy đủ một dòng, gọi `GET /base/audit-logs/{id}`.
