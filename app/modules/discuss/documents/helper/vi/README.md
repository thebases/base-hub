# Discuss: hướng dẫn cho người dùng và quản trị viên

## 1. Mục đích và đối tượng sử dụng

`discuss` bổ sung chatter (luồng trao đổi trên bản ghi), người theo dõi, hoạt động đã lên lịch, phản hồi mẫu, kênh chat, xử lý email gửi đi/đến và theo dõi cuộc gọi cho Base HUB.

| Đối tượng | Việc thực hiện với module |
| --- | --- |
| Người dùng cuối (`base.group_user`) | Đăng và đọc tin nhắn trên bản ghi, theo dõi bản ghi, lên lịch và hoàn thành hoạt động, dùng phản hồi mẫu, chat trong kênh |
| Quản trị viên | Quản lý Activity Types và Canned Responses, cấu hình mail server và alias nhận mail, bảo vệ webhook nhận mail |
| Nhà tích hợp | Gọi các phương thức RPC của model và webhook nhận email |

## 2. Điều kiện tiên quyết và cài đặt

- Phụ thuộc `base` và `base-bus` (tin nhắn và thông báo được gửi thời gian thực qua bus).
- Không phải module lõi: `installable: true`, `auto_install: false`. Cài đặt từ danh sách Apps.
- Khi cài đặt, module nạp sẵn năm loại hoạt động (xem mục 6) và thêm khung chatter vào form Contact (`tbl.partner`).
- Email gửi đi chỉ hoạt động sau khi cấu hình ít nhất một mail server (mục 6).

## 3. Khái niệm chính

| Model | Ý nghĩa |
| --- | --- |
| `discuss.message` | Một tin nhắn chatter trên bất kỳ bản ghi nào, xác định bởi `res_model` + `res_id`. Gồm Subject, Body (Nội dung), Message Type (Comment, Notification, Email, Auto Comment), Author (Tác giả), tin nhắn cha để tạo luồng |
| `discuss.message.subtype` | Nhãn trên tin nhắn quyết định người theo dõi nào được thông báo |
| `discuss.followers` | Người dùng đăng ký theo dõi một bản ghi, có thể giới hạn theo subtype |
| `discuss.notification` | Dòng giao nhận và đã đọc cho từng người nhận (loại Inbox) |
| `discuss.activity.type` | Mẫu việc cần làm dùng lại: độ trì hoãn, đơn vị, trì hoãn từ, kiểu liên kết chuỗi, biểu tượng |
| `discuss.activity` | Việc cần làm đã lên lịch trên bản ghi, gồm Deadline (Hạn chót), Assigned To (Được giao cho), Summary, Note. Trạng thái quá hạn / hôm nay / sắp tới được tính từ hạn chót, không lưu |
| `discuss.canned.response` | Câu tắt (Source) mở rộng thành văn bản dài (Substitution), có thể giới hạn theo nhóm |
| `discuss.channel` | Cuộc hội thoại Chat (1:1), Group (riêng tư) hoặc Channel (phát rộng); tin nhắn là các dòng `discuss.message` thông thường |
| `discuss.channel.member` | Tư cách thành viên kênh, kèm tin nhắn đã xem cuối cùng và thời điểm tắt thông báo |
| `discuss.channel.rtc.session` | Thành viên đang trong cuộc gọi (cờ camera, chia sẻ màn hình, tắt mic, tắt âm thanh nghe). Bị xóa khi rời cuộc gọi |
| `discuss.call.history` | Bản ghi lâu dài về lúc bắt đầu/kết thúc của mỗi cuộc gọi |
| `discuss.guest` | Người tham gia bên ngoài, không phải user (Name, Email, Access Token) |
| `discuss.alias` | Bí danh nhận email: phần local của địa chỉ, model đích, giá trị mặc định, chính sách liên hệ (Everyone / Followers Only) |
| `discuss.mail.server` | Máy chủ SMTP gửi đi (host, port, thông tin đăng nhập, TLS, sequence) |
| `discuss.mail` | Hàng đợi email gửi đi: mỗi lần gửi một dòng, trạng thái Outgoing, Sending, Sent, Exception, Cancelled |

## 4. Vị trí trong giao diện

| Vị trí | Chức năng |
| --- | --- |
| Menu chính > Canned Responses (Phản hồi mẫu) (`/web/tbl.discuss_canned_response`) | Danh sách và form cho các câu mẫu |
| Menu chính > Activity Types (Loại hoạt động) (`/web/tbl.discuss_activity_type`) | Danh sách và form cho mẫu hoạt động |
| Form Contact (`tbl.partner`) | Khung chatter cho tin nhắn, người theo dõi và hoạt động |
| `POST /discuss/inbound_email` | Webhook nhận email (không phải màn hình) |

Hiện chưa có menu hay màn hình cho kênh, cuộc gọi, mail server, hàng đợi mail hoặc alias. Các model này chỉ truy cập được qua RPC/API model chung.

## 5. Các tác vụ từng bước

### Trao đổi về một bản ghi
1. Mở một Contact và dùng khung chatter.
2. Soạn tin nhắn và đăng. Người theo dõi bản ghi nhận thông báo ngay lập tức.
3. Trả lời một tin nhắn để xếp nó vào luồng dưới tin nhắn cha.

### Theo dõi hoặc bỏ theo dõi bản ghi
Đăng ký từ khung chatter, có thể giới hạn theo subtype. Hủy đăng ký cũng làm tại đó.

### Lên lịch một hoạt động
1. Chọn loại hoạt động (Email, Call, Meeting, To Do, Upload Document).
2. Đặt Deadline, người được giao (Assigned To), và Summary hoặc Note.
3. Khi xong, đánh dấu hoàn thành. Việc hoàn thành được ghi bằng một tin nhắn tự động, và hoạt động kế tiếp có thể được gợi ý hoặc tự kích hoạt tùy Chaining Type của loại hoạt động.

### Tạo loại hoạt động
1. Mở Activity Types, chọn New.
2. Điền Name, Sequence, Resource Model (tùy chọn), Icon, Decoration Type (Warning hoặc Danger).
3. Đặt Delay Count, Delay Unit (Days, Weeks, Months), Delay From (Current Date hoặc hạn của hoạt động trước), Chaining Type và loại kế tiếp (tùy chọn).

### Tạo phản hồi mẫu
Mở Canned Responses, chọn New. Nhập Source (câu tắt) và Substitution (nội dung mở rộng). Dùng Groups để giới hạn người thấy. Last Used At được cập nhật khi sử dụng.

### Dùng kênh (hiện chỉ qua API)
Tạo kênh (Chat, Group hoặc Channel), tham gia hoặc rời, đăng tin nhắn, đánh dấu đã xem, tắt thông báo đến một thời điểm. Dùng các helper runtime tương ứng qua API.

## 6. Quản trị và cấu hình

- Quyền truy cập: mọi model đều cho `base.group_user` đầy đủ CRUD (ACL trong `security/sys_model_access.csv`). Không có nhóm quản lý riêng.
- Dữ liệu seed: các loại hoạt động Email, Call, Meeting, To Do, Upload Document (đều trì hoãn 0 ngày, chaining Suggest). Được làm mới khi cập nhật module.
- Mail server: tạo một dòng `discuss.mail.server` gồm Host, Port (mặc định 587), Username, Password, Use TLS (mặc định bật), Sequence (số nhỏ được thử trước). Nếu chưa cấu hình, mail trong hàng đợi bị đánh dấu Exception với lý do "no mail server configured".
- Quét hàng đợi mail: handler `sys.cron` tên `discuss.send_queued_mail` sẽ gửi mail trong hàng đợi. Không có lịch nào được seed sẵn, nên quản trị viên phải tạo một cron dùng handler này.
- Secret của webhook nhận mail: đặt cấu hình `app.discussInboundWebhookSecret`. Bên gọi phải gửi secret qua header `X-Discuss-Webhook-Secret` (không còn chấp nhận tham số query `secret`). Nếu để trống, webhook bị tắt và trả về 403.
- Alias: tạo `discuss.alias` với Alias Name (phần local, duy nhất), Target Model, Defaults (JSON, tùy chọn), Force Thread ID và Contact Policy. Chỉ thành viên `base.group_system` được tạo, sửa hoặc xóa alias. Target Model phải có form view bật chatter; `base.users`, `tbl.groups` và các model `sys.*`/`ui.*` bị từ chối.

## 7. Điểm tích hợp API và tự động hóa

| Điểm tích hợp | Mục đích |
| --- | --- |
| `tbl.discuss_message` `post_message` (cấp model) | Cách đăng chatter được hỗ trợ; xử lý người theo dõi, thông báo và đẩy thời gian thực. Không dùng create thô |
| `tbl.discuss_activity` `schedule_activity` (cấp model) | Lên lịch hoạt động |
| `tbl.discuss_activity` `mark_done` (theo id bản ghi) | Hoàn thành hoạt động |
| CRUD/RPC model chung | Tất cả model `tbl.discuss_*` còn lại |
| `POST /discuss/inbound_email` | Nhận email đã phân tích (`message_id_header`, `in_reply_to`, `references`, `from_email`, `to_emails`, `cc_emails`, `subject`, `body_html`); trả về `status`, `res_model`, `res_id`, `message_id` |
| Topic bus | `discuss:channel:<id>` cho sự kiện kênh, `discuss:user:<id>` cho thông báo cá nhân và tín hiệu cuộc gọi |

Thứ tự định tuyến email đến: khớp luồng sẵn có theo Message-ID, In-Reply-To hoặc References; nếu không, khớp phần local của người nhận với alias; nếu không, trả về `no_route` (vẫn HTTP 200).

## 8. Xử lý sự cố và câu hỏi thường gặp

| Vấn đề | Kiểm tra |
| --- | --- |
| Email mãi ở trạng thái Outgoing | Chưa có cron chạy `discuss.send_queued_mail`, hoặc cron đang tắt |
| Email ở trạng thái Exception, "no mail server configured" | Thêm một `discuss.mail.server` đang hoạt động |
| Email đến trả về 401 | Thiếu hoặc sai secret |
| Email đến trả về `no_route` | Không khớp luồng nào và không có alias với phần local đó, hoặc model đích của alias không được phép |
| Email đến trả về 403 | Chưa đặt `app.discussInboundWebhookSecret`, hoặc header `X-Discuss-Webhook-Secret` thiếu hoặc sai |
| Tin nhắn không thông báo cho người theo dõi | Tin nhắn được tạo bằng create thô thay vì `post_message`; hoặc bộ lọc subtype của người theo dõi loại trừ nó |
| Form không có chatter | View form phải bật chatter (mặc định chỉ form Contact có) |

## 9. Giới hạn và lưu ý

- Chỉ hỗ trợ email gửi đi; chưa có màn hình cho kênh, cuộc gọi, mail server, hàng đợi hay alias.
- Khớp alias chỉ dựa trên phần local, không có khái niệm domain.
- Không có TURN/STUN hay truyền media: cuộc gọi chỉ theo dõi trạng thái và chuyển tiếp tín hiệu qua bus.
- Khách (guest) có danh tính, token và có thể đăng tin nhắn. Quản lý phiên, phân quyền theo bản ghi và trang portal chưa có; bên gọi phải tự quyết định guest được phép truy cập gì.
- Thông báo chỉ được gửi trong ứng dụng (Inbox).
