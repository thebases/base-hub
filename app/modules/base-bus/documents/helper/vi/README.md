# Base Bus: hướng dẫn cho người dùng và quản trị viên

## 1. Mục đích và đối tượng sử dụng

Base Bus là bus dịch vụ (service bus) luôn bật của Base HUB. Nó chuyển các sự kiện thời gian thực từ backend tới trình duyệt (toast, làm mới dữ liệu, cập nhật menu, sự kiện workflow) và lưu nhật ký bền vững cho mọi sự kiện, để client kết nối lại có thể bắt kịp những gì đã bỏ lỡ.

| Đối tượng | Việc thực hiện |
| --- | --- |
| Người dùng cuối | Không thao tác trực tiếp. Chỉ thấy thông báo và làm mới dữ liệu theo thời gian thực. |
| Quản trị viên / vận hành | Kiểm tra cập nhật trực tiếp hoạt động, xem sự kiện gần đây, cấu hình transport cho trình duyệt (WebSocket hoặc MQTT) và reverse proxy. |
| Lập trình viên / AI agent | Publish và poll sự kiện qua các công cụ MCP `bus.*` hoặc model `base.service.bus`. |

## 2. Điều kiện tiên quyết và cài đặt

| Mục | Chi tiết |
| --- | --- |
| Phụ thuộc | `base` |
| Danh mục | System |
| Cài đặt | Tự động (`auto_install`). Module luôn được cài và không thể tắt hay gỡ. |
| Cơ sở dữ liệu | PostgreSQL (dùng LISTEN/NOTIFY để đánh thức các instance máy chủ khác). |
| Tùy chọn | Không cần thêm gì: đẩy sự kiện tới trình duyệt dùng WebSocket hoặc SSE. MQTT cho trình duyệt đang tắt (xem "MQTT cho đẩy sự kiện tới trình duyệt"). |

## 3. Khái niệm chính

| Khái niệm | Giải thích |
| --- | --- |
| Service Bus (`base.service.bus`) | Nhật ký sự kiện bền vững, chỉ ghi thêm (append-only). Mỗi sự kiện trên mỗi kênh là một dòng. |
| Kênh (Channel) | Luồng có tên. `servicebus:global` mọi người đều thấy; `servicebus:user:<id>` chỉ riêng một người dùng. Mặc định mỗi người dùng đăng ký cả hai. |
| Loại sự kiện (Event Type) | Nhãn ngắn mô tả sự kiện (ví dụ `action_client`). |
| Payload | Dữ liệu JSON tự do đính kèm sự kiện. |
| Con trỏ (`last`) | Id của sự kiện cuối cùng client đã nhận. Khi kết nối lại, client tiếp tục từ id này. |
| Transport | Cách sự kiện tới trình duyệt: WebSocket (mặc định), MQTT qua WebSocket (đang tắt), SSE hoặc HTTP long-poll (dự phòng). |

Các trường của một bản ghi bus: Kênh (Channel), Loại sự kiện (Event Type), Payload, Người tải lên (Created By), Ngày tạo (Created At).

## 4. Vị trí truy cập

Module không có menu hay màn hình riêng. Web client của Base HUB tự động dùng các endpoint HTTP bên dưới.

| Endpoint | Phương thức | Mục đích |
| --- | --- | --- |
| `/bus/transport_config` | GET | Cho client biết dùng transport nào. |
| `/bus/subscribe` | POST | Xác định danh sách kênh và con trỏ bắt đầu. |
| `/bus/ws` | GET (WebSocket) | Luồng đẩy thời gian thực mặc định. |
| `/bus/sse` | GET | Cùng luồng đẩy qua HTTP streaming thuần (không cần Upgrade). |
| `/websocket/peek_notifications` | POST | Dự phòng HTTP long-poll. |
| `/bus/has_missed_notifications` | POST | Cho biết sự kiện tại con trỏ của client còn tồn tại hay không. |
| `/websocket/health` | GET | Kiểm tra tình trạng transport của bus. |

## 5. Các tác vụ từng bước

### Kiểm tra cập nhật trực tiếp hoạt động
1. Đăng nhập Base HUB trên hai trình duyệt.
2. Thay đổi dữ liệu ở một trình duyệt (hoặc kích hoạt workflow có gửi thông báo).
3. Trình duyệt còn lại phải hiện toast hoặc làm mới mà không cần tải lại trang.

### MQTT cho đẩy sự kiện tới trình duyệt (đang tắt)
`/bus/transport_config` luôn trả về `websocket` (kèm dự phòng SSE), kể cả khi đã đặt `mqtt.public_ws_url`. Tài khoản broker duy nhất là tài khoản của máy chủ; gửi nó cho trình duyệt sẽ cho phép mọi người dùng đã đăng nhập publish hoặc subscribe với danh nghĩa máy chủ. MQTT cho trình duyệt sẽ được bật lại khi broker cấp thông tin đăng nhập ngắn hạn cho từng người dùng, kèm ACL theo topic. MQTT phía máy chủ (trigger workflow, lưu lượng thiết bị) không bị ảnh hưởng.

### Xem sự kiện gần đây (quản trị viên)
1. Dùng công cụ MCP `bus.recent` (mới nhất trước) hoặc `bus.record.search_read` trên `base.service.bus`.
2. Để theo dõi người dùng khác, lọc `bus.record.search_read` theo kênh: `bus.recent` chỉ trả về các kênh bạn được nhận (global, kênh của bạn, kênh discuss bạn tham gia).

### Publish sự kiện thử (lập trình viên)
1. Với tài khoản `base.group_system`, gọi `bus.publish` với `channels`, `event_type` và đối tượng `payload` (người dùng khác nhận lỗi `permission_denied`).
2. Mọi trình duyệt đang đăng ký nhận ngay sự kiện và mỗi kênh lưu một dòng.

## 6. Quản trị và cấu hình

| Chủ đề | Chi tiết |
| --- | --- |
| Nhóm / ACL | `security/sys_model_access.csv` không có quy tắc bổ sung. Quyền truy cập bản ghi `base.service.bus` theo ACL và record rule thông thường. |
| Dữ liệu seed | Không có. |
| Khóa cấu hình | `mqtt.public_ws_url` hiện bị bỏ qua đối với trình duyệt (xem "MQTT cho đẩy sự kiện tới trình duyệt"). |
| Bảo mật MQTT | Thông tin đăng nhập broker không bao giờ được gửi tới trình duyệt. |
| Reverse proxy | `/bus/ws` cần hỗ trợ WebSocket upgrade (xem Xử lý sự cố). |

## 7. API và điểm tích hợp tự động hóa

| Công cụ MCP | Mục đích |
| --- | --- |
| `bus.publish` | Publish sự kiện lên một hoặc nhiều kênh (chỉ quản trị hệ thống). |
| `bus.poll` | Sự kiện mới hơn `last_id` trên các kênh chỉ định cùng kênh mặc định của người gọi. |
| `bus.recent` | Sự kiện gần nhất, mới nhất trước (mặc định 50). |
| `bus.last_id` | Id sự kiện cao nhất đang lưu, dùng làm con trỏ bắt đầu cho `bus.poll`. |
| `bus.get_default_channels` | Các kênh mặc định của người gọi. |
| `bus.record.search_read` / `bus.record.read` | Truy cập bản ghi `base.service.bus` chỉ đọc (chỉ quản trị hệ thống). |

Không có công cụ tạo, ghi hay xóa: nhật ký chỉ ghi thêm và `bus.publish` là đường ghi duy nhất.

## 8. Xử lý sự cố và hỏi đáp

| Triệu chứng | Nguyên nhân và cách xử lý |
| --- | --- |
| `/bus/ws` trả về 426 | Header Upgrade bị proxy loại bỏ. Với nginx hãy đặt `proxy_http_version 1.1`, chuyển tiếp header `Upgrade` và `Connection`, và dùng `proxy_read_timeout` dài. Với Cloudflare, bật Network, WebSockets. |
| `/bus/ws` trả về 401 | Thiếu hoặc sai tham số truy vấn `token`, hoặc token thuộc cơ sở dữ liệu khác. |
| Không có cập nhật trực tiếp nhưng dữ liệu đúng | Client chuyển sang SSE hoặc long-poll; kiểm tra console trình duyệt và proxy. |
| Client bỏ lỡ sự kiện khi offline | Client tiếp tục từ con trỏ cuối; nếu dòng của con trỏ đã bị xóa, `has_missed_notifications` trả về true và client nên làm mới. |
| Máy chủ treo khi tắt | Không nên xảy ra: các kết nối `/bus/ws` được giải phóng khi nhận tín hiệu tắt. Hãy báo lỗi nếu gặp. |

## 9. Giới hạn và lưu ý

- Postgres NOTIFY chỉ là tín hiệu đánh thức. Dòng đã lưu mới là nguồn dữ liệu chuẩn.
- WebSocket và SSE xác thực bằng tham số truy vấn `token` vì trình duyệt không thể đặt header cho các kết nối này.
- Ở chế độ MQTT, sự kiện được publish thẳng lên broker (QoS 1) và đường NOTIFY không được dùng.
- Module không thể gỡ; nó là phụ thuộc của việc phát sự kiện workflow.
