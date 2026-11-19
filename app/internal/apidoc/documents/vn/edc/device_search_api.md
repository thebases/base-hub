## Search Devices

Tìm thiết bị theo serial và phân trang theo số trang. Kết quả giống `GET /base/devices?serial=...`, dành cho client thích dùng JSON body với `pageNum` và `pageSize`.

### Trước khi bắt đầu

Bạn cần bearer token (token đăng nhập, gửi trong header `Authorization`) của user có quyền đọc thiết bị. Không có quyền sẽ nhận `403`.

### Request

```http
POST /base/devices/search
Authorization: Bearer <token>
Content-Type: application/json
```

| Tên | Kiểu | Bắt buộc | Ý nghĩa |
| --- | --- | --- | --- |
| `value` | string | Không | Tìm serial chứa chuỗi này, không phân biệt hoa thường, ví dụ `SN00`. Khoảng trắng bị cắt. Cũng nhận tên `serial`. Để trống sẽ trả tất cả thiết bị. |
| `pageSize` | int | Không | Số dòng mỗi trang, ví dụ `20`. Mặc định `50`, tối đa `500`. Giá trị `<= 0`, lớn hơn `500` hoặc thiếu sẽ thành `50`. Cũng nhận `page_size` và `limit`. |
| `pageNum` | int | Không | Số trang, bắt đầu từ `1`. Mặc định `1`; giá trị `<= 0` thành `1`. Cũng nhận `page_num` và `page`. |

Body rỗng vẫn hợp lệ: dùng toàn bộ giá trị mặc định.

### Ví dụ

```bash
curl -X POST "$BASE_URL/base/devices/search" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"value":"SN00","pageSize":20,"pageNum":1}'
```

### Response

HTTP `200`.

```json
{
  "id": "111020261535481234570001",
  "result": {
    "items": [
      {
        "id": 42,
        "serial": "SN0001",
        "imei": "356938035643809",
        "ownerId": 10,
        "holderId": 25,
        "groupId": 1,
        "active": true,
        "provisioningStatus": "PENDING",
        "secretVersion": 0,
        "createdAt": "2026-09-26T08:00:00Z",
        "updatedAt": "2026-09-26T08:00:00Z"
      }
    ],
    "total": 1
  }
}
```

| Trường | Ý nghĩa |
| --- | --- |
| `items` | Thiết bị trong trang này, mới nhất trước, gồm cả thiết bị đã tắt. Là `[]` khi không có kết quả. |
| `total` | Tổng số thiết bị khớp trên mọi trang. Là `0` khi không có kết quả. |

Mỗi item có các trường giống *Get Device*.

### Lỗi

Lỗi dùng định dạng chuẩn được mô tả trong API guide (xem "Response" → "Errors").

| HTTP | `error.code` | Ý nghĩa | Bạn nên làm gì |
| --- | --- | --- | --- |
| `400` | `-32700` | Body không phải JSON hợp lệ. | Sửa lại JSON. |
| `401` | `-32001` | Thiếu token hoặc token không hợp lệ. | Đăng nhập lại rồi gọi lại. |
| `403` | `-32003` | User không có quyền đọc thiết bị. | Nhờ admin cấp quyền. |
| `500` | `-32603` | Lỗi server không mong đợi. | Thử lại sau; báo kèm `id` của response. |

### Cần biết

- Chỉ tìm theo serial. Muốn lọc theo owner hoặc `active`, dùng `GET /base/devices`.
- Các khóa lọc khác của service cũ (`groupId`, `configurationId`, `sortBy`, bộ lọc ngày) bị bỏ qua.
- Cũng có route cũ `POST /base/edc/devices/search` với các khóa tương tự nằm trong `kwargs`. Các item của route này dùng tên snake_case, và user không có quyền đọc nhận `200` với kết quả rỗng thay vì `403`.
