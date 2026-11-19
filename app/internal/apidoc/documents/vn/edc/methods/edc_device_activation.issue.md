## Create Device Activation

Đặt activation code dùng một lần (mã 8 chữ số) để terminal nhập khi tự thiết lập lần đầu (provisioning). Đây là bước 2 của quy trình onboarding thiết bị. Xem `POST /base/devices` để có hướng dẫn đầy đủ create, activate, provision.

### Trước khi bắt đầu

- Bạn cần bearer token (xem API guide) của user có quyền ghi `edc.device.activation`.
- Thiết bị nên đã được đăng ký (bước 1). Serial chưa đăng ký thiết bị vẫn tạo được activation, nhưng provisioning sau đó sẽ lỗi `device_not_registered`.

### Yêu cầu

```http
POST /base/device/activation
Authorization: Bearer <token>
Content-Type: application/json
```

| Tên | Kiểu | Bắt buộc | Ý nghĩa |
| --- | --- | --- | --- |
| `serial` | string | Có | Serial của terminal, ví dụ `SN0001`. Tự viết hoa và cắt khoảng trắng. |
| `activationCode` | string | Có | **Đúng 8 chữ số**, ví dụ `48213907`. Định dạng khác vẫn được nhận ở đây nhưng không bao giờ dùng để provision được. |
| `expiresAt` | string (RFC 3339) | Không | Lúc mã hết hiệu lực, ví dụ `2026-09-27T08:00:00Z`. Nếu bỏ qua (hoặc giá trị không đọc được), mặc định là bây giờ + thời hạn activation của server (`edc.provisioning.activation_code_ttl_minutes`, mặc định 60 phút). |

### Ví dụ

```bash
curl -X POST "$BASE_URL/base/device/activation" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"serial":"SN0001","activationCode":"48213907","expiresAt":"2026-09-27T08:00:00Z"}'
```

### Phản hồi

`201 Created`. Activation nằm trong envelope `result` chuẩn.

```json
{
  "id": "111020261535481234570001",
  "result": {
    "id": 7,
    "serial": "SN0001",
    "expiresAt": "2026-09-27T08:00:00Z",
    "createdAt": "2026-09-26T08:05:00Z",
    "updatedAt": "2026-09-26T08:05:00Z"
  }
}
```

| Field | Ý nghĩa |
| --- | --- |
| `result.id` | Id của activation. Dùng trong `PUT /base/device/activation/{id}`. |
| `serial`, `expiresAt` | Terminal và lúc mã hết hiệu lực. |

> **Warning:** activation code không bao giờ được trả về. Hãy giữ mã bạn đã gửi và đưa nó cho người thiết lập terminal.

### Lỗi

Lỗi dùng định dạng chuẩn được mô tả trong API guide (xem "Response" → "Errors").

| HTTP | `error.code` | Ý nghĩa | Bạn nên làm gì |
| --- | --- | --- | --- |
| `400` | `-32700` | Body không phải JSON hợp lệ. | Sửa JSON. |
| `400` | `-32000` | `serial` hoặc `activationCode` rỗng, hoặc không lưu được bản ghi. | Gửi cả hai giá trị. |
| `401` | `-32001` | Thiếu hoặc sai token. | Lấy token mới. |
| `403` | `-32003` | User không có quyền ghi `edc.device.activation`. | Nhờ admin cấp quyền. |
| `500` | `-32603` | Server chưa cấu hình provisioning key (`reason`: `provisioning_secret_not_configured`). | Nhờ admin server cấu hình. |

### Cần biết

- Mỗi serial có một dòng activation. Gọi lại cho cùng serial sẽ thay mã cũ và xóa `usedAt`.
- Cấp mã sẽ **đưa thiết bị về trạng thái chờ**: provisioning status về `PENDING` và secret, secret version, MQTT client id bị xóa. Terminal đang `ACTIVE` bị ngắt cho đến khi provision lại bằng mã mới. `active` không đổi.
- Cũng có dạng lệnh RPC bên dưới. Ở đó `code` là tùy chọn, và nếu bỏ qua thì server tự sinh mã 8 chữ số và trả về **một lần**.

---

## Update Device Activation

Cấp activation code mới (và tùy chọn thời hạn mới) cho một activation đã có. Dùng để gia hạn mã đã hết hạn hoặc cho terminal một mã mới để nó provision lại.

### Yêu cầu

```http
PUT /base/device/activation/{id}
Authorization: Bearer <token>
Content-Type: application/json
```

| Tên | Kiểu | Bắt buộc | Ý nghĩa |
| --- | --- | --- | --- |
| `id` | integer (path) | Có | Id của activation, ví dụ `7`. |
| `activationCode` | string | Không, nhưng nên gửi | Mã mới, đúng 8 chữ số. **Nếu bỏ qua hoặc gửi `""`, server tạo mã ngẫu nhiên và không trả về ở đâu cả**, nên hãy luôn gửi mã bạn muốn. |
| `expiresAt` | string (RFC 3339) | Không | Thời hạn mới, ví dụ `2026-09-27T08:00:00Z`. Nếu bỏ qua (hoặc không đọc được) là bây giờ + thời hạn activation (mặc định 60 phút). Hãy kèm múi giờ (`Z` hoặc `+07:00`). |

### Ví dụ

Đổi mã và đặt thời hạn mới:

```bash
curl -X PUT "$BASE_URL/base/device/activation/7" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"activationCode":"48213907","expiresAt":"2026-09-27T08:00:00Z"}'
```

### Phản hồi

`200 OK`, cùng dạng với *Create Device Activation* bên trong `result`.

```json
{
  "id": "111020261535481234570001",
  "result": {
    "id": 7,
    "serial": "SN0001",
    "expiresAt": "2026-09-27T08:00:00Z",
    "createdAt": "2026-09-26T08:00:00Z",
    "updatedAt": "2026-09-26T10:00:00Z"
  }
}
```

`usedAt` luôn bị xóa, nên mã dùng lại được. Mã không bao giờ được trả về.

### Lỗi

Lỗi dùng định dạng chuẩn được mô tả trong API guide (xem "Response" → "Errors").

| HTTP | `error.code` | Ý nghĩa | Bạn nên làm gì |
| --- | --- | --- | --- |
| `400` | `-32602` | `id` không phải số. | Gửi id là số nguyên. |
| `400` | `-32700` | Body không phải JSON hợp lệ. | Sửa JSON. |
| `400` | `-32000` | Không lưu được bản ghi. | Đọc `message`. |
| `401` | `-32001` | Thiếu hoặc sai token. | Lấy token mới. |
| `403` | `-32003` | User không có quyền ghi `edc.device.activation`. | Nhờ admin cấp quyền. |
| `404` | `-32004` | Không có activation nào với id này. | Kiểm tra lại id. |
| `500` | `-32603` | Server chưa cấu hình provisioning key. Không có gì bị thay đổi. | Nhờ admin server cấu hình. |

### Cần biết

- **Mỗi lần gọi thành công đều reset thiết bị.** Secret, secret version và MQTT client id bị xóa, provisioning status về `PENDING`, và terminal bị đăng xuất. Đừng gọi trên thiết bị đang chạy tốt trừ khi bạn muốn thiết lập lại nó.
- Không có lệnh "chỉ gia hạn". Chỉ gửi `expiresAt` cũng làm mã bị thay bằng một mã ngẫu nhiên không biết. Để gia hạn, hãy gửi lại mã cùng với `expiresAt` mới.

---

## Call as RPC method: `edc.device.activation.issue`

Dùng dạng này từ code phía server. Nó chạy cùng logic với *Create Device Activation*.

```http
POST /rpc/edc.device.activation/issue
Authorization: Bearer <token>
Content-Type: application/json
```

Cùng method này cũng ở `POST /base/edc/device/activation/issue`. Không có path id. Gửi `args` là `[]` và đặt các giá trị trong `kwargs`.

| Key trong `kwargs` | Kiểu | Bắt buộc | Ý nghĩa |
| --- | --- | --- | --- |
| `serial` | string | Có | Serial, ví dụ `SN0001`. Tự viết hoa và cắt khoảng trắng. |
| `code` | string | Không | Mã 8 chữ số. Cũng chấp nhận `activation_code` và `activationCode`. Nếu bỏ qua, server tự sinh. |
| `expires_at` | string (RFC 3339) | Không | Thời hạn (cũng chấp nhận `expiresAt`). Mặc định bây giờ + thời hạn activation. |

```bash
curl -X POST "$BASE_URL/rpc/edc.device.activation/issue" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"args": [], "kwargs": {"serial": "SN0001", "expires_at": "2026-09-27T08:00:00Z"}}'
```

Response `200`:

```json
{
  "id": "111020261535481234570001",
  "result": { "id": 7, "serial": "SN0001", "expires_at": "2026-09-27T08:00:00Z", "activation_code": "48213907" }
}
```

> **Warning:** `activation_code` chỉ được trả về khi server tự sinh, và chỉ lần này. Hãy lưu lại ngay.

Lỗi: thiếu `serial` là `400` / `-32602`; server thiếu provisioning key là `500` / `-32603`.
