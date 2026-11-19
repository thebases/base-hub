## List Devices

Trả về một trang các thiết bị đã đăng ký, mới nhất trước. Dùng cho bảng thiết bị trong giao diện admin, với các filter tùy chọn.

### Trước khi bắt đầu

Bạn cần bearer token (xem API guide) của user có quyền đọc `edc.device`.

### Yêu cầu

```http
GET /base/devices?limit={limit}&offset={offset}&serial={serial}&owner_id={owner_id}&active={active}
Authorization: Bearer <token>
```

| Tên | Kiểu | Bắt buộc | Ý nghĩa |
| --- | --- | --- | --- |
| `serial` | string | Không | Một phần của serial, không phân biệt hoa thường. `SN00` khớp `SN0001`. |
| `owner_id` | integer | Không | Id owner chính xác, ví dụ `10`. Bỏ qua nếu không phải số. |
| `active` | boolean | Không | `true` hoặc `false` (cũng nhận `1`/`0`, `t`/`f`). Bỏ qua nếu không phải boolean. |
| `limit` | integer | Không | Số dòng mỗi trang, ví dụ `20`. Mặc định `50`, tối đa `500`. Giá trị `<= 0`, `> 500` hoặc không phải số sẽ thành `50`. |
| `offset` | integer | Không | Số dòng bỏ qua. Mặc định `0`. |

### Ví dụ

```bash
curl "$BASE_URL/base/devices?limit=20&offset=0&serial=SN00&active=true" \
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

| Field | Ý nghĩa |
| --- | --- |
| `items` | Các thiết bị trong trang này, mới nhất trước. |
| `total` | Tổng số thiết bị khớp filter (không phụ thuộc `limit` và `offset`). |
| `provisioningStatus` | Terminal đang ở đâu trong quá trình thiết lập lần đầu, ví dụ `PENDING` hoặc `ACTIVE`. |

`imei`, `groupId`, `mqttClientId`, `lastProvisionedAt` và `revokedAt` bị bỏ khi rỗng. Device secret không bao giờ được trả về. Thiết bị inactive vẫn có trong kết quả trừ khi bạn gửi `active=true`.

### Lỗi

Lỗi dùng định dạng chuẩn được mô tả trong API guide (xem "Response" → "Errors").

| HTTP | `error.code` | Ý nghĩa | Bạn nên làm gì |
| --- | --- | --- | --- |
| `401` | `-32001` | Thiếu hoặc sai token. | Lấy token mới. |
| `403` | `-32003` | User không có quyền đọc `edc.device`. | Nhờ admin cấp quyền. |
| `500` | `-32603` | Lỗi server không mong đợi. | Thử lại sau và báo `id` của response. |

### Cần biết

- Phân trang bằng `offset = (page - 1) * limit` và dừng khi `offset >= total`.
- Khi không có kết quả, `items` là `[]` và `total` là `0`.
- Yêu cầu hơn 500 dòng không bị giới hạn ở 500. Nó âm thầm trả 50.
- Ở đây không có filter `groupId`. Dùng `GET /base/groups/{id}/devices` cho một group.

---

## List Devices in Group

Trả về các thiết bị thuộc một group, có phân trang. Dùng để hiển thị thành viên của group.

### Yêu cầu

```http
GET /base/groups/{id}/devices?limit={limit}&offset={offset}
Authorization: Bearer <token>
```

Bạn cần user có quyền đọc `edc.device`.

| Tên | Kiểu | Bắt buộc | Ý nghĩa |
| --- | --- | --- | --- |
| `id` | integer (path) | Có | Id của group, ví dụ `3`. |
| `limit` | integer | Không | Số dòng mỗi trang từ `1` đến `500`, mặc định `50`. Giá trị khác thành `50`. |
| `offset` | integer | Không | Số dòng bỏ qua, mặc định `0`. |

### Ví dụ

```bash
curl "$BASE_URL/base/groups/3/devices?limit=50&offset=0" \
  -H "Authorization: Bearer $TOKEN"
```

### Phản hồi

`200 OK`. Thiết bị sắp xếp theo `id` tăng dần, và thiết bị inactive vẫn có trong kết quả.

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
        "groupId": 3,
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

Mỗi item có các field giống *List Devices*. `id` của nó là `memberID` dùng trong `DELETE /base/groups/{id}/devices/{memberID}`. `total` là số thiết bị trong group trước khi phân trang.

### Lỗi

Lỗi dùng định dạng chuẩn được mô tả trong API guide (xem "Response" → "Errors").

| HTTP | `error.code` | Ý nghĩa | Bạn nên làm gì |
| --- | --- | --- | --- |
| `400` | `-32602` | `id` không phải số. | Gửi id là số nguyên. |
| `401` | `-32001` | Thiếu hoặc sai token. | Lấy token mới. |
| `403` | `-32003` | User không có quyền đọc `edc.device`. | Nhờ admin cấp quyền. |
| `500` | `-32603` | Lỗi server không mong đợi. | Thử lại sau và báo `id` của response. |

### Cần biết

- Group không tồn tại trả `200` với `items: []` và `total: 0`, không phải `404`.
- Một thiết bị thuộc tối đa một group (`groupId` của chính nó). Không có bản ghi thành viên riêng.

---

## Call as RPC method: `edc.device.search_read`

Dùng method tìm kiếm chuẩn từ code phía server. Nó đọc trực tiếp bảng thiết bị.

```http
POST /rpc/edc.device/search_read
Authorization: Bearer <token>
Content-Type: application/json
```

Không có path id. Gửi `args` là `[]` và đặt các tùy chọn trong `kwargs`.

| Key trong `kwargs` | Kiểu | Bắt buộc | Ý nghĩa |
| --- | --- | --- | --- |
| `domain` | array | Không | Filter dạng danh sách `[field, operator, value]`, ví dụ `[["serial", "ilike", "SN00"], ["active", "=", true]]`. API guide còn mô tả object `filter`. |
| `fields` | array of strings | Không | Tên field cần trả về, ví dụ `["id", "serial"]`. |
| `limit` | integer | Không | Số dòng tối đa, ví dụ `20`. |
| `offset` | integer | Không | Số dòng bỏ qua. |
| `order` | string | Không | Sắp xếp, ví dụ `created_at desc`. |

```bash
curl -X POST "$BASE_URL/rpc/edc.device/search_read" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"args": [], "kwargs": {"domain": [["serial", "ilike", "SN00"]], "limit": 20, "offset": 0, "order": "created_at desc"}}'
```

`result` là danh sách bản ghi với tên field snake_case (không phải `{items, total}`). Dùng `search_count` với cùng `domain` để lấy tổng. Nếu bị từ chối đọc thì trả `200` với `[]`.
