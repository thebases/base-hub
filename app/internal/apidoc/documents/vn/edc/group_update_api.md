## Cập nhật nhóm (Group)

Đổi tên nhóm thiết bị và/hoặc đổi khách hàng của nhóm. Chỉ các trường bạn gửi mới bị thay đổi.

### Trước khi bắt đầu

Bạn cần bearer token của user có quyền ghi nhóm (kiểm tra quyền, ACL). Không có quyền sẽ nhận `403`.

### Request

```http
PUT /base/groups/{id}
Authorization: Bearer <token>
Content-Type: application/json
```

| Tham số path | Kiểu | Bắt buộc | Ý nghĩa |
| --- | --- | --- | --- |
| `id` | int | Có | Id nhóm, ví dụ `3`. |

| Trường body | Kiểu | Bắt buộc | Ý nghĩa |
| --- | --- | --- | --- |
| `name` | string | Không | Tên mới, ví dụ `Hanoi Flagship Stores`. Bỏ trống để giữ tên hiện tại. Không bị cắt khoảng trắng hay kiểm tra. |
| `customerId` | int | Không | Id khách hàng mới, ví dụ `12`. Bỏ trống để giữ nguyên. Gửi `null` để gỡ khách hàng. |

### Ví dụ

```bash
curl -X PUT "$BASE_URL/base/groups/3" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"name":"Hanoi Flagship Stores","customerId":12}'
```

### Response

```json
{
  "id": "111020261535481234570001",
  "result": { "id": 3, "name": "Hanoi Flagship Stores", "customerId": 12 }
}
```

`result` là nhóm sau khi cập nhật.

### Lỗi

Lỗi dùng định dạng chuẩn được mô tả trong hướng dẫn API (xem "Response" → "Errors").

| HTTP | `error.code` | Ý nghĩa | Bạn nên làm gì |
| --- | --- | --- | --- |
| `400` | `-32602` | `id` không phải số nguyên. | Gửi một số. |
| `400` | `-32700` | Body không phải JSON hợp lệ. | Sửa JSON. |
| `400` | `-32000` (`data.reason` = `update_failed`) | Không lưu được nhóm, ví dụ `"name": null` không được phép. | Gửi tên thật. |
| `401` | `-32001` | Thiếu hoặc sai token. | Lấy token mới. |
| `403` | `-32003` | User không có quyền ghi nhóm. | Xin admin cấp quyền. |
| `404` | `-32004` | Không có nhóm nào với id này. | Kiểm tra id. |

### Cần biết

- Đây là cập nhật từng phần. Bạn có thể đổi tên mà không gửi `customerId` (dịch vụ bedc cũ xóa `customerId` nếu thiếu; dịch vụ này thì không).
- Đổi tên nhóm làm thay đổi topic lệnh (`base/<group name>/<serial>/cmd`) mà thiết bị của nhóm nhận trong `mqttConfig.mqttDeviceTopic`.
