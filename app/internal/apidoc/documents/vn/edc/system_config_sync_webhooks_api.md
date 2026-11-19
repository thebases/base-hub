## Đồng bộ MQTT webhook

`POST /base/system-config/sync-webhooks` không được triển khai trong Base HUB (trả về `404`). Không có hàng đợi tin MQTT chưa gửi để đẩy theo yêu cầu.

### Dùng gì thay thế

| Bạn muốn | Dùng |
| --- | --- |
| Xem các tin nhắn cũ của terminal | `GET /base/mqtt-messages` (xem `mqtt_message_list_api.md`). Bộ lọc `sync` tìm dòng theo trạng thái đồng bộ. |
| Chuyển tiếp sự kiện thiết bị cho đối tác | Một workflow của Base HUB. Workflow đang hoạt động có trigger MQTT trên `base/evt` hoặc `base/txn` sẽ chạy khi có tin nhắn từ terminal. Topic của workflow đang hoạt động cũng gọi được như webhook tại `/wh/<topic>`. Việc gửi và thử lại được đặt trên workflow (`base.workflow`). |
| Gửi lại lệnh cho thiết bị | Phát lệnh mới đã ký bằng `POST /base/devices/{serial}/commands`. |

### Ví dụ

```bash
curl "$BASE_URL/base/mqtt-messages?serial=SN0001&limit=20" \
  -H "Authorization: Bearer $TOKEN"
```

### Response

```json
{ "result": { "items": [ ], "total": 0 }, "id": "111020261535481234570001" }
```

Các dòng được lược bỏ. Xem `mqtt_message_list_api.md` để biết các trường của dòng.

### Lỗi

Lỗi dùng định dạng chuẩn được mô tả trong hướng dẫn API (xem "Response" → "Errors").

| HTTP | `error.code` | Ý nghĩa | Bạn nên làm gì |
| --- | --- | --- | --- |
| `404` | `-32601` | Bạn gọi `POST /base/system-config/sync-webhooks`, route này không tồn tại. | Dùng một trong các cách ở trên. |

### Cần biết

- Đừng xây tích hợp chờ một job `sync-webhooks`. Tin nhắn được gửi bởi trigger của workflow ngay khi chúng đến.
