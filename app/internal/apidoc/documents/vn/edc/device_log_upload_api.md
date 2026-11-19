## Upload Device Log File

Terminal gọi API này để upload file log (text thường hoặc file nén như `.zip`, `.tar.gz`). Server lưu file thành attachment và tạo một log gắn với file đó, trong cùng một bước. Sau đó admin xem log bằng `GET /base/device/log`.

### Trước khi bắt đầu

- API này do terminal gọi, không phải user. Nó dùng **device token**, không dùng user token. User token sẽ bị từ chối.
- Device token có dạng `<serial>:<secretVersion>:<deviceSecret>`. Serial là số in trên terminal. Secret và version được cấp cho terminal khi nó tự thiết lập lần đầu (provisioning).
- Thiết bị phải đang active, đã provision và chưa bị revoke.

### Yêu cầu

```http
POST /base/device/log/upload
Authorization: Bearer <serial>:<secretVersion>:<deviceSecret>
Content-Type: multipart/form-data
```

Ví dụ header: `Authorization: Bearer SN0001:1:9f8e7d6c...`.

| Form field | Kiểu | Bắt buộc | Ý nghĩa |
| --- | --- | --- | --- |
| `file` | file | Có | File log. Không được rỗng. Nhận mọi loại file. Phần mở rộng quyết định kiểu nén (`.zip`, `.gz`, `.tar.gz`, `.tgz`, `.bz2`, `.tar.bz2`, `.xz`, `.7z`, `.rar`). |
| `level` | string | Không | Mức log, ví dụ `ERROR`, `WARN`, `INFO`. |
| `tag` | string | Không | Nhóm tự do, ví dụ `payment`. Mặc định `file`. |
| `bankBincode` | string | Không | Mã BIN ngân hàng liên quan, ví dụ `970436`. |
| `loggedAt` | string | Không | Lúc log được tạo, RFC 3339, ví dụ `2026-09-26T14:59:30+07:00`. Mặc định là lúc upload. |
| `metadata` | string | Không | Một **object** JSON viết dạng text, ví dụ `{"appVersion":"1.3.2"}`. Mảng hoặc giá trị khác bị từ chối. |
| `deviceSerial` | string | Không | Bị bỏ qua. Serial luôn lấy từ token. |

Server tự điền phần còn lại:

- `message` luôn là `uploaded device log file <tên file gốc>`.
- `fileExtension` là phần mở rộng viết thường kèm dấu chấm (`.zip`, `.tar.gz`, `.txt`).
- `compressionType` là một trong `zip`, `gzip`, `tar.gz`, `tar.bz2`, `bzip2`, `xz`, `7z`, `rar`. Với file khác thì bị bỏ.
- `storedFileName` là `<UTC yyyyMMddTHHmmssZ>_<tên file>`. Khoảng trắng và `/ \ :` trong tên đổi thành `_`.
- `contentType` là `Content-Type` của phần file, nếu không có thì đoán từ phần mở rộng, nếu không nữa thì `application/octet-stream`.

### Ví dụ

```bash
curl -X POST "$BASE_URL/base/device/log/upload" \
  -H "Authorization: Bearer SN0001:1:$DEVICE_SECRET" \
  -F "file=@edc-20260926.zip" \
  -F "level=ERROR" \
  -F "tag=payment" \
  -F "bankBincode=970436" \
  -F "loggedAt=2026-09-26T14:59:30+07:00" \
  -F 'metadata={"appVersion":"1.3.2"}'
```

### Phản hồi

`201 Created`. Body là log mới tạo. Route này là hợp đồng cố định cho firmware terminal, nên **không** bọc trong envelope `result`.

```json
{
  "id": 311,
  "deviceSerial": "SN0001",
  "level": "ERROR",
  "tag": "payment",
  "message": "uploaded device log file edc-20260926.zip",
  "metadata": { "appVersion": "1.3.2" },
  "originalFileName": "edc-20260926.zip",
  "storedFileName": "20260926T080000Z_edc-20260926.zip",
  "fileExtension": ".zip",
  "contentType": "application/zip",
  "compressionType": "zip",
  "fileSize": 48213,
  "storageProvider": "attachment",
  "storagePath": "device-logs/SN0001/2026/09/26/20260926T080000Z_edc-20260926.zip",
  "storageUrl": "/web/content/5120",
  "bankBincode": "970436",
  "loggedAt": "2026-09-26T07:59:30Z",
  "createdAt": "2026-09-26T08:00:00Z",
  "severity": "ERROR"
}
```

| Field | Ý nghĩa |
| --- | --- |
| `id` | Id của log. |
| `storageProvider` / `storageUrl` | Luôn là `"attachment"` / đường dẫn tải về `/web/content/<attachmentId>`. |
| `loggedAt`, `createdAt` | Thời gian UTC. |
| `severity` | Bản sao của `level`. Chỉ response upload này có. |

`metadata` rỗng và các field tùy chọn chưa đặt sẽ bị bỏ.

### Lỗi

Route này giữ body lỗi riêng: `{"error": "<code>", "message": "..."}`.

| HTTP | `error` | Ý nghĩa | Bạn nên làm gì |
| --- | --- | --- | --- |
| `400` | `file_required` | Không có field `file`, body không phải `multipart/form-data`, hoặc file rỗng. | Gửi file không rỗng. |
| `400` | `invalid_metadata` | `metadata` không phải object JSON. | Gửi object như `{"a":1}`. |
| `400` | `invalid_logged_at` | `loggedAt` không phải RFC 3339. | Dùng dạng `2026-09-26T14:59:30+07:00`. |
| `401` | `missing_device_token` | Thiếu header `Authorization` hoặc không phải `Bearer ...`. | Gửi device token. |
| `401` | `invalid_device_token` | Token sai định dạng, không tồn tại, secret sai hoặc cũ, hoặc thiết bị inactive, bị revoke, chưa provision. | Kiểm tra token. Sau khi provision lại thì dùng secret version mới. |
| `500` | `internal_error` | Không lưu được file, hoặc lỗi server khác. | Thử lại sau. |

Cả hai mã `401` đều có message `device authentication failed`.

### Cần biết

- Serial trong token không phân biệt hoa thường (`sn0001` vẫn dùng được cho `SN0001`). Log được lưu theo serial đã lưu của thiết bị.
- Sau khi thiết bị được provision lại, secret và version cũ không còn dùng được.
- File nén lớn có thể chậm. Hãy đặt timeout phía client đủ lớn.
