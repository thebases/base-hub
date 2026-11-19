## Search Settlements

Liệt kê các settlement, mới nhất trước. **Settlement** (quyết toán) là tổng kết cuối lô của ngân hàng cho một lô của terminal (bao nhiêu giao dịch bán, bao nhiêu void, bao nhiêu tiền). Mỗi lần terminal gửi "EDC Batch Settle" sẽ tạo một dòng.

### Before you start

- Một bearer token của Base HUB.
- Quyền đọc `edc.settlement.journal`.

### Request

```http
POST /base/settlement/search
Authorization: Bearer <token>
Content-Type: application/json
```

Bộ lọc đặt ở cấp ngoài cùng của body **hoặc** trong object `filters` (camelCase, hoặc snake_case `settle_id` / `batch_no`). Body là tùy chọn. Body không rỗng mà không phải JSON object sẽ trả về `400`.

| Body field | Type | Required | Ý nghĩa |
| --- | --- | --- | --- |
| `settleId` | string | No | Khớp một phần, không phân biệt hoa thường. `SN0001` tìm mọi lô của terminal đó. |
| `batchNo` | string | No | Số lô khớp chính xác, ví dụ `000012`. |
| `tid` | string | No | Terminal ID khớp chính xác, ví dụ `70010001`. |
| `mid` | string | No | Merchant ID khớp chính xác, ví dụ `000000012345678`. |
| `limit` | integer | No | Mặc định `50`. Giá trị `<= 0` hoặc `> 500` thành `50`. |
| `offset` | integer | No | Mặc định `0`. |

### Example

```bash
curl -X POST "$BASE_URL/base/settlement/search" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"settleId":"SN0001","limit":20}'
```

### Response

HTTP `200`. Chú ý chữ hoa trong `Total` và `Settlements`.

```json
{
  "id": "111020261535481234570001",
  "result": {
    "Total": 1,
    "Settlements": [
      {
        "id": 88,
        "reqId": "STL20260926000012",
        "billId": "000012",
        "settleId": "SN0001:000012:STL20260926000012",
        "settleDate": "2026-09-26T15:00:00Z",
        "batchNo": "000012",
        "txnQuant": 25,
        "txnTotal": 3750000,
        "saleQuant": 26,
        "saleAmount": 3900000,
        "voidQuant": 1,
        "voidAmount": 150000,
        "tid": "70010001",
        "mid": "000000012345678",
        "aliasValue": "BANKAPP",
        "de37RefCode": "",
        "de39AuthCode": "00",
        "createdAt": "2026-09-26T15:00:01.310Z",
        "updatedAt": "2026-09-26T15:00:01.310Z"
      }
    ]
  }
}
```

| Field | Ý nghĩa |
| --- | --- |
| `Total` | Số dòng khớp bộ lọc. Không phụ thuộc `limit` và `offset`. |
| `Settlements` | Trang các settlement. Các field được giải thích ở "Get Settlement". |

### Errors

Lỗi dùng định dạng chuẩn mô tả trong API guide (xem "Response" → "Errors").

| HTTP | `error.code` | Ý nghĩa | Bạn nên làm gì |
| --- | --- | --- | --- |
| `400` | `-32602` | Body không phải JSON object hợp lệ. | Sửa body. |
| `401` | `-32001` | Thiếu hoặc sai token. | Lấy token mới. |
| `403` | `-32003` | Bạn không có quyền đọc model này. | Nhờ admin cấp quyền. |
| `500` | `-32603` | Lỗi database. | Thử lại sau. |

### Good to know

- Số tiền là số nguyên theo đơn vị tiền nguyên (VND). `txnTotal` và `txnQuant` là tổng của ngân hàng như terminal báo cáo. Server không tính lại chúng từ các giao dịch.
- `de39AuthCode` là mã phản hồi settle của ngân hàng (`00` = thành công).
- Để liệt kê các giao dịch trong một lô, search giao dịch và khớp `settleId` hoặc `batchNo`.
- Sắp xếp theo `id`, mới nhất trước.
- Các field đối chiếu (`edcSerial`, `reconcileStatus`, tổng tính toán) không có trong response này. Đọc chúng qua `/rpc` trên `edc.settlement.journal`.
- Cũng gọi được bằng `POST /base/edc/settlement/search` (RPC: `POST /rpc/edc.settlement.journal/settle_search`). Gửi bộ lọc trong `kwargs`. Phản hồi dùng `total` / `settlements` viết thường và các dòng snake_case.
