# Mô-đun Base: hướng dẫn cho người dùng và quản trị viên

## 1. Mục đích và đối tượng sử dụng

`base` là mô-đun lõi của Base HUB. Mọi mô-đun khác đều phụ thuộc vào nó. Mô-đun cung cấp:

- Dữ liệu chủ: công ty, liên hệ (partner), tài khoản ngân hàng của liên hệ, quốc gia, tỉnh/bang, phường/xã, nhóm quốc gia, tiền tệ và ngôn ngữ.
- Định danh và phân quyền: người dùng, nhóm (vai trò), đặc quyền nhóm, quyền truy cập mô hình và quy tắc bản ghi.
- Danh mục hệ thống (các mô hình `sys.*`): mô-đun, mô hình, trường, định danh ngoài, tham số cấu hình, chuỗi định danh, tệp đính kèm, báo cáo, nhật ký thay đổi.
- Danh mục giao diện (các mô hình `ui.*`): menu, khung nhìn, hành động cửa sổ, hành động client và loại khung nhìn.
- Các menu chung Settings, Admin, Document và các công cụ MCP để xem và chỉnh sửa dữ liệu bằng chương trình.

| Đối tượng | Việc thực hiện tại đây |
| --- | --- |
| Người dùng nội bộ (`Internal User`) | Xem liên hệ, công ty, quốc gia, tiền tệ, ngôn ngữ và định nghĩa menu/khung nhìn. |
| Quản trị viên cài đặt (`Settings`) | Quản lý người dùng, nhóm, quyền truy cập, quy tắc, chuỗi định danh, báo cáo, tệp đính kèm, định nghĩa giao diện và mô hình. |
| Siêu quản trị (`Technical Features`) | Toàn quyền trên mọi mô hình và mọi công ty. |
| Nhà tích hợp và AI agent | Dùng các công cụ MCP `base.*` hoặc JSON-RPC API. |

## 2. Điều kiện tiên quyết và cài đặt

| Mục | Giá trị |
| --- | --- |
| Tên mô-đun | `base` (phiên bản 0.1.0, nhóm Hidden) |
| Phụ thuộc | Không |
| Cài đặt | Được cài tự động như mô-đun lõi; không cài từ màn hình Mô-đun. |
| Dữ liệu khởi tạo | Nạp khi cài đặt và nâng cấp từ thư mục `data/` (xem mục 6). |

## 3. Khái niệm chính

| Khái niệm | Mô hình | Giải thích |
| --- | --- | --- |
| Công ty | `res.company` | Một pháp nhân hoặc chi nhánh. Công ty có thể có công ty mẹ; `Is Branch` đánh dấu chi nhánh. Mỗi công ty gắn với một liên hệ. |
| Liên hệ | `res.partner` | Mọi đối tượng liên hệ: cá nhân hoặc tổ chức (`Is Company`), có địa chỉ, quốc gia, ghi chú, tài khoản ngân hàng và tệp đính kèm. |
| Tài khoản ngân hàng | `res.partner.bank` | Tài khoản ngân hàng gắn với một liên hệ. |
| Người dùng | `base.users` | Người đăng nhập. Có login, liên hệ, nhóm chính, công ty chính, các công ty được phép, ngôn ngữ và múi giờ. |
| Nhóm | `res.groups` | Một vai trò. Nhóm có thể kế thừa (implied) nhóm khác. |
| Đặc quyền nhóm | `base.groups.privilege` | Tập đặc quyền có tên để các nhóm tham chiếu. |
| Quyền truy cập mô hình | `sys.model.access` | Theo nhóm và mô hình: quyền đọc, ghi, tạo, xóa. |
| Quy tắc bản ghi | `sys.rule` | Bộ lọc theo dòng (domain) trên một mô hình. Là quy tắc toàn cục hoặc áp dụng cho các nhóm được chọn. |
| Quốc gia, Tỉnh/Bang, Phường/Xã, Nhóm quốc gia | `res.country`, `res.country.state`, `res.country.ward`, `res.country.group` | Dữ liệu tham chiếu địa lý. |
| Tiền tệ và tỷ giá | `res.currency`, `res.currency.rate` | Các loại tiền tệ và tỷ giá. |
| Ngôn ngữ | `res.lang` | Ngôn ngữ giao diện cùng định dạng ngày, giờ, số. |
| Chuỗi định danh | `sys.sequence` | Bộ sinh số chứng từ (tiền tố, hậu tố, độ dài đệm, bước tăng, số kế tiếp). |
| Tham số cấu hình | `sys.config_param` | Thiết lập hệ thống dạng khóa và giá trị. |
| Key Values | `base.kvt` | Danh sách tra cứu dùng chung, ví dụ múi giờ. |
| Tệp đính kèm | `sys.attachment` | Tệp tải lên gắn với một bản ghi. |
| Báo cáo | `sys.report` | Định nghĩa báo cáo in được, gắn với mô hình và mẫu. |
| Định danh ngoài | `sys.model.data` | Tên ổn định (như `base.group_user`) trỏ tới một bản ghi. |
| UI Menu, View, Actions, View type | `ui.menu`, `ui.view`, `ui.actions.window`, `ui.actions.client`, `ui.view.type` | Các định nghĩa tạo nên màn hình bạn thấy. |

### Các nhóm có sẵn

| Nhóm | Nhãn | Ý nghĩa |
| --- | --- | --- |
| `base.group_no_one` | Technical Features | Siêu quản trị: mọi mô hình, mọi bản ghi, mọi công ty. |
| `base.group_user` | Internal User | Người dùng ứng dụng mặc định. |
| `base.group_multi_company` | Multi Companies | Internal User, mở rộng tới mọi công ty trong danh sách công ty được phép của người dùng, không chỉ công ty chính. |
| `base.group_system` | Settings | Multi Companies, cộng toàn quyền tạo/sửa/xóa trên các mô hình `ui.*` và `sys.*`. |

## 4. Vị trí trên giao diện

| Menu (EN / VI) | Đường dẫn | Ghi chú |
| --- | --- | --- |
| Main (Chính) > Company / Công ty | `/web/tbl.company` | Hiển thị với Multi Companies hoặc Technical Features. |
| Main > Partners / Liên hệ | `/web/tbl.partner` | |
| Main > Bank Accounts / Tài khoản ngân hàng | `/web/tbl.partner.bank` | |
| Settings (Cài đặt) > Group Privileges / Đặc quyền nhóm | `/web/base.groups.privilege` | Menu Settings: chỉ nhóm Settings hoặc Technical Features. |
| Settings > Users / Người dùng | `/web/base.users` | |
| Settings > Countries / Quốc gia | `/web/tbl.country` | |
| Settings > Country Groups / Nhóm quốc gia | `/web/tbl.country.group` | |
| Settings > Country States / Tỉnh/Bang | `/web/tbl.country.state` | |
| Settings > Country Wards | `/web/tbl.country.ward` | |
| Settings > Languages / Ngôn ngữ | `/web/tbl.lang` | |
| Settings > Groups / Nhóm | `/web/tbl.groups` | |
| Settings > Attachments / Tệp đính kèm | `/web/sys.attachment` | |
| Settings > Reports / Báo cáo | `/web/sys.report` | |
| Settings > Sequences / Chuỗi định danh | `/web/sys.sequence` | |
| Admin (Quản trị viên) > System Config Params / Tham số cấu hình hệ thống | `/web/sys.config_param` | |
| Admin > Key Values | `/web/base.kvt` | |
| Admin > Model Access / Quyền truy cập mô hình | `/web/sys.model.access` | |
| Admin > Record Rules / Các quy tắc bản ghi | `/web/sys.rule` | |
| Admin > Models, Model Fields, Modules, Module Categories | `/web/sys.model`, `/web/sys.model.field`, `/web/sys.module.module`, `/web/sys.module.category` | |
| Admin > External Identifiers, Model Relations, Operations, Constraints, Actions | `/web/sys.model.data`, `.relation`, `.operation`, `.constraint`, `.action` | |
| Admin > UI Client Actions, UI Window Actions, UI Menus, UI Views, UI View Types, UI View Custom | `/web/ui.actions.client`, `/web/ui.actions.window`, `/web/ui.menu`, `/web/ui.view`, `/web/ui.view.type`, `/web/ui.view.custom` | |
| Admin > Service Health Check / Kiểm tra tình trạng dịch vụ | `/web/system-health` | |
| Admin > Model Change Log / Nhật ký thay đổi mô hình | `/web/sys.model.log` | |
| Document (Tài liệu) > API Documentation, Instruction | `/web/apidoc`, `/web/instruction` | `Instruction` hiển thị tài liệu này. |

## 5. Các tác vụ từng bước

### Tạo liên hệ
1. Mở Main > Partners và bấm New.
2. Nhập tên, đánh dấu `Is Company` nếu là tổ chức, rồi điền nhóm Business và Profile (quốc gia, công ty, thông tin liên lạc).
3. Dùng các tab Relations, Bank Account, Notes và Attachments, sau đó Save.

### Tạo người dùng và đặt mật khẩu
1. Mở Settings > Users và bấm New.
2. Trong Identity nhập Name, Login, Email và Partner liên kết.
3. Ở tab Access chọn Group chính, các Companies được phép và Company chính.
4. Ở tab Preferences chọn Language và Timezone.
5. Save, sau đó bấm Change Password (nút chỉ hiện khi bản ghi đã lưu) và nhập mật khẩu mới.
6. Tùy chọn: bấm Generate API Key. Khóa chỉ hiển thị một lần, hãy sao chép ngay; hệ thống chỉ lưu bản băm.

### Vô hiệu hóa người dùng
Mở người dùng, bỏ chọn `Active` rồi lưu. Dùng bộ lọc `Not Active` để tìm người dùng không hoạt động.

### Thêm công ty hoặc chi nhánh
Main > Company > New. Nhập tên, tùy chọn `Parent Company` và đánh dấu `Is Branch`. Chi nhánh hiện ở tab Branches của công ty mẹ. Liên hệ tương ứng được đồng bộ tự động.

### Tạo chuỗi định danh
Settings > Sequences > New: nhập Name, Code, Prefix (ví dụ `DEMO/%(year)s/`), Suffix, Padding, Increment, Next Number và Implementation (ví dụ `no_gap`).

### Định nghĩa quy tắc bản ghi
Admin > Record Rules > New: chọn mô hình, nhập domain, chọn các nhóm (để trống nếu là quy tắc toàn cục) và các quyền mà quy tắc áp dụng.

### Cấp quyền truy cập mô hình
Admin > Model Access > New: chọn nhóm và mô hình, đánh dấu quyền đọc, ghi, tạo, xóa khi cần.

### Tìm bản ghi
Mọi danh sách đều có ô tìm kiếm, bộ lọc (ví dụ Active, Is Company) và nhóm theo (group by). Danh sách hỗ trợ sửa nhiều dòng (multi-edit).

## 6. Quản trị và cấu hình

- Kiểm soát truy cập có hai lớp: Model Access (nhóm có được dùng mô hình này không) và Record Rules (được thấy những dòng nào).
- `Internal User` chỉ được đọc dữ liệu chủ và định nghĩa giao diện. `Settings` có toàn quyền trên chúng. Người dùng nội bộ không đọc được `sys.model`, `sys.model.field` hay `sys.model.data`.
- Dữ liệu khởi tạo do mô-đun nạp:

| Dữ liệu | Nội dung |
| --- | --- |
| Nhóm | Bốn nhóm ở mục 3. |
| Người dùng | `admin` (Technical Features, công ty `company.main` và `company.base1`), `base1`, cùng các bản ghi không hoạt động `User template` và `BaseBot`. Hãy đổi mật khẩu mặc định ngay lần đăng nhập đầu. |
| Dữ liệu tham chiếu | Quốc gia, tỉnh/bang, phường/xã, tiền tệ, hơn 90 ngôn ngữ và múi giờ. |
| Chuỗi định danh | `demo.document` (tiền tố `DEMO/%(year)s/`, padding 5). |
| Tác vụ định kỳ | Platform Autovacuum (hằng ngày), Idempotency Store GC (30 phút/lần), Attachment Blob GC (hằng ngày). |
| Báo cáo | `Demo User Summary` (A4, mô hình `base.users`). |
| Quy tắc bản ghi | Các quy tắc cấp cho nhóm `Settings` quyền trên các mô hình metadata `sys.*`. |

- Bản dịch: nhãn giao diện tiếng Việt lấy từ `i18n/vi_vn.po`. Người dùng thấy giao diện theo ngôn ngữ đặt trong phần tùy chọn của mình.

## 7. Điểm tích hợp API và tự động hóa

Mô-đun đăng ký các công cụ MCP. Chúng tuân theo cùng quyền truy cập và quy tắc bản ghi như giao diện.

| Công cụ | Mục đích |
| --- | --- |
| `base.model.list`, `base.model.describe`, `base.model.fields_get`, `base.model.methods` | Khám phá mô hình, trường và phương thức có thể gọi. |
| `base.record.search_read`, `base.record.read` | Truy vấn và đọc bản ghi. |
| `base.record.create`, `base.record.write`, `base.record.unlink` | Tạo, cập nhật, xóa bản ghi. |
| `base.record.call_method` | Gọi phương thức của mô hình. |
| `base.model.create_custom`, `add_field`, `rename_field`, `delete_field`, `create_ui`, `publish`, `unpublish` | Xây dựng và xuất bản mô hình tùy biến. |
| `base.batch.execute` | Chạy nhiều lệnh gọi công cụ trong một giao dịch; lỗi thì hoàn tác tất cả. Không lồng nhau được. |

Người dùng còn có các hành động đặt mật khẩu và tạo API key (`action_set_password`, `action_generate_api_key`) qua phương thức của mô hình. Trang API Documentation liệt kê toàn bộ endpoint.

## 8. Xử lý sự cố và câu hỏi thường gặp

| Vấn đề | Nguyên nhân và cách xử lý |
| --- | --- |
| Không thấy menu | Settings và Admin cần `Settings` hoặc `Technical Features`; Company cần `Multi Companies`. Nhờ quản trị viên thêm nhóm. |
| Không lưu được người dùng | Người dùng đang hoạt động cần login, mật khẩu hợp lệ và nhóm. Đặt mật khẩu bằng Change Password. |
| Không thấy nút Change Password hoặc Generate API Key | Hãy lưu bản ghi trước. |
| Mất API key | Không thể khôi phục. Hãy tạo khóa mới. |
| Người dùng không thấy dữ liệu của công ty khác | Thêm công ty vào Companies được phép của người dùng và cấp `Multi Companies`. |
| Bị từ chối truy cập một mô hình | Thêm hoặc sửa dòng trong Model Access, hoặc kiểm tra Record Rules. |
| Giao diện không phải tiếng Việt | Đặt Language ở tab Preferences của người dùng rồi tải lại trang. |

## 9. Giới hạn và lưu ý

- Sửa các bản ghi `sys.*` và `ui.*` ảnh hưởng toàn hệ thống. Hãy thử trên cơ sở dữ liệu không phải production trước.
- Tài khoản `admin` khởi tạo chỉ dùng cho thiết lập ban đầu; hãy đổi trước khi vận hành thật.
- Mô-đun thuộc nhóm Hidden và là nền tảng của mọi mô-đun khác.
- Bản ghi khởi tạo được đánh dấu cập nhật cưỡng bức có thể bị đặt lại khi nâng cấp; hãy giữ cấu hình tùy chỉnh trong bản ghi riêng của bạn.
