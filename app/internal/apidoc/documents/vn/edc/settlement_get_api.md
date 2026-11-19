## Get Settlement

Trả về một settlement theo ID dạng số. **Settlement** (quyết toán) là tổng kết cuối lô của ngân hàng cho một terminal: "trong lô `000012`, N giao dịch và số tiền này đã được chốt". Dùng khi bạn biết `id` từ settlement search.

### Before you start

- Một bearer token của Base HUB.
- Quyền đọc `edc.settlement.journal`.

### Request

```http
GET /base/settlement/{id}
Authorization: Bearer <token>
```

| Path parameter | Type | Required | Ý nghĩa |
| --- | --- | --- | --- |
| `id` | integer | Yes | ID settlement, ví dụ `88`. Không phải chuỗi `settleId`. |

### Example

```bash
curl "$BASE_URL/base/settlement/88" \
  -H "Authorization: Bearer $TOKEN"
```

### Response

HTTP `200`. Settlement nằm trong `result`.

```json
{
  "id": "111020261535481234570001",
  "result": {
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
}
```

| Field | Ý nghĩa |
| --- | --- |
| `settleId` | Khóa duy nhất: `<serial>:<batchNo>:<transactionId của settle>`. |
| `settleDate`, `batchNo` | Lúc lô được chốt và số lô. |
| `txnQuant`, `txnTotal` | Số lượng và số tiền ròng (bán trừ void) như ngân hàng báo. |
| `saleQuant`, `saleAmount` | Số lượng và tổng tiền các giao dịch bán. |
| `voidQuant`, `voidAmount` | Số lượng và tổng tiền các void. |
| `tid`, `mid`, `aliasValue` | Terminal ID, merchant ID, tên acquirer hoặc merchant. |
| `de39AuthCode` | Mã phản hồi settle của ngân hàng. `00` nghĩa là thành công. |

Số tiền là số nguyên theo đơn vị tiền nguyên (VND).

### Errors

Lỗi dùng định dạng chuẩn mô tả trong API guide (xem "Response" → "Errors").

| HTTP | `error.code` | Ý nghĩa | Bạn nên làm gì |
| --- | --- | --- | --- |
| `400` | `-32602` | `id` không phải số nguyên. | Gửi một số. |
| `401` | `-32001` | Thiếu hoặc sai token. | Lấy token mới. |
| `403` | `-32003` | Bạn không có quyền đọc model này. | Nhờ admin cấp quyền. |
| `404` | `-32004` | Không có settlement nào có ID này. | Search trước để tìm ID. |
| `500` | `-32603` | Lỗi database. | Thử lại sau. |

### Good to know

- Các giao dịch đã settle không nằm trong kết quả. Hãy search giao dịch theo `batchNo` hoặc `settleId`.
- Các field đối chiếu (`edcSerial`, `reconcileStatus`, tổng tính toán) không có trong response này. Đọc chúng qua `/rpc` trên `edc.settlement.journal`.
