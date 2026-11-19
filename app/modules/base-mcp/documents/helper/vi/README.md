# Base MCP: hướng dẫn cho người dùng và quản trị viên

## 1. Mục đích và đối tượng sử dụng

Base MCP đưa một hệ thống Base HUB ra cho các AI agent thông qua Model Context Protocol (MCP). Module cung cấp một endpoint có xác thực là `/mcp`, nơi agent có thể khám phá công cụ và gọi chúng để đọc, thay đổi dữ liệu, luôn với quyền của người dùng đã xác thực.

| Đối tượng | Việc thực hiện |
| --- | --- |
| Người dùng cuối | Kết nối trợ lý AI (Claude, ChatGPT/OpenAI Responses, Codex hoặc client MCP bất kỳ) với tài khoản Base HUB. |
| Quản trị viên | Cài đặt module, tạo OAuth client hoặc người dùng cho agent, kiểm soát quyền qua nhóm và ACL. |
| Lập trình viên | Thêm nhóm công cụ từ module của mình và tích hợp client tùy biến. |

## 2. Điều kiện tiên quyết và cài đặt

| Mục | Chi tiết |
| --- | --- |
| Phụ thuộc | `base` |
| Danh mục | System |
| Cài đặt | Có thể cài nhưng không tự động: cài `base-mcp` từ màn hình Apps/Modules. |
| Thông tin xác thực | OAuth 2.0 bearer access token (của một người dùng đăng nhập hoặc một service client `sys.oauth_client`). |
| Giao thức | Streamable HTTP, phiên bản `2025-06-18`, chỉ trả về một phản hồi JSON. |

## 3. Khái niệm chính

| Khái niệm | Giải thích |
| --- | --- |
| Công cụ (Tool) | Một hành động agent có thể gọi, ví dụ `base.record.search_read`. |
| Nhóm công cụ | Các tool chung tiền tố: `base.*`, `website.*`, `workflow.*`, `knowledge.*`, `auth.*`, `automation.*`, `bus.*`, `calendar.*`, `credential.*` (mỗi nhóm do module tương ứng cung cấp). |
| Tenant | Cơ sở dữ liệu người gọi đang làm việc. Nhóm công cụ của một module chỉ xuất hiện nếu module đó được cài trong cơ sở dữ liệu này. |
| Phiên (Session) | `Mcp-Session-Id` tùy chọn do `initialize` cấp, hiệu lực 30 phút không hoạt động. Phiên không cấp quyền; quyền được kiểm tra lại ở mỗi lần gọi. |
| Idempotency key | `idempotency_key` do người gọi chọn, giúp gọi tạo mới an toàn khi thử lại (lưu 24 giờ). |
| Bridge | Script Node.js nhỏ cho các client chỉ chạy được máy chủ stdio cục bộ. |

## 4. Vị trí truy cập

Module không có menu. Module phục vụ các route HTTP sau.

| Route | Phương thức | Xác thực | Mục đích |
| --- | --- | --- | --- |
| `/mcp` | POST | Bearer | JSON-RPC: `initialize`, `tools/list`, `tools/call`, notification. |
| `/mcp` | GET | Bearer | Luôn trả 405 (không có luồng do server khởi tạo). |
| `/mcp` | DELETE | Bearer | Kết thúc phiên (cần `Mcp-Session-Id`). |
| `/mcp/tools` | GET | Bearer | Liệt kê các tool người gọi được dùng. |
| `/mcp/health` | GET | Không | Kiểm tra sẵn sàng. |
| `/mcp/bridge` | GET | Không | Tải `base-hub-mcp-bridge.js`. |

## 5. Các tác vụ từng bước

### Kết nối client MCP bằng URL từ xa
1. Trong client, thêm máy chủ MCP từ xa với URL `https://<your-host>/mcp`.
2. Cung cấp header `Authorization: Bearer <access token>`. Với hệ thống nhiều cơ sở dữ liệu, gửi thêm header tên cơ sở dữ liệu (`X-Database-Name`).
3. Client chạy `initialize` rồi `tools/list`. Bạn sẽ thấy các tool `base.*` cùng tool của các module đã cài.

### Kết nối qua stdio bridge
1. Tải `/mcp/bridge` và lưu lại. Cần Node.js 18 trở lên.
2. Trong client, cấu hình lệnh `node` cùng đường dẫn script.
3. Đặt biến môi trường `BASE_HUB_URL`, `BASE_HUB_MCP_KEY` (bearer token) và tùy chọn `BASE_HUB_DB_NAME`.

### Tạo bản ghi an toàn từ agent
1. Luôn đưa `arguments.idempotency_key` vào các lệnh gọi tạo mới.
2. Với `base.record.create` và `write`, truyền giá trị trường trong `arguments.values`.
3. Dùng `base.batch.execute` để chạy nhiều bước trong một transaction.

### Kiểm tra gateway
1. Mở `/mcp/health`; kết quả mong đợi là `healthy`.
2. Gọi `/mcp/tools` kèm token để xác nhận danh sách tool.

## 6. Quản trị và cấu hình

| Chủ đề | Chi tiết |
| --- | --- |
| Kiểm soát truy cập | Agent có đúng nhóm, ACL và record rule như người dùng đã xác thực. Không có scope MCP riêng. Để giới hạn agent, hãy cấp một người dùng hoặc OAuth client riêng với vai trò hạn chế. |
| API key | `X-API-Key` không được chấp nhận trên `/mcp`. Chỉ dùng OAuth 2.0 bearer token. |
| Xoay vòng quyền truy cập | Thu hồi hoặc tạo lại người dùng hay `sys.oauth_client` mà agent sử dụng. |
| Bộ nhớ đệm | Danh sách tool được cache theo cơ sở dữ liệu 30 giây và làm mới khi cài, gỡ hoặc reinit module. |
| Kiểm toán | Mỗi lần gọi tool được ghi log (người dùng, cơ sở dữ liệu, tool, model, method, kết quả, độ trễ), không bao giờ ghi tham số hay token. |
| Dữ liệu seed | Không có. |

## 7. API và điểm tích hợp tự động hóa

| Nhóm công cụ | Cách dùng thường gặp |
| --- | --- |
| `base.model.*`, `base.record.*` | Khám phá model, tìm kiếm, đọc, tạo, ghi, xóa, gọi method công khai (`base.record.call_method`). |
| `base.model.create_custom` và liên quan | Tạo, mở rộng, publish model tùy biến. |
| `base.batch.execute` | Nhiều bước trong một transaction (không lồng nhau). Bắt buộc `idempotency_key`; gọi lại cùng key sẽ trả lại toàn bộ kết quả cũ. |
| `website.*` | Site, trang, SEO, publish. |
| `workflow.*` | CRUD workflow, export và import. |
| `knowledge.*` | Ghi chú tri thức Markdown (namespace user có thể ghi). |
| `auth.*`, `automation.*`, `bus.*`, `calendar.*`, `credential.*` | Truy cập bản ghi và tool riêng theo từng module. |

Lưu ý: `search_read` trả 20 dòng mặc định và tối đa 200; trường văn bản hoặc JSON lớn hơn 4096 byte được thay bằng kích thước `<field>_bytes`; `arguments` có thể là đối tượng hoặc chuỗi JSON.

## 8. Xử lý sự cố và hỏi đáp

| Triệu chứng | Nguyên nhân và cách xử lý |
| --- | --- |
| 401 "authorization is required" | Thiếu header `Authorization`. |
| 401 "must use the Bearer scheme" hoặc "invalid bearer token" | Sai scheme, token hết hạn hoặc thuộc cơ sở dữ liệu khác. Lấy token mới. |
| 400 liên quan cơ sở dữ liệu | Thiếu hoặc sai tên cơ sở dữ liệu trong hệ thống nhiều cơ sở dữ liệu. |
| Thiếu nhóm công cụ trong danh sách | Module sở hữu chưa được cài trong cơ sở dữ liệu đó. |
| `model_not_found` hoặc `tool_not_found` | Tên sai hoặc bị tắt; phản hồi có thể kèm gợi ý "see also". |
| `MCP-Protocol-Version` không hỗ trợ | Client phải dùng `2025-06-18` hoặc bỏ header. |
| Tạo trùng sau khi thử lại | Thêm `idempotency_key`; không có sẽ tạo bản ghi trùng. |
| 405 khi GET `/mcp` | Bình thường. Hãy dùng POST. |

## 9. Giới hạn và lưu ý

- Chỉ có phản hồi JSON đơn; không đẩy streaming từ server. DeepSeek và Kimi cần một agent host hỗ trợ MCP đặt phía trước.
- Không có tự khám phá OAuth (RFC 8414) hay đăng ký client động: phải cấp token thủ công cho client.
- `search_read` không có phân trang bằng cursor.
- Model tùy biến chỉ hoạt động trong tiến trình đã publish cho đến khi khởi động lại; cần lưu ý khi chạy nhiều replica.
- Các hành động sinh bí mật (như client secret) chỉ có qua `base.record.call_method`; hãy xác nhận với người dùng trước.
