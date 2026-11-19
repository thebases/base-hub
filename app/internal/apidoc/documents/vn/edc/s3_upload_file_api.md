## Upload file lên S3

Route này không tồn tại trong Base HUB. Không có `POST /s3/upload`, và route bedc cũ không được chuyển sang. Đừng xây tích hợp mới dựa vào nó.

Hãy dùng upload attachment thay thế. Nó lưu file vào kho file riêng của Base HUB và tạo một bản ghi `sys.attachment` ("attachment" là file đã lưu cùng thông tin của nó).

### Trước khi bắt đầu

Bạn cần bearer token (xem hướng dẫn API) của user có quyền tạo attachment.

### Request

```http
POST /web/content
Authorization: Bearer <token>
Content-Type: multipart/form-data
```

| Tên | Kiểu | Bắt buộc | Ý nghĩa |
| --- | --- | --- | --- |
| `file` | file | Có | Nội dung file. Cũng có thể dùng tên trường `ufile`. Server giữ `Content-Type` bạn gửi, trừ khi nó rỗng hoặc giống HTML; khi đó server tự nhận diện kiểu từ nội dung. |
| `name` | form field | Không | Tên file để lưu. Mặc định: tên file đã upload. |
| `sys_model` | form field | Không | Tên model của bản ghi muốn gắn file vào, ví dụ `edc.device.log`. |
| `model_id` | form field | Không | Id của bản ghi đó, ví dụ `42`. |
| `is_public` | form field | Không | `true` hoặc `1` cho phép tải về không cần token. |
| `company_id`, `user_id`, `group_id` | form field | Không | Ai được xem file. Chỉ đặt tối đa một trong ba. |

Thay cho multipart, bạn có thể gửi JSON: `{"name": "...", "datas": "<base64>", "sys_model": "...", "model_id": 42, "mimetype": "..."}`.

### Ví dụ

```bash
curl -X POST "$BASE_URL/web/content" \
  -H "Authorization: Bearer $TOKEN" \
  -F "name=receipt-20260926.pdf" \
  -F "file=@./receipt-20260926.pdf;type=application/pdf"
```

### Response

```json
{
  "result": {
    "id": 5120,
    "name": "receipt-20260926.pdf",
    "mimetype": "application/pdf",
    "checksum": "9b2cf535f27731c974343645a3985328c0d3a3c7e5f2b4a1d6e8f90123456789",
    "file_size": 48213,
    "create_uid": 7,
    "is_public": false
  }
}
```

| Trường | Ý nghĩa |
| --- | --- |
| `id` | Id của attachment. Tải về bằng `GET /web/content/{id}`. |
| `checksum` | Dấu vân tay của nội dung file. |
| `file_size` | Kích thước tính bằng byte. |
| `is_public` | `true` nghĩa là tải về không cần token. |

Bản ghi còn có các trường thông thường (`active`, `created_at`, `updated_at`, ...). Tên key dùng snake_case.

### Lỗi

Lỗi ở đây là body văn bản thuần kèm HTTP status, không phải định dạng lỗi JSON.

| HTTP | Ý nghĩa | Bạn nên làm gì |
| --- | --- | --- |
| `400` | Không có phần `file`/`ufile` và không có JSON hợp lệ với `datas` base64; không đọc được file; `datas` rỗng hoặc không phải base64; hoặc các trường name/scope/binding không hợp lệ. | Đọc lý do trong body và sửa request. |
| `401` | Thiếu hoặc sai token. | Đăng nhập lại. |
| `403` | User không có quyền tạo attachment (`Permission denied: ...`). | Xin admin cấp quyền. |
| `413` | File quá lớn. Giới hạn mặc định 20 MB (cài đặt database `attachment.max_size_mb`). | Gửi file nhỏ hơn. |

### Cần biết

- Cùng một nội dung chỉ được lưu một lần cho mỗi database, nhưng mỗi lần upload vẫn tạo một bản ghi attachment riêng.
- File log thiết bị gửi qua `POST /base/device/log/upload` được lưu theo cách này. `storageUrl` của log là `/web/content/<attachmentId>`.
