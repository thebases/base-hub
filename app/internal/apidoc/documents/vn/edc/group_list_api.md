## Liệt kê nhóm (Group)

Trả về các nhóm thiết bị, theo từng trang. Dùng để điền danh sách chọn nhóm hoặc bảng nhóm, có thể lọc theo tên hoặc khách hàng.

### Trước khi bắt đầu

Bạn cần bearer token của user có quyền đọc nhóm (kiểm tra quyền, ACL). Không có quyền sẽ nhận `403`.

### Request

```http
GET /base/groups?limit={limit}&offset={offset}&name={name}&customer_id={customer_id}
Authorization: Bearer <token>
```

| Tham số query | Kiểu | Bắt buộc | Ý nghĩa |
| --- | --- | --- | --- |
| `limit` | int | Không (mặc định `50`) | Kích thước trang, `1` đến `500`. Giá trị ngoài khoảng này (hoặc không phải số) trở thành `50`. |
| `offset` | int | Không (mặc định `0`) | Số dòng bỏ qua, ví dụ `50` cho trang 2. |
| `name` | string | Không | Khớp "chứa" trong tên, không phân biệt hoa thường, ví dụ `hanoi`. |
| `customer_id` | int | Không | Chỉ các nhóm của khách hàng này, ví dụ `12`. |

### Ví dụ

```bash
curl "$BASE_URL/base/groups?limit=20&offset=0&name=hanoi" \
  -H "Authorization: Bearer $TOKEN"
```

### Response

Các nhóm sắp xếp theo `id`, nhỏ nhất trước.

```json
{
  "id": "111020261535481234570001",
  "result": {
    "items": [
      { "id": 3, "name": "Hanoi Stores", "customerId": 12 },
      { "id": 7, "name": "Hanoi Kiosks" }
    ],
    "total": 2
  }
}
```

| Trường | Ý nghĩa |
| --- | --- |
| `result.items` | Các nhóm trong trang này. Là danh sách rỗng `[]` khi không có kết quả. |
| `result.items[].customerId` | Id khách hàng sở hữu. Bị bỏ khi nhóm không có khách hàng. |
| `result.total` | Số nhóm khớp trước khi phân trang (`0` khi không có). |

### Lỗi

Lỗi dùng định dạng chuẩn được mô tả trong hướng dẫn API (xem "Response" → "Errors").

| HTTP | `error.code` | Ý nghĩa | Bạn nên làm gì |
| --- | --- | --- | --- |
| `401` | `-32001` | Thiếu hoặc sai token. | Lấy token mới. |
| `403` | `-32003` | User không có quyền với nhóm. | Xin admin cấp quyền. |
| `500` | `-32603` | Lỗi server bất ngờ. | Thử lại sau; báo cáo kèm `id` của response. |

### Cần biết

- `customer_id` không phải số sẽ bị bỏ qua âm thầm, nên bạn nhận danh sách chưa lọc. Hãy tự kiểm tra ở phía bạn.
- `limit` lớn hơn `500` không bị cắt xuống `500`. Nó được đặt lại thành `50`.
