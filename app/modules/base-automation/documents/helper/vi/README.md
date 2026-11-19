# base-automation: hướng dẫn cho người dùng và quản trị viên

## 1. Mục đích và đối tượng sử dụng

`base-automation` cho phép quản trị viên định nghĩa **automation action** (hành động tự động hóa, một đơn vị công việc nhỏ) và **automation cron** (lịch chạy một action sau mỗi N giây). Đây là nền tảng lập lịch mà các module khác dùng lại: ví dụ `base-calendar` chạy tác vụ dọn dẹp thông qua module này.

| Đối tượng | Mục đích sử dụng |
| --- | --- |
| Quản trị viên hệ thống | Tạo, chạy thử, lập lịch và theo dõi action và cron |
| Lập trình viên / tích hợp | Viết script Lua, đăng ký Go handler, gọi công cụ RPC/MCP |
| Module khác | Giao các tác vụ nền đã biên dịch cho bộ điều phối dùng chung |

## 2. Điều kiện tiên quyết và cài đặt

- Chỉ phụ thuộc `base`. Module có `auto_install` và luôn được cài, không thể tắt hay gỡ.
- Bộ lập lịch của kernel phải đang chạy: một job lập lịch được seed sẵn sẽ kích hoạt bộ điều phối.
- Chỉ thành viên nhóm `base.group_system` mới thấy và dùng được các màn hình.

## 3. Khái niệm chính

| Khái niệm | Ý nghĩa dễ hiểu |
| --- | --- |
| Automation Action (`automation.action`) | Đơn vị công việc tái sử dụng, gồm tên, loại và cấu hình. Có thể chạy thủ công hoặc bởi cron. |
| Automation Cron (`automation.cron`) | Lịch trỏ tới đúng một action và chạy theo chu kỳ cố định. |
| Loại `lua` | Script Lua chạy trong sandbox. Nhận bảng `payload` và có thể trả về một bảng làm kết quả. |
| Loại `sys_model` | Thao tác khai báo (create, write, unlink, find_or_create) trên bất kỳ model nào, không cần code. |
| Loại `go_handler` | Trình xử lý đã biên dịch do module khác đăng ký theo **Handler Key** (Khóa trình xử lý). |
| Payload | Dữ liệu đầu vào. `Values` của action là payload mặc định; lần chạy thủ công có thể ghi đè từng khóa. |

### Các trường của Action

| Trường (nhãn UI) | Ý nghĩa |
| --- | --- |
| Name (Tên) | Tên dễ đọc (bắt buộc) |
| Action Type (Loại hành động) | Lua Script, Sys Model hoặc Go Handler (bắt buộc) |
| Description (Mô tả) | Mục đích nghiệp vụ |
| Active | Action không hoạt động sẽ không được node Lua của workflow dùng |
| Lua Code (Mã Lua) | Script (loại Lua) |
| Model (Mô hình) / Operation (Thao tác) | Model đích và Create, Write, Unlink hoặc Find Or Create (loại Sys Model) |
| Domain (Miền) | Bộ lọc chọn bản ghi đích cho write, unlink, find_or_create |
| Values (Giá trị) | Giá trị trường khi create/write, hoặc payload mặc định cho Lua và Go Handler |
| Handler Key (Khóa trình xử lý) | Tên handler đã đăng ký (loại Go Handler) |

### Các trường của Cron

| Trường (nhãn UI) | Ý nghĩa |
| --- | --- |
| Name (Tên) | Tên lịch trình (bắt buộc) |
| Action (Hành động) | Action sẽ chạy (bắt buộc) |
| Interval Seconds (Khoảng thời gian, giây) | Chu kỳ chạy tính bằng giây, tối thiểu 30 (bắt buộc) |
| Next Call (Lần gọi tiếp theo) | Thời điểm đến hạn kế tiếp; mặc định là lúc tạo |
| Max Failures (Số lần thất bại tối đa) | Số lần thất bại liên tiếp trước khi tự vô hiệu hóa, mặc định 5 |
| Fail Count (Số lần thất bại) | Số lần thất bại liên tiếp hiện tại (do hệ thống cập nhật) |
| Last Run Status (Trạng thái lần chạy gần nhất) | Ok hoặc Error (do hệ thống cập nhật) |
| Active | Lịch có chạy hay không |

## 4. Vị trí trong giao diện

| Menu | Mở ra |
| --- | --- |
| Automation (Tự động hóa, menu cấp cao nhất) | Menu cha |
| Automation > Actions (Hành động) | Danh sách và biểu mẫu `automation.action` |
| Automation > Crons (Cron) | Danh sách và biểu mẫu `automation.cron` |

## 5. Các tác vụ từng bước

### Tạo và chạy thử action Lua
1. Mở **Automation > Actions** và bấm New.
2. Nhập Name và đặt **Action Type** là Lua Script.
3. Ở tab **Lua Script** viết script. Biến toàn cục có sẵn: `payload` và bảng `base` (`base.call(model, method, kwargs, ids)`, `base.log(text)`, `base.json_encode`, `base.json_decode`). Trả về một bảng để dùng làm kết quả.
4. Có thể nhập đầu vào mặc định ở **Values**, rồi lưu.
5. Chạy thủ công qua phương thức `run` (xem mục 7) để xem trước chính xác điều cron sẽ làm.

### Tạo action thao tác model không cần code
1. Tạo action mới, **Action Type** = Sys Model.
2. Chọn **Model** và **Operation**.
3. Với Write, Unlink và Find Or Create hãy điền **Domain**; điền **Values** cho dữ liệu cần áp dụng.
4. Lưu.

### Lập lịch cho action
1. Mở **Automation > Crons** và bấm New.
2. Chọn Name, **Action** và **Interval Seconds** (từ 30 trở lên).
3. Giữ **Max Failures** là 5 trừ khi cần khác, rồi lưu.
4. Theo dõi **Next Call**, **Last Run Status** và **Fail Count** trong danh sách.

### Bật lại cron đã bị vô hiệu hóa
1. Khắc phục nguyên nhân (chạy thủ công action để kiểm tra).
2. Mở cron, bật lại **Active** và đặt **Fail Count** về 0, rồi lưu.

## 6. Quản trị và cấu hình

- **Quyền truy cập**: chỉ `base.group_system` có quyền đọc/ghi/tạo/xóa trên cả hai model.
- **Dữ liệu seed**: job lập lịch kernel "Automation: Dispatch Due Crons" (handler `automation.dispatch_due_crons`, 30 giây một lần, tối đa 5 lần lỗi) được cài sẵn. Không nên vô hiệu hóa, nếu không không cron nào chạy.
- **Phạm vi công ty**: action lấy công ty của người tạo; cron luôn kế thừa công ty của action và không thể đặt riêng.
- **Cách điều phối**: mỗi lượt lấy tối đa 16 cron đến hạn, đẩy ngay Next Call lên một chu kỳ, chạy action rồi ghi Ok hoặc Error. Nếu sự cố xảy ra, lịch chỉ bị trễ tối đa một chu kỳ thay vì chạy hai lần.
- **Chính sách lỗi**: mỗi lần lỗi tăng Fail Count thêm 1; đạt Max Failures thì cron chuyển sang không hoạt động. Thành công sẽ đặt lại Fail Count về 0.
- **Sandbox Lua**: chỉ nạp thư viện base, table, string, math; các hàm đọc file và nạp mã (`dofile`, `load`, `require`, v.v.) bị gỡ bỏ. Mỗi lần chạy giới hạn 30 giây. `base.call` chạy với quyền của người khởi chạy (người dùng với lần chạy thủ công, hệ thống với cron) và báo lỗi khi thất bại trừ khi bọc trong `pcall`.

## 7. Điểm tích hợp API và tự động hóa

| Điểm tích hợp | Mục đích |
| --- | --- |
| RPC `automation.action` `run` (theo id, `payload` tùy chọn) | Chạy action ngay, cùng đường xử lý với cron |
| RPC `create` / `write` trên cả hai model | Tạo và cập nhật có kiểm tra hợp lệ |
| MCP `automation.record.search_read`, `.read`, `.create`, `.write`, `.unlink` | CRUD bản ghi giới hạn trong hai model automation; `create` yêu cầu `idempotency_key` |
| MCP `automation.action.run` (`action_id`, `payload` tùy chọn) | Chạy thủ công |
| Go: `RegisterGoHandler(key, fn)` | Cho phép module công bố handler để chọn trong action loại Go Handler |
| Node Lua của workflow | Có thể chạy action Lua đã lưu theo id hoặc external id của seed; action không hoạt động hoặc không phải Lua sẽ bị từ chối |

## 8. Xử lý sự cố và câu hỏi thường gặp

| Hiện tượng | Nguyên nhân và cách xử lý |
| --- | --- |
| "interval_seconds must be at least 30" | Dùng giá trị từ 30 trở lên |
| "lua_code is required" / "model is required" / "handler_key is required" | Điền trường bắt buộc theo Action Type đã chọn |
| "go_handler ... is not registered" | Handler Key sai chính tả hoặc module sở hữu chưa được cài |
| "domain is required for write/unlink" | Bắt buộc có Domain để action không bao giờ tác động lên mọi bản ghi |
| Cron ngừng chạy | Kiểm tra Active và Fail Count; cron đã tự vô hiệu hóa sau Max Failures |
| Cron không bao giờ chạy | Xác nhận job "Automation: Dispatch Due Crons" đang hoạt động và Next Call đã qua |
| Script lỗi timeout | Script dừng sau 30 giây; hãy giảm khối lượng công việc mỗi lần chạy |

## 9. Giới hạn và lưu ý

- Chu kỳ tối thiểu 30 giây; chỉ hỗ trợ lập lịch theo khoảng cố định (không có biểu thức cron hay lịch).
- Mỗi lượt chỉ lấy tối đa 16 cron; hàng đợi lớn hơn sẽ được xử lý dần qua nhiều lượt.
- Cron chạy với payload rỗng; giá trị mặc định lấy từ Values của action.
- Action chạy qua ORM thông thường nên thay đổi được ghi vào các trường kiểm toán chuẩn (created/updated).
- Đổi loại action sau này không kiểm tra lại mọi trường bắt buộc; hãy xác minh bằng cách chạy thủ công.
