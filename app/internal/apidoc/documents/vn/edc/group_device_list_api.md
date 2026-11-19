## Liệt kê thiết bị trong nhóm

Trả về các thiết bị thuộc một nhóm, theo từng trang. Dùng để hiển thị thành viên của nhóm.

### Trước khi bắt đầu

Bạn cần bearer token của user có quyền đọc thiết bị (kiểm tra quyền, ACL). Không có quyền sẽ nhận `403`.

### Request

```http
GET /base/groups/{id}/devices?limit={limit}&offset={offset}
Authorization: Bearer <token>
```

| Tham số path | Kiểu | Bắt buộc | Ý nghĩa |
| --- | --- | --- | --- |
| `id` | int | Có | Id nhóm, ví dụ `3`. |

| Tham số query | Kiểu | Bắt buộc | Ý nghĩa |
| --- | --- | --- | --- |
| `limit` | int | Không (mặc định `50`) | Kích thước trang, `1` đến `500`. Giá trị ngoài khoảng này trở thành `50`. |
| `offset` | int | Không (mặc định `0`) | Số dòng bỏ qua, ví dụ `50` cho trang 2. |

### Ví dụ

```bash
curl "$BASE_URL/base/groups/3/devices?limit=50&offset=0" \
  -H "Authorization: Bearer $TOKEN"
```

### Response

Thiết bị sắp xếp theo `id`, nhỏ nhất trước. Thiết bị không hoạt động cũng được tính.

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

| Trường | Ý nghĩa |
| --- | --- |
| `result.items` | Các thiết bị trong trang này. Mỗi phần tử là một đối tượng thiết bị đầy đủ, giống *List Devices*. |
| `result.items[].id` | Id thiết bị. Dùng làm `memberID` trong `DELETE /base/groups/{id}/devices/{memberID}`. |
| `result.total` | Số thiết bị trong nhóm trước khi phân trang. |

`imei`, `mqttClientId`, `lastProvisionedAt` và `revokedAt` bị bỏ khi rỗng.

### Lỗi

Lỗi dùng định dạng chuẩn được mô tả trong hướng dẫn API (xem "Response" → "Errors").

| HTTP | `error.code` | Ý nghĩa | Bạn nên làm gì |
| --- | --- | --- | --- |
| `400` | `-32602` | `id` không phải số nguyên. | Gửi một số. |
| `401` | `-32001` | Thiếu hoặc sai token. | Lấy token mới. |
| `403` | `-32003` | User không có quyền với thiết bị. | Xin admin cấp quyền. |
| `500` | `-32603` | Lỗi server bất ngờ. | Thử lại sau; báo cáo kèm `id` của response. |

### Cần biết

- Id nhóm không tồn tại cho `200` với `items: []` và `total: 0`, không phải `404`.
- Thiết bị thuộc nhóm qua `groupId` của chính nó. Không có bản ghi thành viên riêng.
