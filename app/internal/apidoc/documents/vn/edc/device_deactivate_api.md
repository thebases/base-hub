## Deactivate Device

Tắt thiết bị bằng cách đặt `active` thành `false`. Thiết bị vẫn nằm trong danh sách và có thể bật lại sau. Dùng để chặn máy bị mất hoặc ngừng sử dụng.

### Trước khi bắt đầu

Cần bearer token của user có quyền ghi `edc.device`; nếu không sẽ nhận `403`.

### Request

```http
DELETE /base/devices/{serial}
Authorization: Bearer <token>
```

| Tên | Kiểu | Bắt buộc | Ý nghĩa |
| --- | --- | --- | --- |
| `serial` | string | Có | Serial chính xác của thiết bị, ví dụ `SN0001` (phân biệt hoa thường). |

### Ví dụ

```bash
curl -X DELETE "$BASE_URL/base/devices/SN0001" \
  -H "Authorization: Bearer $TOKEN"
```

### Response

`200`:

```json
{
  "id": "111020261535481234570001",
  "result": { "deleted": true, "id": 42 }
}
```

| Trường | Ý nghĩa |
| --- | --- |
| `result.deleted` | `true` khi lệnh thành công. Thiết bị **không** bị xóa; chỉ bị tắt. |
| `result.id` | ID số của thiết bị. |

### Lỗi

Lỗi dùng định dạng chuẩn được mô tả trong hướng dẫn API (xem "Response" → "Errors").

| HTTP | `error.code` | Ý nghĩa | Bạn nên làm gì |
| --- | --- | --- | --- |
| `401` | `-32001` | Thiếu token hoặc token không hợp lệ. | Lấy token mới. |
| `403` | `-32003` | User không được ghi `edc.device`. | Nhờ admin cấp quyền. |
| `404` | `-32004` | Không có thiết bị với serial này. | Kiểm tra chính tả (serial viết hoa). |

### Cần biết

- Gọi lặp lại an toàn: deactivate thiết bị đã inactive vẫn thành công.
- Không có gì khác thay đổi. Secret, provisioning status, nhóm và config được giữ nguyên. Muốn bật lại, gọi `PUT /base/devices/{serial}` với `{"active": true}`.
- Thiết bị inactive không thể provision, và bearer token của thiết bị bị từ chối.
- Cũng có thể gọi: `POST /base/edc/devices/{id}/deactivate` (ID số của thiết bị). Nó trả về `{"result":{"device_id":42,"active":false,"status":"<trạng thái hiện tại>"}}`.
