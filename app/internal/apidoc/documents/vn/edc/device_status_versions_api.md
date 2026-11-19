## List Device Software Versions

Trả về các phiên bản app và firmware khác nhau mà terminal đã báo. Dùng để điền các filter phiên bản trên màn hình trạng thái thiết bị.

### Trước khi bắt đầu

Bạn cần bearer token (xem API guide) của user có quyền đọc `edc.device.status`.

### Yêu cầu

```http
GET /base/device/status/versions
Authorization: Bearer <token>
```

Không có tham số.

### Ví dụ

```bash
curl "$BASE_URL/base/device/status/versions" \
  -H "Authorization: Bearer $TOKEN"
```

### Phản hồi

`200 OK`.

```json
{
  "id": "111020261535481234570001",
  "result": {
    "appVersions": ["1.4.2", "1.4.1", "1.3.9"],
    "firmwareVersions": ["V2.0.1", "V1.9.7"]
  }
}
```

| Field | Ý nghĩa |
| --- | --- |
| `appVersions` | Các phiên bản app khác nhau, mới nhất trước. |
| `firmwareVersions` | Các phiên bản firmware khác nhau, mới nhất trước. |

Hai danh sách luôn có mặt. Chúng là `[]` khi chưa có thiết bị nào báo cáo.

### Lỗi

Lỗi dùng định dạng chuẩn được mô tả trong API guide (xem "Response" → "Errors").

| HTTP | `error.code` | Ý nghĩa | Bạn nên làm gì |
| --- | --- | --- | --- |
| `401` | `-32001` | Thiếu hoặc sai token. | Lấy token mới. |
| `403` | `-32003` | Bạn không đọc được `edc.device.status`. | Nhờ admin cấp quyền. |
| `500` | `-32603` | Lỗi database. | Thử lại sau và báo `id` của response. |

### Cần biết

- Sắp xếp theo thứ tự chữ thông thường, nên `1.10.0` đứng trước `1.9.0`.
- Phiên bản rỗng và null bị bỏ, mỗi danh sách không có giá trị trùng.
- API này chỉ đọc dữ liệu.
- Cũng có thể gọi bằng `POST /base/edc/device/status/versions` và `POST /rpc/edc.device.status/versions` (body JSON-RPC, không tham số). Ở các route này, nếu bị từ chối đọc thì trả `{"result": null}` thay vì `403`.
