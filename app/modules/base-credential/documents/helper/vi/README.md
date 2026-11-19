# base-credential: Hướng dẫn cho người dùng và quản trị viên

## 1. Mục đích và đối tượng sử dụng

`base-credential` lưu các thông tin xác thực mà Base HUB dùng để đăng nhập vào hệ thống khác. Module mã hóa bí mật, gọi endpoint xác thực từ xa để lấy access token và tự động làm mới token trước khi hết hạn. Workflow tham chiếu thông tin xác thực theo tên (`@cred:<name>`) và luôn nhận token còn hiệu lực thay vì bí mật tĩnh.

| Đối tượng | Việc thực hiện tại đây |
| --- | --- |
| Quản trị hệ thống | Tạo và duy trì thông tin xác thực, thử đăng nhập, kiểm tra trạng thái làm mới |
| Người xây dựng workflow | Tham chiếu `@cred:<name>` trong tham số action của workflow |

## 2. Điều kiện tiên quyết và cài đặt

- Phụ thuộc vào `base` và `base-automation` (tác vụ làm mới do bộ lập lịch automation gọi).
- Luôn được cài (`auto_install`); không thể tắt hoặc gỡ cài đặt. Module `workflow` phụ thuộc vào module này.
- Chỉ người dùng thuộc `base.group_system` mới xem và sửa được thông tin xác thực.

## 3. Khái niệm chính

| Khái niệm | Ý nghĩa |
| --- | --- |
| Thông tin xác thực (`sys.credential`) | Một kết nối có tên tới hệ thống khác |
| Tên | Tên tham chiếu duy nhất, dùng dạng `@cred:<name>` |
| Loại thông tin xác thực | Cách đăng nhập, xem bảng bên dưới |
| Trạng thái token | Access token, refresh token và Thời gian hết hạn hiện tại (mã hóa khi lưu, không hiển thị lại sau khi lưu) |
| Làm mới trước (giây) | Thời điểm gia hạn token trước khi hết hạn (mặc định 300) |
| Lần thử cuối | Trạng thái (Ok hoặc Error), thông báo lỗi và thời gian của lần đăng nhập gần nhất |

| Loại | Các trường cần điền |
| --- | --- |
| `rbac` (RBAC) | URL xác thực, Tên đăng nhập, Mật khẩu |
| `oidc_client` (OIDC Client Credentials) | URL xác thực, Client ID, Client Secret, Phạm vi (tùy chọn) |
| `oidc_password` (OIDC Password) | URL xác thực, Tên đăng nhập, Mật khẩu, Client ID, Client Secret, Phạm vi (tùy chọn) |
| `apikey` (API Key) | Chỉ Mã truy cập / API Key; không cần URL xác thực; không hết hạn |

## 4. Vị trí trong giao diện

| Menu | Đường dẫn |
| --- | --- |
| Admin > Credentials (Thông tin xác thực) | `/web/sys.credential` |

Cột danh sách: Tên, Loại thông tin xác thực, URL xác thực, Thời gian hết hạn, Trạng thái đăng nhập gần nhất, Hoạt động. Các nhóm trên form: Kết nối, Mật khẩu RBAC / OIDC, Ứng dụng khách OIDC, Trạng thái token và Lần thử cuối.

## 5. Các tác vụ từng bước

### Tạo thông tin xác thực
1. Mở Admin > Credentials và tạo bản ghi mới.
2. Nhập Tên duy nhất (ví dụ `crm-api`); workflow sẽ tham chiếu theo tên này.
3. Chọn Loại thông tin xác thực và điền các trường ở bảng trên. URL xác thực bắt buộc với mọi loại trừ API Key.
4. Lưu. Bí mật được mã hóa và sẽ không hiển thị lại.

### Thử đăng nhập
Với loại OIDC, bấm Thử ở đầu form. Nhóm Lần thử cuối sẽ hiện Ok hoặc Error, thông báo và thời gian. Với API Key, dán khóa vào Mã truy cập / API Key.

### Dùng trong workflow
Trong tham số action của workflow, viết `@cred:<name>`. Khi chạy, giá trị được thay bằng token hiện tại đã được tự động làm mới.

### Đổi bí mật
Nhập giá trị mới vào trường bí mật và lưu. Để trống trường bí mật thì giá trị đã lưu được giữ nguyên.

### Vô hiệu hóa
Bỏ tick Hoạt động. Tác vụ làm mới sẽ bỏ qua bản ghi này.

## 6. Quản trị và cấu hình

| Mục | Chi tiết |
| --- | --- |
| Quyền truy cập | `base.group_system`: đọc, ghi, tạo, xóa |
| Dữ liệu seed | Automation action "Credential: Refresh Due Tokens" và automation cron cùng tên, mặc định bật, chạy mỗi 60 giây (tối đa 5 lần lỗi) |
| Mã hóa | Mật khẩu, Client Secret, Access Token và Refresh Token được mã hóa khi lưu và không bao giờ trả về qua API |
| Thời điểm làm mới | Theo từng bản ghi qua Làm mới trước (giây); bộ lập lịch kiểm tra mỗi 60 giây |

## 7. Điểm tích hợp API và tự động hóa

| Điểm tích hợp | Mục đích |
| --- | --- |
| Method `login` trên `sys.credential` | Xác thực lại ngay, giống nút Thử |
| `@cred:<name>` trong workflow | Được thay bằng token còn hiệu lực |
| Automation handler `credential.refresh_due_credentials` | Gia hạn token sắp hết hạn |
| MCP `credential.record.search_read`, `read`, `create`, `write`, `unlink` | Quản lý thông tin xác thực; bí mật không bao giờ trả về; `create` cần `idempotency_key` |

## 8. Xử lý sự cố và câu hỏi thường gặp

| Sự cố | Nguyên nhân thường gặp và cách xử lý |
| --- | --- |
| Trạng thái đăng nhập gần nhất = Error | Đọc Lỗi đăng nhập gần nhất: sai URL, tên đăng nhập, mật khẩu hoặc client secret, hoặc hệ thống từ xa lỗi. Sửa rồi bấm Thử |
| Token không được gia hạn | Bản ghi không Hoạt động, hoặc cron "Credential: Refresh Due Tokens" đang tắt hoặc đã chạm giới hạn lỗi |
| Lưu lỗi: thiếu Auth URL | Mọi loại trừ API Key đều cần URL xác thực |
| Lưu lỗi: loại không được hỗ trợ | Dùng một trong `rbac`, `oidc_client`, `oidc_password`, `apikey` |
| Không thấy mật khẩu đã lưu | Đúng thiết kế; nhập lại để thay đổi |

## 9. Giới hạn và lưu ý

- Nút Thử chỉ hiện với loại OIDC Client và OIDC Password; các loại khác dùng method `login`.
- Thông tin xác thực API Key không hết hạn và không bao giờ được làm mới.
- Tên phải duy nhất để `@cred:<name>` không mơ hồ.
- Module không thể gỡ cài đặt vì workflow phụ thuộc vào nó.
