## List Devices

Trả về một trang thiết bị đã đăng ký, mới nhất trước. Dùng cho bảng thiết bị trong màn hình admin, có thể kèm bộ lọc.

### Trước khi bắt đầu

Bạn cần bearer token (token đăng nhập, gửi trong header `Authorization`) của user có quyền đọc thiết bị. Không có quyền sẽ nhận `403`.

### Request

```http
GET /base/devices?limit={limit}&offset={offset}&serial={serial}&owner_id={owner_id}&active={active}
Authorization: Bearer <token>
```

| Tên | Kiểu | Bắt buộc | Ý nghĩa |
| --- | --- | --- | --- |
| `limit` | int | Không | Số dòng mỗi trang, ví dụ `20`. Mặc định `50`, tối đa `500`. Giá trị `<= 0`, lớn hơn `500` hoặc không phải số sẽ thành `50` (không bị giới hạn ở 500). |
| `offset` | int | Không | Số dòng bỏ qua, ví dụ `40`. Mặc định `0`. |
| `serial` | string | Không | Tìm serial chứa chuỗi này, không phân biệt hoa thường. `SN00` khớp `SN0001`. |
| `owner_id` | int | Không | ID chủ sở hữu chính xác, ví dụ `10`. Bị bỏ qua nếu không phải số. |
| `active` | bool | Không | `true` hoặc `false` (cũng nhận `1`/`0`, `t`/`f`). Bị bỏ qua nếu không phải boolean. |

### Ví dụ

```bash
curl "$BASE_URL/base/devices?limit=20&offset=0&serial=SN00&active=true" \
  -H "Authorization: Bearer $TOKEN"
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
        "provisioningStatus": "ACTIVE",
        "mqttClientId": "SN0001",
        "secretVersion": 1,
        "lastProvisionedAt": "2026-09-26T08:05:00Z",
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
| `items` | Các thiết bị trong trang này, mới nhất trước. Là `[]` khi không có kết quả. |
| `total` | Tổng số thiết bị khớp bộ lọc trên mọi trang (không phụ thuộc `limit` và `offset`). Là `0` khi không có kết quả. |

Mỗi item có các trường giống *Get Device* (`imei`, `groupId`, `mqttClientId`, `lastProvisionedAt` và `revokedAt` bị bỏ khi rỗng). Khóa bí mật của thiết bị không bao giờ được trả về.

### Lỗi

Lỗi dùng định dạng chuẩn được mô tả trong API guide (xem "Response" → "Errors").

| HTTP | `error.code` | Ý nghĩa | Bạn nên làm gì |
| --- | --- | --- | --- |
| `401` | `-32001` | Thiếu token hoặc token không hợp lệ. | Đăng nhập lại rồi gọi lại. |
| `403` | `-32003` | User không có quyền đọc thiết bị. | Nhờ admin cấp quyền. |
| `500` | `-32603` | Lỗi server không mong đợi. | Thử lại sau; báo kèm `id` của response. |

### Cần biết

- Thiết bị đã tắt (`active=false`) vẫn có trong kết quả, trừ khi bạn gửi `active=true`.
- Để phân trang dùng `offset = (page - 1) * limit` và dừng khi `offset >= total`.
- Gửi `limit=1000` không trả 500 dòng. Server âm thầm trả 50 dòng.
- Không có bộ lọc `groupId` ở đây. Để xem thiết bị của một nhóm hãy gọi `GET /base/groups/{id}/devices`.
