## Delete Transaction

Xóa vĩnh viễn một dòng trong transaction journal. Chỉ dùng để operator dọn dẹp dữ liệu.

### Before you start

- Một bearer token của Base HUB.
- Quyền xóa `edc.transaction.journal`.

### Request

```http
DELETE /base/transaction/{id}
Authorization: Bearer <token>
```

| Path parameter | Type | Required | Ý nghĩa |
| --- | --- | --- | --- |
| `id` | integer | Yes | ID journal, ví dụ `1025`. |

### Example

```bash
curl -X DELETE "$BASE_URL/base/transaction/1025" \
  -H "Authorization: Bearer $TOKEN"
```

### Response

HTTP `200`.

```json
{
  "id": "111020261535481234570001",
  "result": { "deleted": true, "id": 1025 }
}
```

`result.deleted` là `true` và `result.id` là ID bạn vừa xóa.

### Errors

Lỗi dùng định dạng chuẩn mô tả trong API guide (xem "Response" → "Errors").

| HTTP | `error.code` | Ý nghĩa | Bạn nên làm gì |
| --- | --- | --- | --- |
| `400` | `-32602` | `id` không phải số nguyên. | Gửi một số. |
| `401` | `-32001` | Thiếu hoặc sai token. | Lấy token mới. |
| `403` | `-32003` | Bạn không có quyền xóa model này. | Nhờ admin cấp quyền. |
| `404` | `-32004` | Không có giao dịch nào có ID này. | Không có gì để xóa (hoặc đã xóa rồi). |
| `500` | `-32603` | Lỗi database. | Thử lại sau. |

### Good to know

- Đây là xóa cứng (hard delete), không hoàn tác được.
- Xóa hai lần: lần thứ hai trả về `404`.
- Chỉ dòng journal bị xóa. Bản ghi giao dịch (`edc.transaction`) không bị đụng tới.
- Các callback thô của terminal vẫn xem được trong `POST /base/transaction/logs/search`.
