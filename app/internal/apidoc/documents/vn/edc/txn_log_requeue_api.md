## Requeue Failed Transaction Log

Đặt lại một log callback result hoặc settle bị lỗi về `PENDING`, để job thử lại của server xử lý nó một lần nữa. Dùng sau khi bạn đã sửa nguyên nhân khiến log bị `FAILED`. Không có gì được xử lý ngay lập tức.

### Before you start

- Một bearer token của Base HUB.
- Quyền ghi `edc.txn.log`.
- `id` của một log `FAILED` (tìm bằng "Search Transaction Callback Logs").

### Request

```http
POST /base/edc/transaction/logs/{id}/requeue
Authorization: Bearer <token>
Content-Type: application/json
```

Body là RPC call envelope và không có tham số (gửi `{}`). Dạng RPC: `POST /rpc/edc.txn.log/requeue/{id}`.

| Name | Where | Type | Required | Ý nghĩa |
| --- | --- | --- | --- | --- |
| `id` | path | integer | Yes | ID log, ví dụ `5501`. Đúng một id. |

### Example

```bash
curl -X POST "$BASE_URL/base/edc/transaction/logs/5501/requeue" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{}'
```

### Response

HTTP `200`.

```json
{
  "id": "111020261535481234570001",
  "result": { "id": 5501, "requeued": true }
}
```

`requeued` là `true` khi log đã được đặt lại. Là `false` khi không có gì thay đổi (đây không phải lỗi).

### Errors

Lỗi dùng định dạng chuẩn mô tả trong API guide (xem "Response" → "Errors").

| HTTP | `error.message` (hoặc nguyên nhân) | Ý nghĩa | Bạn nên làm gì |
| --- | --- | --- | --- |
| `401` | không có | Thiếu hoặc sai token. | Lấy token mới. |
| `403` | `Permission denied: ...` | Bạn không có quyền ghi `edc.txn.log`. | Nhờ admin cấp quyền. |
| `400` | `edc.txn.log requeue requires exactly one id` | Path chứa nhiều id. | Gửi một id. |
| `400` | `path id "..." must be a positive integer` | Id không phải số dương. | Sửa id. |
| `400` | `edc.txn.log requeue: ...` | Lỗi database. | Thử lại sau. |

### Good to know

- Chỉ dòng có `parse_status` = `FAILED` và `log_type` = `EDC_RESULT_REQUEST` hoặc `EDC_SETTLE_REQUEST` mới bị đổi. Dòng khác, hoặc id không tồn tại, được để nguyên và trả về `requeued: false`.
- Khi thành công `parse_status` thành `PENDING` và `locked_at` được xóa.
- Gọi lặp lại là an toàn.
