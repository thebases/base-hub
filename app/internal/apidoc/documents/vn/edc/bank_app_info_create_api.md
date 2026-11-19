## Tạo bank app info

Tạo một cấu hình bank app: TID/MID (mã terminal và merchant do ngân hàng cấp), host ngân hàng và package của app. Sau đó bạn gắn nó vào device config.

### Trước khi bắt đầu

- Bạn cần bearer token.
- User của bạn phải có quyền tạo bản ghi `edc.tidmid.config`. Nếu không, bạn nhận `403`.

### Request

```http
POST /base/bank-app-info
Authorization: Bearer <token>
Content-Type: application/json
```

Mọi field đều tùy chọn. Key không biết sẽ bị bỏ qua.

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
curl -X POST "$BASE_URL/base/bank-app-info" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "appBankInfo": "BIDV",
    "bincode": "970418",
    "defaultApp": false,
    "environment": "prod",
    "ip": "10.20.30.40",
    "port": "17109",
    "packageName": "com.wdvn.bidv",
    "secret": "<bank-app-secret>",
    "thirdPartyId": "27981340",
    "tid": "12345678",
    "mid": "000000012345678"
  }'
```

### Response

HTTP `201`:

```json
{
  "id": "111020261535481234570001",
  "result": {
    "id": 8,
    "appBankInfo": "BIDV",
    "bincode": "970418",
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
    "createdAt": "2026-09-26T08:00:00Z",
    "updatedAt": "2026-09-26T08:00:00Z"
  }
}
```

Bản ghi nằm trong `result`.

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
| `400` | `-32602` hoặc `-32000` | Body không phải JSON hợp lệ, hoặc một field sai kiểu (ví dụ gửi `port` là số). | Sửa body. |
| `401` | `-32001` | Thiếu token hoặc token không hợp lệ. | Lấy token mới. |
| `403` | `-32003` | User của bạn không được tạo bank app info. | Nhờ admin cấp quyền. |

### Cần biết

- Key bạn bỏ qua sẽ nhận **giá trị mặc định của server**, không phải giá trị rỗng: `encryptType=HEXHL`, `environment=dev`, một `ip` mặc định, `port=17109`, `nii=003`, `packageName=com.wdvn.bidv`, và `refNo`, `secret`, `thirdPartyId` mặc định. `defaultApp` để trống. Key gửi là `""` được lưu là chuỗi rỗng.
- Luôn tự gửi `environment`, `ip`, `port` và `secret`. Như vậy bản ghi không vô tình dùng giá trị mặc định.
- Không kiểm tra trùng lặp. Cùng một TID/MID có thể được tạo hai lần, và nhiều bản ghi có thể cùng `defaultApp: true`.
