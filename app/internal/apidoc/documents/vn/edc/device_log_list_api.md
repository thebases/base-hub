## List Device Logs

Trả về một trang các log do terminal upload, mới nhất trước. Dùng cho màn hình device log hoặc để tìm file log của một terminal.

### Trước khi bắt đầu

Bạn cần bearer token (xem API guide) của user có quyền đọc device log (`edc.device.log`).

### Yêu cầu

```http
GET /base/device/log?device_serial={serial}&level={level}&tag={tag}&limit={limit}&offset={offset}
Authorization: Bearer <token>
```

| Tên | Kiểu | Bắt buộc | Ý nghĩa |
| --- | --- | --- | --- |
| `device_serial` | string | Không | Một phần của serial, không phân biệt hoa thường, ví dụ `sn00`. Lưu ý tên snake_case. |
| `level` | string | Không | Mức log chính xác, phân biệt hoa thường, ví dụ `ERROR`. |
| `tag` | string | Không | Một phần của tag, không phân biệt hoa thường, ví dụ `payment`. File upload không có tag dùng `file`. |
| `severity` | string | Không | Tên cũ của `level`. Chỉ dùng khi `level` rỗng. |
| `limit` | integer | Không | Số dòng mỗi trang, ví dụ `20`. Mặc định `50`, tối đa `500`. Giá trị 0, lớn hơn 500 hoặc không phải số sẽ thành `50`. |
| `offset` | integer | Không | Số dòng bỏ qua, ví dụ `40`. Mặc định `0`. |

Filter để trống sẽ bị bỏ qua. Kết quả sắp xếp theo `loggedAt` (mới nhất trước), rồi `createdAt`.

### Ví dụ

```bash
curl "$BASE_URL/base/device/log?device_serial=SN0001&level=ERROR&limit=20" \
  -H "Authorization: Bearer $TOKEN"
```

### Phản hồi

`200 OK`.

```json
{
  "id": "111020261535481234570001",
  "result": {
    "items": [
      {
        "id": 311,
        "deviceSerial": "SN0001",
        "level": "ERROR",
        "tag": "payment",
        "message": "uploaded device log file edc-20260926.zip",
        "metadata": { "appVersion": "1.3.2" },
        "originalFileName": "edc-20260926.zip",
        "storedFileName": "20260926T080000Z_edc-20260926.zip",
        "fileExtension": ".zip",
        "contentType": "application/zip",
        "compressionType": "zip",
        "fileSize": 48213,
        "storageProvider": "attachment",
        "storagePath": "device-logs/SN0001/2026/09/26/20260926T080000Z_edc-20260926.zip",
        "storageUrl": "/web/content/5120",
        "bankBincode": "970436",
        "loggedAt": "2026-09-26T07:59:30Z",
        "createdAt": "2026-09-26T08:00:00Z"
      }
    ],
    "total": 1
  }
}
```

| Field | Ý nghĩa |
| --- | --- |
| `items` | Các log trong trang này. Mỗi item có các field giống *Get Device Log*. |
| `total` | Tổng số log khớp filter (không phụ thuộc `limit` và `offset`). |

Các field tùy chọn rỗng sẽ bị bỏ. Item không có key `severity`; hãy đọc `level`.

### Lỗi

Lỗi dùng định dạng chuẩn được mô tả trong API guide (xem "Response" → "Errors").

| HTTP | `error.code` | Ý nghĩa | Bạn nên làm gì |
| --- | --- | --- | --- |
| `401` | `-32001` | Thiếu hoặc sai token. | Lấy token mới. |
| `403` | `-32003` | User không có quyền đọc device log. | Nhờ admin cấp quyền. |
| `500` | `-32603` | Lỗi server không mong đợi. | Thử lại sau và báo `id` của response. |

### Cần biết

- Phân trang bằng `offset = (page - 1) * limit` và dừng khi `offset >= total`.
- File được lưu dưới dạng attachment của Base HUB (`storageProvider: "attachment"`). Tải file từ `storageUrl`, tương đối so với host Base HUB.
- `storagePath` chỉ là nhãn, không phải đường dẫn thật trên đĩa.
