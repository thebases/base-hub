## Danh sách bank app info

Trả về một trang các cấu hình bank app (TID/MID, host ngân hàng, package name), mới nhất trước. Dùng để hiển thị bảng, hoặc để chọn một cái gắn vào device config.

### Trước khi bắt đầu

- Bạn cần bearer token.
- User của bạn phải có quyền đọc bản ghi `edc.tidmid.config`. Nếu không, bạn nhận `403`.

### Request

```http
GET /base/bank-app-info?limit=50&offset=0&search={text}&default_app={bool}&environment={env}
Authorization: Bearer <token>
```

| Tên | Kiểu | Bắt buộc | Ý nghĩa |
| --- | --- | --- | --- |
| `limit` | integer | Không | Kích thước trang, từ 1 đến 500. Mặc định `50`. Giá trị không hợp lệ hoặc ngoài khoảng sẽ thành `50`. |
| `offset` | integer | Không | Số dòng bỏ qua. Mặc định `0`. |
| `search` | string | Không | Khớp một phần, không phân biệt hoa thường, trên `appBankInfo`, `packageName`, `tid`, `mid` hoặc `bincode`. Ví dụ `BIDV`. |
| `default_app` | boolean | Không | `true` hoặc `false`. Giá trị không hợp lệ bị bỏ qua (không lọc). |
| `environment` | string | Không | Khớp chính xác, ví dụ `dev` hoặc `prod`. |

### Ví dụ

```bash
curl "$BASE_URL/base/bank-app-info?search=BIDV&environment=prod&limit=20" \
  -H "Authorization: Bearer $TOKEN"
```

### Response

HTTP `200`:

```json
{
  "id": "111020261535481234570001",
  "result": {
    "items": [
      {
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
    ],
    "total": 1
  }
}
```

| Field | Ý nghĩa |
| --- | --- |
| `result.items` | Các bản ghi của trang này. Mỗi item có cùng field như trong "Lấy bank app info". |
| `result.total` | Tổng số bản ghi khớp bộ lọc trên mọi trang. Dùng để phân trang. |

### Lỗi

Lỗi dùng định dạng chuẩn được mô tả trong API guide (xem "Response" → "Errors").

| HTTP | `error.code` | Ý nghĩa | Bạn nên làm gì |
| --- | --- | --- | --- |
| `401` | `-32001` | Thiếu token hoặc token không hợp lệ. | Lấy token mới. |
| `403` | `-32003` | User của bạn không được đọc bank app info. | Nhờ admin cấp quyền. |
| `500` | `-32603` | Không tải được danh sách. | Thử lại sau. Báo kèm `id` của response. |

### Cần biết

- Field null, `""` và `false` bị bỏ khỏi từng item. Ví dụ không có key `tid` khi chưa đặt TID, và `defaultApp` chỉ xuất hiện khi là `true`.
- `secret` không bao giờ được trả về bởi bất kỳ route bank app info nào.
