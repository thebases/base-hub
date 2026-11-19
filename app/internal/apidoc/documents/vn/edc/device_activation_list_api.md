## List Device Activations

Trả về một trang các bản ghi activation (mỗi serial một dòng), mới nhất trước. Dùng để xem thiết bị nào có mã, mã hết hạn khi nào, và đã được dùng chưa.

### Trước khi bắt đầu

Cần bearer token của user có quyền đọc `edc.device.activation`; nếu không sẽ nhận `403`.

### Request

```http
GET /base/device/activation?limit={limit}&offset={offset}&serial={serial}
Authorization: Bearer <token>
```

| Tên | Kiểu | Bắt buộc | Ý nghĩa |
| --- | --- | --- | --- |
| `limit` | int | Không | Kích thước trang. Mặc định `50`, tối đa `500`. Giá trị `<= 0`, lớn hơn `500` hoặc không phải số sẽ thành `50`. |
| `offset` | int | Không | Số dòng bỏ qua, ví dụ `50` cho trang 2. Mặc định `0`. |
| `serial` | string | Không | Khớp chính xác, phân biệt hoa thường. Serial được lưu chữ hoa, nên gửi `SN0001`, không phải `sn0001`. |

### Ví dụ

```bash
curl "$BASE_URL/base/device/activation?serial=SN0001" \
  -H "Authorization: Bearer $TOKEN"
```

### Response

`200`:

```json
{
  "id": "111020261535481234570001",
  "result": {
    "items": [
      {
        "id": 7,
        "serial": "SN0001",
        "expiresAt": "2026-09-27T08:00:00Z",
        "usedAt": "2026-09-26T08:05:00Z",
        "createdAt": "2026-09-26T08:00:00Z",
        "updatedAt": "2026-09-26T08:05:00Z"
      }
    ],
    "total": 1
  }
}
```

| Trường | Ý nghĩa |
| --- | --- |
| `result.items` | Các bản ghi activation trong trang này. Mảng rỗng nếu không có gì khớp. |
| `result.total` | Tổng số bản ghi khớp (không chỉ trong trang này). |
| `items[].usedAt` | Không có cho đến khi máy provision bằng mã. |

Mã kích hoạt không bao giờ được trả lại.

### Lỗi

Lỗi dùng định dạng chuẩn được mô tả trong hướng dẫn API (xem "Response" → "Errors").

| HTTP | `error.code` | Ý nghĩa | Bạn nên làm gì |
| --- | --- | --- | --- |
| `401` | `-32001` | Thiếu token hoặc token không hợp lệ. | Lấy token mới. |
| `403` | `-32003` | User không được đọc model này. | Nhờ admin cấp quyền. |
| `500` | `-32603` | Lỗi server không mong đợi. | Thử lại sau và báo kèm `id` của phản hồi. |

### Cần biết

- Dùng `items[].id` cho `GET`, `PUT`, `DELETE /base/device/activation/{id}`.
- Mã chỉ dùng được khi không có `usedAt` và `expiresAt` còn ở tương lai.
