# base-auth: Hướng dẫn cho người dùng và quản trị viên

## 1. Mục đích và đối tượng sử dụng

`base-auth` là máy chủ ủy quyền OAuth 2.0 tích hợp sẵn của Base HUB. Module phát hành access token đã ký (RS256 JWT) và refresh token xoay vòng, giúp con người, dịch vụ, AI agent và ứng dụng bên thứ ba gọi `/rpc` và `/mcp` an toàn. Nếu bật nhà cung cấp OIDC bên ngoài (Casdoor, Keycloak, ...) qua `AuthService.Enable`, cùng một hợp đồng token sẽ được chuyển cho nhà cung cấp đó.

| Đối tượng | Việc thực hiện tại đây |
| --- | --- |
| Quản trị hệ thống | Đăng ký OAuth client, xoay vòng client secret, xem và thu hồi token |
| Nhà tích hợp / lập trình viên | Lấy token qua `/oauth/token` và `/oauth/authorize` |
| Người dùng cuối | Đăng nhập trên trang đăng nhập của trình duyệt khi ứng dụng ngoài yêu cầu truy cập |

## 2. Điều kiện tiên quyết và cài đặt

- Phụ thuộc vào `base`. Module có `auto_install`: được cài tự động, thuộc nhóm module gần kernel.
- Không cần cấu hình thủ công để dùng đăng nhập bằng mật khẩu. Khóa ký được tạo ở lần dùng đầu tiên và lưu trong tham số hệ thống.
- Chỉ người dùng thuộc nhóm `base.group_system` mới quản lý được các màn hình dữ liệu.
- Thời hạn token lấy từ cấu hình máy chủ: access token mặc định 15 phút, refresh token mặc định 30 ngày (`AccessTokenTTLMinutes`, `RefreshTokenTTLDays`).

## 3. Khái niệm chính

| Khái niệm | Model | Giải thích đơn giản |
| --- | --- | --- |
| OAuth Client (Ứng dụng khách OAuth) | `sys.oauth_client` | Bên gọi đã đăng ký (dịch vụ, tự động hóa, GPT Action, AI agent) với client ID và secret riêng. Thay thế API key tĩnh dùng chung trước đây |
| OAuth Token (Mã thông báo OAuth) | `sys.oauth_refresh_token` | Refresh token phát kèm mỗi access token. Lưu dạng băm, xoay vòng sau mỗi lần dùng. Các dòng cùng Family ID thuộc cùng một chuỗi đăng nhập |
| Mã ủy quyền | `sys.oauth_auth_code` | Mã dùng một lần, thời hạn ngắn do `/oauth/authorize` tạo, đổi một lần lấy token. Nội bộ, không có màn hình |
| Mã kích hoạt | `sys.activation_code` | Mã dùng một lần, thời hạn ngắn, để module khác (ví dụ kích hoạt thiết bị) gắn vào bản ghi. Nội bộ, không có màn hình |
| Access token | - | Bearer token ngắn hạn, gửi qua `Authorization: Bearer <token>` |
| Role / Is Superadmin | - | Vai trò mà token của client được gắn; Is Superadmin bỏ qua hoàn toàn kiểm tra vai trò |

## 4. Vị trí trong giao diện

| Menu | Đường dẫn | Hiển thị |
| --- | --- | --- |
| Admin > OAuth Clients (Ứng dụng khách OAuth) | `/web/sys.oauth_client` | Các client đã đăng ký |
| Admin > OAuth Tokens (Mã thông báo OAuth) | `/web/sys.oauth_refresh_token` | Các refresh token đã phát hành |

Cột danh sách OAuth Clients: Client ID, Tên dịch vụ, Vai trò, Là siêu quản trị, Hoạt động. Bộ lọc: Hoạt động, Đã lưu trữ; nhóm theo Vai trò.
Cột danh sách OAuth Tokens: Người dùng, Tên cơ sở dữ liệu, ID họ, Phát hành lúc, Hết hạn lúc, Thu hồi lúc. Bộ lọc: Đã thu hồi; nhóm theo Cơ sở dữ liệu hoặc Người dùng.

## 5. Các tác vụ từng bước

### Đăng ký OAuth client
1. Mở Admin > OAuth Clients và tạo bản ghi mới.
2. Nhập Client ID (định danh công khai) và Tên dịch vụ.
3. Chọn Vai trò, hoặc chỉ tick Là siêu quản trị khi bên gọi cần toàn quyền.
4. Ở tab URI chuyển hướng, thêm chính xác các URL callback nếu client dùng luồng authorization code trên trình duyệt.
5. Lưu, rồi bấm Tạo Client Secret. Hãy sao chép secret ngay: chỉ hiện một lần, hệ thống chỉ lưu bản băm.

### Xoay vòng client secret
Mở client và bấm Tạo Client Secret lần nữa. Secret cũ mất hiệu lực ngay; hãy cập nhật hệ thống gọi.

### Vô hiệu hóa hoặc xóa client
Bỏ tick Hoạt động (bản ghi hiện dải Đã lưu trữ) để chặn mà vẫn giữ lịch sử, hoặc xóa bản ghi để loại bỏ.

### Lấy token (nhà tích hợp)
1. Dịch vụ với dịch vụ: `POST /oauth/token` với `grant_type=client_credentials`, `client_id`, `client_secret`.
2. Đăng nhập người dùng: `grant_type=password` với `username`, `password`, tùy chọn `db`.
3. Gia hạn: `grant_type=refresh_token` kèm refresh token. Token cũ bị thay thế.
4. Ứng dụng trình duyệt: chuyển người dùng tới `GET /oauth/authorize` (`response_type=code`, tùy chọn PKCE `code_challenge`), rồi đổi code bằng `grant_type=authorization_code`.
5. Gọi API với `Authorization: Bearer <access_token>`.

### Kiểm tra hoặc thu hồi token
Mở Admin > OAuth Tokens, lọc Đã thu hồi hoặc nhóm theo Người dùng. Các dòng ở chế độ chỉ đọc; dùng `POST /oauth/revoke` để thu hồi token. Có thể xóa dòng khi cần.

## 6. Quản trị và cấu hình

| Mục | Chi tiết |
| --- | --- |
| Quyền truy cập | Nhóm `base.group_system` có đủ quyền trên OAuth Clients; trên OAuth Tokens được đọc, ghi, xóa nhưng không tạo (token chỉ do máy chủ phát hành) |
| Dữ liệu seed | Không có |
| Bí mật | Client secret băm bcrypt; refresh token và mã băm SHA-256; mã kích hoạt dùng HMAC chỉ máy chủ biết |
| Giới hạn đăng nhập | Đăng nhập mật khẩu bị giới hạn tốc độ: chờ tăng dần sau 5 lần sai (từ 30 giây đến 15 phút) và 30 lần thử mỗi phút theo IP. Trạng thái lưu trong bộ nhớ (một instance) |
| Nhà cung cấp ngoài | Đặt `AuthService.Enable` để chuyển cho Casdoor, Keycloak hoặc OIDC khác |

## 7. Điểm tích hợp API và tự động hóa

| Điểm tích hợp | Mục đích |
| --- | --- |
| `POST /oauth/token` (và `/auth/token`) | Các grant: `password`, `refresh_token`, `client_credentials`, `authorization_code`; body dạng form hoặc JSON |
| `GET`/`POST /oauth/authorize` | Đăng nhập và ủy quyền trên trình duyệt (`response_type=code` hoặc `token`) |
| `POST /oauth/revoke` | Thu hồi token |
| `GET /oauth/me` | Thông tin phiên hiện tại |
| Method `generate_client_secret` trên `sys.oauth_client` | Xoay vòng secret; trả bản rõ một lần |
| MCP `auth.record.search_read`, `read`, `create`, `write`, `unlink` | Quản lý `sys.oauth_client`; secret không bao giờ trả về trừ lúc tạo; `create` cần `idempotency_key` |

Lỗi của `/oauth/token` theo RFC 6749: `{"error": "...", "error_description": "..."}`.

## 8. Xử lý sự cố và câu hỏi thường gặp

| Sự cố | Nguyên nhân thường gặp và cách xử lý |
| --- | --- |
| `invalid_client` | Sai Client ID hoặc secret, hoặc client không Hoạt động. Tạo lại secret |
| `invalid_grant` | Code hoặc refresh token đã hết hạn, đã dùng hoặc bị thu hồi. Đăng nhập lại |
| `unsupported_grant_type` | Dùng một trong bốn loại grant được hỗ trợ |
| Lỗi chuyển hướng ở `/oauth/authorize` | `redirect_uri` không nằm trong URI chuyển hướng của client (khớp chính xác) |
| Đăng nhập bị chặn tạm thời | Quá nhiều lần sai; chờ hết thời gian thử lại |
| Mất client secret | Không thể khôi phục; hãy tạo secret mới |

## 9. Giới hạn và lưu ý

- `scope` được nhận và trả lại nguyên vẹn nhưng không được áp dụng; quyền lấy từ Vai trò hoặc Là siêu quản trị của client.
- PKCE là tùy chọn: chỉ kiểm tra khi yêu cầu authorize có kèm challenge.
- Luồng implicit (`response_type=token`) không bao giờ cấp refresh token.
- Thông tin xác thực cũ `authtoken` và `mcp_api_key` vẫn hợp lệ trong giai đoạn chuyển đổi song song với Bearer token.
- Giới hạn tốc độ theo từng tiến trình; triển khai nhiều instance cần kho dùng chung.
