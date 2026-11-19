## Preview Device Config Template

Cho xem config mà thiết bị sẽ nhận nếu một config template được gán cho nó. Đó là JSON đã lưu của template với `cardConfig.tidmid` được dựng lại từ các bank app liên kết của template. Thao tác admin "Copy from Device Config" dùng API này. Nó không gán gì cả.

### Trước khi bắt đầu

- Bạn cần bearer token (xem API guide) của user có quyền đọc `edc.device.status`. Nếu bị từ chối đọc sẽ trả `{"result": null}`, không phải `403`.
- Bạn cần id của một phiên bản config template **active**.

### Yêu cầu

```http
POST /base/edc/device/status/preview-template-config
Authorization: Bearer <token>
Content-Type: application/json
```

API này giống lệnh RPC `POST /rpc/edc.device.status/preview_template_config`. Body là `{ "args": [], "kwargs": { "config_id": 14 } }`.

| Tên | Kiểu | Bắt buộc | Ý nghĩa |
| --- | --- | --- | --- |
| `kwargs.config_id` | integer | Có | Id của một phiên bản config template active, ví dụ `14`. Cũng chấp nhận `configId`. |

### Ví dụ

```bash
curl -X POST "$BASE_URL/base/edc/device/status/preview-template-config" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"kwargs":{"config_id":14}}'
```

### Phản hồi

`200 OK`. `result` là bản sao toàn bộ JSON config (rút gọn ở đây).

```json
{
  "result": {
    "owner": "THEBASE",
    "functions": { "qrFunc": true, "cardFunc": true },
    "cardConfig": { "tidmid": [ { "...": "..." } ] }
  }
}
```

### Lỗi

Lỗi dùng định dạng chuẩn được mô tả trong API guide (xem "Response" → "Errors").

| HTTP | Ý nghĩa | Bạn nên làm gì |
| --- | --- | --- |
| `400` | `config_id` thiếu hoặc không dương (`config_id is required`), template không tồn tại hoặc không active (`device config template is not active`), hoặc lỗi database. | Gửi id của phiên bản template active. |
| `401` | Thiếu hoặc sai token. | Lấy token mới. |

### Cần biết

- Chỉ template active mới xem trước được. Quy tắc này giống *Assign Device Config*, nên bản xem trước khớp với những gì thiết bị nhận.
- Nếu template không có bank app liên kết, `cardConfig.tidmid` của chính nó được trả nguyên vẹn.
- Các key được thêm khi terminal đọc config (`owner`, `state`, `mqttConfig.mqttDeviceTopic`) không được thêm ở đây.
- An toàn khi gọi lại. API chỉ đọc dữ liệu.
