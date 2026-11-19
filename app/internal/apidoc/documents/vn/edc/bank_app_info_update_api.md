## Cập nhật bank app info

Đổi các field bạn gửi trên một cấu hình bank app (TID/MID) đã có. Field bạn không gửi giữ nguyên.

### Trước khi bắt đầu

- Bạn cần bearer token.
- User của bạn phải có quyền sửa bản ghi `edc.tidmid.config`. Nếu không, bạn nhận `403`.

### Request

```http
PUT /base/bank-app-info/{id}
Authorization: Bearer <token>
Content-Type: application/json
```

| Tên | Kiểu | Bắt buộc | Ý nghĩa |
| --- | --- | --- | --- |
| `id` | integer | Có | Id của bank app info (trong path), ví dụ `7`. |

Các field trong body giống "Tạo bank app info". Chỉ gửi những key bạn muốn đổi. Key không biết bị bỏ qua.

| Tên | Kiểu | Bắt buộc | Ý nghĩa |
| --- | --- | --- | --- |
| `appBankInfo` | string | Không | Nhãn ngân hàng hoặc app, ví dụ `BIDV`. |
| `bincode` | string | Không | Mã BIN của ngân hàng (những chữ số đầu của số thẻ), ví dụ `970418`. |
| `defaultApp` | boolean | Không | `true` đánh dấu đây là bank app mặc định. |
| `encryptType` | string | Không | Kiểu mã hóa, ví dụ `HEXHL`. |
| `environment` | string | Không | Nhãn môi trường, ví dụ `dev` hoặc `prod`. |
| `ip` | string | Không | Địa chỉ IP của host ngân hàng, ví dụ `10.20.30.40`. |
| `port` | string | Không | Port của host ngân hàng, **dưới dạng string**, ví dụ `"17109"`. |
| `nii` | string | Không | NII (mã mạng terminal dùng với ngân hàng), ví dụ `003`. |
| `packageName` | string | Không | Android package của bank app, ví dụ `com.wdvn.bidv`. |
| `refNo` | string | Không | Số tham chiếu, ví dụ `08062026`. |
| `secret` | string | Không | Secret của bank app. Chỉ ghi: được lưu nhưng không route nào trả về. |
| `thirdPartyId` | string | Không | Id bên thứ ba do ngân hàng cấp, ví dụ `27981340`. |
| `tid` | string | Không | Terminal ID (mã của terminal tại ngân hàng), ví dụ `12345678`. |
| `mid` | string | Không | Merchant ID (mã của cửa hàng tại ngân hàng), ví dụ `000000012345678`. |

### Ví dụ

```bash
curl -X PUT "$BASE_URL/base/bank-app-info/7" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"ip": "10.20.30.41", "port": "17109"}'
```

### Response

HTTP `200`. Bản ghi đã cập nhật nằm trong `result`:

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
    "ip": "10.20.30.41",
    "port": "17109",
    "nii": "003",
    "packageName": "com.wdvn.bidv",
    "refNo": "08062026",
    "thirdPartyId": "27981340",
    "tid": "12345678",
    "mid": "000000012345678",
    "createdAt": "2026-09-20T03:15:00Z",
    "updatedAt": "2026-09-26T08:05:00Z"
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
| `400` | `-32602` hoặc `-32000` | `id` không phải số, body không phải JSON hợp lệ, hoặc một field sai kiểu. | Sửa request. |
| `401` | `-32001` | Thiếu token hoặc token không hợp lệ. | Lấy token mới. |
| `403` | `-32003` | User của bạn không được sửa bank app info. | Nhờ admin cấp quyền. |
| `404` | `-32004` | Không có bản ghi nào với id này. | Kiểm tra lại id. |

### Cần biết

- Đây là **cập nhật một phần**. Key bạn bỏ qua giữ nguyên giá trị, kể cả `secret`. Key gửi là `""` được lưu là chuỗi rỗng. Giá trị mặc định của server không được áp dụng lại.
- Bạn không đọc lại được `secret`. Chỉ gửi khi bạn muốn đổi nó.
- Thay đổi áp dụng cho mọi device config đang gắn bản ghi này.
