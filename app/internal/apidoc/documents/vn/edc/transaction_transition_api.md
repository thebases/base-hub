## Transition Transaction Status

Chuyển một giao dịch sang status mới, ví dụ `INITIATED` sang `PENDING_DEVICE` sau khi lệnh bắt đầu được gửi tới terminal. Dùng từ công cụ của operator hoặc từ một hệ thống tích hợp. Server chỉ cho phép các bước chuyển hợp lệ và ghi một dòng audit cho mỗi thay đổi.

### Transaction statuses in plain words

| Status | Ý nghĩa |
| --- | --- |
| `INITIATED` | Giao dịch đã bắt đầu; chưa có phản hồi. |
| `PENDING_DEVICE` | Lệnh bắt đầu đã gửi tới terminal; server đang chờ nó. |
| `PROCESSING` | Terminal đang xử lý giao dịch. |
| `APPROVED` | Ngân hàng đã chấp nhận giao dịch. |
| `DECLINED` / `FAILED` | Ngân hàng từ chối, hoặc giao dịch thất bại. |
| `TIMEOUT` | Không có phản hồi kịp thời. |
| `REVERSED` | Giao dịch bán đã duyệt bị hủy bằng void. |
| `SETTLED` | Lô chứa giao dịch này đã được chốt với ngân hàng (một **settlement**). |

### Before you start

- Một bearer token của Base HUB.
- Quyền ghi `edc.transaction`.
- `id` dạng số của bản ghi giao dịch (không phải chuỗi `transactionId`).

### Request

```http
POST /base/edc/transactions/{id}/transition
Authorization: Bearer <token>
Content-Type: application/json
```

Body là RPC call envelope: đặt tham số trong `kwargs`. Dạng RPC: `POST /rpc/edc.transaction/transition/{id}`.

| Name | Where | Type | Required | Ý nghĩa |
| --- | --- | --- | --- | --- |
| `id` | path | integer | Yes | ID dòng giao dịch dạng số, ví dụ `1001`. |
| `kwargs.status` | body | string | Yes | Status mới: `INITIATED`, `PENDING_DEVICE`, `PROCESSING`, `APPROVED`, `DECLINED`, `FAILED`, `TIMEOUT`, `REVERSED` hoặc `SETTLED`. |
| `kwargs.reason` | body | string | No | Lý do đổi (lưu trong audit log). Mặc định `manual transition`. |
| `kwargs.result_code` | body | string | No | Lưu vào giao dịch khi không rỗng. |
| `kwargs.result_message` | body | string | No | Lưu vào giao dịch khi không rỗng. |

Các bước chuyển được phép:

| Từ | Sang |
| --- | --- |
| `INITIATED` | `PENDING_DEVICE`, `PROCESSING`, `APPROVED`, `DECLINED`, `FAILED`, `TIMEOUT` |
| `PENDING_DEVICE` | `PROCESSING`, `APPROVED`, `DECLINED`, `FAILED`, `TIMEOUT` |
| `PROCESSING` | `APPROVED`, `DECLINED`, `FAILED`, `TIMEOUT` |
| `APPROVED` | `REVERSED`, `SETTLED` |
| `TIMEOUT` do job quét timeout của server đặt | `APPROVED`, `DECLINED`, `FAILED` |

Mọi bước chuyển khác đều bị từ chối.

### Example

```bash
curl -X POST "$BASE_URL/base/edc/transactions/1001/transition" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"args": [], "kwargs": {"status": "PENDING_DEVICE", "reason": "start command published"}}'
```

### Response

HTTP `200`.

```json
{
  "id": "111020261535481234570001",
  "result": {
    "id": 1001,
    "transaction_id": "TXN-20261011-0001",
    "from": "INITIATED",
    "status": "PENDING_DEVICE",
    "changed": true
  }
}
```

| Field | Ý nghĩa |
| --- | --- |
| `from` | Status trước lệnh gọi. |
| `status` | Status hiện tại. |
| `changed` | `true` nếu status đã đổi. `false` nếu giao dịch đã ở status đó (không làm gì, vẫn `200`). |

### Errors

Lỗi dùng định dạng chuẩn mô tả trong API guide (xem "Response" → "Errors").

| HTTP | `error.message` (hoặc nguyên nhân) | Ý nghĩa | Bạn nên làm gì |
| --- | --- | --- | --- |
| `401` | không có | Thiếu hoặc sai token. | Lấy token mới. |
| `403` | `Permission denied: ...` | Bạn không có quyền ghi `edc.transaction`. | Nhờ admin cấp quyền. |
| `400` | `edc.transaction transition requires exactly one id` | Path chứa nhiều id. | Gửi đúng một id. |
| `400` | `edc.transaction transition: status is required` | Thiếu `status` hoặc để trống. | Gửi `kwargs.status`. |
| `400` | `path id "..." must be a positive integer` | Id trong path không phải số dương. | Sửa id. |
| `400` | `edc.transaction <id> not found` | Không có giao dịch nào có id này. | Kiểm tra id. |
| `400` | `... illegal status transition X -> Y` | Bước chuyển không được phép, status không biết, hoặc có thay đổi khác xảy ra cùng lúc. | Tải lại giao dịch và chọn bước chuyển hợp lệ. |

> **Check:** `error.code` chính xác của các dòng `400` (và việc "not found" có được báo là `404` hay không) chưa được xác nhận trong code; hãy dựa vào HTTP status và `error.message`.

### Good to know

- Khi giao dịch lần đầu đạt status cuối (`APPROVED`, `DECLINED`, `FAILED`, `TIMEOUT`, `REVERSED`, `SETTLED`), `completed_at` được đặt.
- Mỗi thay đổi thêm một dòng với `reason` của bạn vào state log của giao dịch.
- Lệnh này chỉ đổi status. Nó không chuyển tiền và không tạo settlement.
