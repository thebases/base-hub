# base-calendar: hướng dẫn cho người dùng và quản trị viên

## 1. Mục đích và đối tượng sử dụng

`base-calendar` là engine lặp lịch tổng quát, không phụ thuộc model, phục vụ giao diện lịch. Module cho phép bất kỳ bản ghi của model đã đăng ký (ví dụ công việc, cuộc họp, ca làm việc) lặp lại theo lịch, và trả về các mục lịch trong một khoảng ngày, trong đó các quy tắc lặp được mở rộng ngay khi đọc.

| Đối tượng | Mục đích sử dụng |
| --- | --- |
| Quản trị viên / người dùng nâng cao | Định nghĩa và xem lại quy tắc lặp và ngoại lệ |
| Giao diện lịch (frontend) | Đọc các occurrence và kéo thả để dời lịch |
| Đơn vị tích hợp | Gọi công cụ RPC/MCP để truy vấn hoặc dời occurrence |

## 2. Điều kiện tiên quyết và cài đặt

- Phụ thuộc `base` và `base-automation`; `auto_install` bật nên module được cài tự động.
- Job dọn dẹp nền dựa vào bộ lập lịch của `base-automation` đang hoạt động (xem mục 6).

## 3. Khái niệm chính

| Khái niệm | Ý nghĩa dễ hiểu |
| --- | --- |
| Calendar Recurrence (`base.calendar.recurrence`, Lặp lại lịch) | Quy tắc lặp gắn với một bản ghi gốc, xác định bởi Model và Record ID. |
| Calendar Recurrence Exception (`base.calendar.recurrence.exception`, Ngoại lệ lặp lại lịch) | Thay đổi cho một occurrence riêng lẻ: dời sang ngày khác hoặc bỏ qua. |
| Occurrence | Một lần xuất hiện cụ thể do quy tắc sinh ra cho một ngày. Occurrence được tính khi đọc, không lưu trữ, nên quy tắc không có ngày kết thúc vẫn nhẹ. |

### Các trường của Recurrence

| Trường (nhãn UI) | Ý nghĩa |
| --- | --- |
| Model (`sys_model`, Mô hình) | Tên model đích; phải là model đã đăng ký (bắt buộc) |
| Record ID (ID bản ghi) | Id của bản ghi gốc trên model đó; bạn phải có quyền đọc (bắt buộc) |
| Frequency (Tần suất) | Daily, Weekly, Every 2 Weeks, Monthly, Yearly, Specific Weekday, Specific Day of Month (bắt buộc) |
| Interval (Khoảng thời gian) | Lặp lại mỗi N đơn vị của tần suất, mặc định 1 |
| By Weekday (Theo ngày trong tuần) | Ngày trong tuần từ 0 (Chủ nhật) đến 6 (Thứ bảy); bắt buộc với Weekly, Every 2 Weeks và Specific Weekday |
| By Month Day (Theo ngày trong tháng) | Ngày trong tháng 1 đến 31; bắt buộc với Specific Day of Month; tháng ngắn dùng ngày cuối tháng |
| Start Date (Ngày bắt đầu) | Ngày đầu tiên có hiệu lực (bắt buộc) |
| End Date (Ngày kết thúc) | Ngày cuối có hiệu lực; để trống nghĩa là lặp vô thời hạn |

### Các trường của Exception

| Trường (nhãn UI) | Ý nghĩa |
| --- | --- |
| Recurrence (Lặp lại) | Quy tắc bị ghi đè |
| Original Date (Ngày gốc) | Ngày do quy tắc sinh ra bị ghi đè |
| Override Date (Ngày ghi đè) | Ngày giờ mới; để trống khi bỏ qua |
| Is Skipped (Đã bỏ qua) | Loại occurrence này khỏi lịch |

## 4. Vị trí trong giao diện

| Menu hoặc route | Mở ra |
| --- | --- |
| Admin > Calendar Recurrences (Lặp lại lịch) tại `/web/base.calendar.recurrence` | Danh sách và biểu mẫu quy tắc, có tìm kiếm theo Model, Frequency và nhóm theo |
| Exceptions | Không có mục menu; model ngoại lệ có view danh sách và biểu mẫu, và ngoại lệ thường được tạo khi dời lịch |

## 5. Các tác vụ từng bước

### Cho một bản ghi lặp lại
1. Mở **Admin > Calendar Recurrences** và bấm New.
2. Nhập **Model** và **Record ID** của bản ghi gốc.
3. Chọn **Frequency** và **Interval**. Với quy tắc kiểu tuần hãy điền **By Weekday**; với Specific Day of Month hãy điền **By Month Day**.
4. Đặt **Start Date** và tùy chọn **End Date** (không sớm hơn ngày bắt đầu), rồi lưu.

### Dừng một chuỗi lặp
Mở quy tắc và đặt **End Date**, hoặc xóa quy tắc.

### Bỏ qua hoặc dời một occurrence
Tạo exception gồm quy tắc, **Original Date**, và **Is Skipped** hoặc **Override Date**. Trong giao diện lịch, kéo thả occurrence sẽ tự làm việc này.

### Dời lịch từ giao diện lịch (scope)
| Scope | Hiệu quả |
| --- | --- |
| this | Chỉ dời occurrence đó (ghi một exception) |
| following | Kết thúc quy tắc hiện tại vào ngày hôm trước và tạo quy tắc mới từ ngày mới |
| all | Dịch Start Date (và End Date) của cả quy tắc theo cùng độ lệch |

## 6. Quản trị và cấu hình

- **Quyền truy cập**: thành viên `base.group_user` có quyền đọc, ghi, tạo, xóa trên cả hai model. Quyền với bản ghi đích vẫn được kiểm tra: không thể tạo quy tắc cho bản ghi mà bạn không đọc được.
- **Phạm vi công ty**: quy tắc lấy công ty hiện tại của người tạo; exception kế thừa công ty của quy tắc.
- **Dữ liệu seed**: một Automation Action "Calendar: Cleanup Orphaned Recurrences" (Go Handler `base_calendar.cleanup_orphaned_recurrences`) và một Automation Cron cùng tên chạy mỗi 300 giây (tối đa 5 lần lỗi). Chúng xóa các quy tắc mà bản ghi gốc không còn tồn tại hoặc model không còn được đăng ký. Quản lý tại **Automation > Actions / Crons**.
- Xóa một quy tắc cũng xóa các exception của nó.

## 7. Điểm tích hợp API và tự động hóa

| Điểm tích hợp | Mục đích |
| --- | --- |
| RPC `base.calendar.recurrence` `occurrences` (`sys_model`, `date_from`, `date_to`, `domain` tùy chọn) | Trả về bản ghi thường cùng occurrence lặp đã mở rộng trong khoảng; mục lặp có `is_recurring`, `recurrence_id`, `master_record_id`, `occurrence_date`, id dạng `recurrence_id:date` |
| RPC `reschedule_occurrence` (`original_occurrence_date`, `new_datetime`, `scope`) | Dời một occurrence, theo id quy tắc |
| MCP `calendar.occurrences` | Giống `occurrences` |
| MCP `calendar.reschedule_occurrence` (`recurrence_id`, `original_occurrence_date`, `new_datetime`, `scope`) | Giống RPC |
| MCP `calendar.record.search_read`, `.read`, `.create`, `.write`, `.unlink` | CRUD trên hai model calendar; `create` yêu cầu `idempotency_key` |

Ngày nhận định dạng `YYYY-MM-DD` hoặc RFC3339; `date_to` không bao gồm và phải sau `date_from`.

## 8. Xử lý sự cố và câu hỏi thường gặp

| Hiện tượng | Nguyên nhân và cách xử lý |
| --- | --- |
| "sys_model ... is not registered" | Kiểm tra chính tả tên model và module của nó đã được cài |
| "permission denied: cannot create a recurrence on ..." | Bạn không có quyền đọc bản ghi gốc |
| "frequency ... requires by_weekday / by_monthday" | Điền trường ngày trong tuần hoặc ngày trong tháng tương ứng với tần suất |
| "end_date must not be before start_date" | Sửa lại các ngày |
| Mục lặp không hiện | Bản ghi gốc đã bị xóa hoặc bạn không đọc được, quy tắc nằm ngoài khoảng ngày, hoặc khoảng yêu cầu sai |
| Quy tắc biến mất | Job dọn dẹp đã xóa vì bản ghi gốc đã bị xóa |

## 9. Giới hạn và lưu ý

- Occurrence là ảo và được tính theo từng yêu cầu; không có gì được sinh sẵn.
- Quy tắc mồ côi bị ẩn ngay nhưng chỉ bị xóa thật ở lần dọn dẹp kế tiếp (tối đa khoảng 5 phút sau).
- Bản ghi gốc được liên kết chỉ bằng model và id, không phải khóa ngoại của cơ sở dữ liệu.
- Công ty của quy tắc là công ty phiên của người tạo, không phải của bản ghi gốc.
