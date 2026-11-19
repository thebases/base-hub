## Delete Settlement

Xóa vĩnh viễn một settlement (tổng kết cuối lô của ngân hàng cho một terminal). Chỉ dùng để operator dọn dẹp dữ liệu.

### Before you start

- Một bearer token của Base HUB.
- Quyền xóa `edc.settlement.journal`.

### Request

```http
DELETE /base/settlement/{id}
Authorization: Bearer <token>
```

| Path parameter | Type | Required | Ý nghĩa |
| --- | --- | --- | --- |
| `id` | integer | Yes | ID settlement, ví dụ `88`. |

### Example

```bash
curl -X DELETE "$BASE_URL/base/settlement/88" \
  -H "Authorization: Bearer $TOKEN"
```

### Response

HTTP `200`.

```json
{
  "id": "111020261535481234570001",
  "result": { "deleted": true, "id": 88 }
}
```

### Errors

Lỗi dùng định dạng chuẩn mô tả trong API guide (xem "Response" → "Errors").

| HTTP | `error.code` | Ý nghĩa | Bạn nên làm gì |
| --- | --- | --- | --- |
| `400` | `-32602` | `id` không phải số nguyên. | Gửi một số. |
| `401` | `-32001` | Thiếu hoặc sai token. | Lấy token mới. |
| `403` | `-32003` | Bạn không có quyền xóa model này. | Nhờ admin cấp quyền. |
| `404` | `-32004` | Không có settlement nào có ID này. | Không có gì để xóa (hoặc đã xóa rồi). |
| `500` | `-32603` | Lỗi database. | Thử lại sau. |

### Good to know

- Đây là xóa cứng, không hoàn tác được. Xóa hai lần thì lần thứ hai trả về `404`.
- Các giao dịch của lô đó không bị đổi. Chúng vẫn giữ `settleId`, và giờ nó trỏ tới một settlement không còn tồn tại.
