## Lấy bank app info

Trả về một cấu hình bank app (TID/MID) theo id. Dùng để điền form sửa.

### Trước khi bắt đầu

- Bạn cần bearer token.
- User của bạn phải có quyền đọc bản ghi `edc.tidmid.config`. Nếu không, bạn nhận `403`.
- Lấy `id` từ "Danh sách bank app info".

### Request

```http
GET /base/bank-app-info/{id}
Authorization: Bearer <token>
```

| Tên | Kiểu | Bắt buộc | Ý nghĩa |
| --- | --- | --- | --- |
| `id` | integer | Có | Id của bank app info (trong path), ví dụ `7`. |

### Ví dụ

```bash
curl "$BASE_URL/base/bank-app-info/7" \
  -H "Authorization: Bearer $TOKEN"
```

### Response

HTTP `200`:

```json
{
  "id": "111020261535481234570001",
  "result": {
    "id": 7,
    "appBankInfo": "BIDV",
    "bincode": "970418",
    "defaultApp": true,
    "encryptType": "HEXHL",
    "environment": "prod",
    "ip": "10.20.30.40",
    "port": "17109",
    "nii": "003",
    "packageName": "com.wdvn.bidv",
    "refNo": "08062026",
    "thirdPartyId": "27981340",
    "tid": "12345678",
    "mid": "000000012345678",
    "createdAt": "2026-09-20T03:15:00Z",
    "updatedAt": "2026-09-25T09:40:12Z"
  }
}
```

| Field | Ý nghĩa |
| --- | --- |
| `id` | Id của bank app info. Dùng trong các route get, update và delete. |
| `appBankInfo`, `bincode`, `encryptType`, `environment`, `ip`, `port`, `nii`, `packageName`, `refNo`, `thirdPartyId`, `tid`, `mid` | Các giá trị đã lưu, cùng ý nghĩa như trong request. |
| `defaultApp` | Chỉ có khi là `true`. |
| `createdAt` / `updatedAt` | Thời điểm bản ghi được tạo và thay đổi lần cuối (UTC, ISO 8601). |

`secret` không bao giờ có trong response. Field null, `""` hoặc `false` bị bỏ.

### Lỗi

Lỗi dùng định dạng chuẩn được mô tả trong API guide (xem "Response" → "Errors").

| HTTP | `error.code` | Ý nghĩa | Bạn nên làm gì |
| --- | --- | --- | --- |
| `400` | `-32602` | `id` không phải số. | Gửi id là số nguyên. |
| `401` | `-32001` | Thiếu token hoặc token không hợp lệ. | Lấy token mới. |
| `403` | `-32003` | User của bạn không được đọc bank app info. | Nhờ admin cấp quyền. |
| `404` | `-32004` | Không có bản ghi nào với id này. | Kiểm tra lại id. |
| `500` | `-32603` | Không tải được bản ghi. | Thử lại sau. Báo kèm `id` của response. |

### Cần biết

- `secret` không bao giờ được trả về. Nó chỉ ghi, không đọc.
- Field null, `""` và `false` bị bỏ, nên `defaultApp` chỉ xuất hiện khi là `true`.
