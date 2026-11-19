# Knowledge: hướng dẫn cho người dùng và quản trị viên

## 1. Mục đích và đối tượng sử dụng

`knowledge` là module tài liệu markdown của Base HUB. Module hiển thị tài liệu sản phẩm có sẵn và cho phép quản trị viên tự viết, tổ chức tài liệu markdown của mình ngay trong ứng dụng.

| Đối tượng | Việc thực hiện với module |
| --- | --- |
| Quản trị viên (superadmin, `base.group_system`) | Thấy mục KnowLedge ở thanh bên, đọc tài liệu, tạo, sửa, đổi tên và xóa tài liệu của mình |
| Nhà tích hợp và AI agent | Đọc, tìm kiếm và quản lý tài liệu qua các tool MCP `knowledge.*` hoặc các route `/md` |

## 2. Điều kiện tiên quyết và cài đặt

- Chỉ phụ thuộc `base`. Không phải module lõi: `installable: true`, `auto_install: false`. Cài đặt từ danh sách Apps.
- Nếu module chưa được cài cho một tenant, menu tài liệu sẽ trống (không báo lỗi).
- Tài liệu có thể ghi được lưu dưới dạng tệp trên máy chủ, trong thư mục chia sẻ của tenant. Tên thư mục con lấy từ khóa cấu hình `knowledge.publicDocsPath` (mặc định `public_docs`).

## 3. Khái niệm chính

| Khái niệm | Ý nghĩa |
| --- | --- |
| Document (tài liệu) | Một tệp markdown `.md`. Chỉ chấp nhận phần mở rộng `.md` |
| Logical path | Mọi đường dẫn tài liệu bắt đầu bằng namespace, ví dụ `public/notes/todo.md` |
| Namespace `base` | Tài liệu tích hợp sẵn trong sản phẩm. Chỉ đọc. Chỉ hiển thị trên tenant Home |
| Namespace `user_guide` | Hướng dẫn người dùng tích hợp sẵn, nằm trong gói tài liệu. Chỉ đọc, hiển thị cho mọi tenant |
| Namespace `public` | Vùng duy nhất có thể ghi. Tách riêng theo tenant. Cho phép tạo thư mục |
| Name / label | Nhãn hiển thị tùy chọn trong menu tài liệu |
| Search index | Chỉ mục toàn văn của nội dung tài liệu, dùng cho tìm kiếm theo nội dung |

## 4. Vị trí trong giao diện

| Vị trí | Chức năng |
| --- | --- |
| Mục "KnowLedge" ở thanh bên | Cây thư mục và tài liệu; nhấp để đọc. Chỉ người dùng superadmin thấy |
| Trang xem/soạn tài liệu | Hiển thị tài liệu; tạo, sửa, đổi tên, xóa trong namespace `public` |
| `GET /md/menu.json` | Cây tài liệu dạng JSON |
| `GET /md/<namespace>/<path>.md` | Nội dung markdown gốc của tài liệu |

## 5. Các tác vụ từng bước

### Đọc một tài liệu
1. Mở mục KnowLedge ở thanh bên.
2. Mở rộng các thư mục và nhấp vào tài liệu.

### Tạo tài liệu
1. Trong trình soạn thảo, chọn tạo tài liệu mới.
2. Nhập đường dẫn hoặc thư mục và tên tệp kết thúc bằng `.md`. Ứng dụng tự đặt tài liệu dưới `public/`.
3. Viết nội dung markdown và lưu. Nếu đường dẫn đã tồn tại sẽ báo lỗi "already exists".

### Sửa tài liệu
Mở tài liệu `public`, thay đổi nội dung và lưu. Tài liệu `base` và `user_guide` có sẵn không thể sửa.

### Đổi tên hoặc di chuyển tài liệu
Dùng chức năng đổi tên và nhập đường dẫn mới. Sẽ lỗi nếu đích đã tồn tại.

### Xóa tài liệu
Xóa tài liệu `public` từ trình soạn thảo. Thao tác xóa cả tệp và mục chỉ mục tìm kiếm của nó.

### Tìm tài liệu theo chủ đề (agent)
Dùng `knowledge.doc.search` để xếp hạng theo nội dung, hoặc `knowledge.doc.find` để tìm theo tên hay từ khóa.

## 6. Quản trị và cấu hình

- Truy cập: menu ở thanh bên bị ẩn với mọi người dùng đã đăng nhập không phải superadmin. Đây chỉ là ẩn menu, không phải kiểm tra quyền trên các route tài liệu `/md/*`.
- Lưu trữ: đặt `knowledge.publicDocsPath` (mặc định `public_docs`) để đổi tên thư mục trong thư mục chia sẻ của mỗi tenant. Sao lưu thư mục đó để sao lưu tài liệu.
- Seed: không có dữ liệu seed. Tài liệu `base` và `user_guide` đi kèm trong ứng dụng.
- Tìm kiếm: dùng Postgres full-text search khi có thể. Nếu cơ sở dữ liệu không cho phép (ví dụ không có quyền tạo index), tự động chuyển sang tệp chỉ mục JSON trong thư mục chia sẻ của tenant (`knowledge-search-index.json`).
- Độ mới của chỉ mục: một lần đồng bộ đầy đủ chạy một lần cho mỗi tiến trình và tenant ở lần tìm theo nội dung đầu tiên; các thao tác tạo, sửa, đổi tên, xóa sau đó cập nhật chỉ mục ngay.

## 7. Điểm tích hợp API và tự động hóa

| Điểm tích hợp | Mục đích |
| --- | --- |
| `GET /md/menu.json` | Cây tài liệu (trống với người dùng không phải superadmin và tenant chưa cài module) |
| `GET /md/*` | Markdown gốc của một tài liệu |
| `POST /md/api/files` | Tạo tài liệu `public` (201, hoặc 409 nếu đã tồn tại) |
| `PUT /md/api/files/*` | Ghi đè tài liệu `public` (404 nếu không có) |
| `POST /md/api/files/rename` | Di chuyển/đổi tên (404 không có, 409 đích đã tồn tại) |
| `DELETE /md/api/files/*` | Xóa tài liệu `public` (404 nếu không có) |
| MCP `knowledge.doc.list` | Liệt kê mọi tài liệu tenant được thấy |
| MCP `knowledge.doc.find` | Tìm theo tên hoặc từ khóa |
| MCP `knowledge.doc.search` | Xếp hạng theo độ liên quan của nội dung |
| MCP `knowledge.doc.read` | Đọc một tài liệu theo logical path |
| MCP `knowledge.doc.create` | Tạo mới; bắt buộc có `idempotency_key` để gọi lại an toàn |
| MCP `knowledge.doc.update` / `rename` / `delete` | Quản lý tài liệu `public` |

Các lệnh ghi thành công trả về `{"result":{"path": ...}}`.

## 8. Xử lý sự cố và câu hỏi thường gặp

| Vấn đề | Kiểm tra |
| --- | --- |
| Không thấy mục KnowLedge | Bạn không phải superadmin, hoặc module chưa được cài cho tenant này |
| "markdown files must use the .md extension" | Thêm `.md` vào tên tệp |
| 409 khi tạo hoặc đổi tên | Đã có tài liệu ở đường dẫn đó; chọn đường dẫn khác |
| Không sửa hoặc xóa được tài liệu | Tài liệu thuộc `base` hoặc `user_guide` (chỉ đọc). Sao chép nội dung sang `public/` |
| Tìm kiếm không thấy tài liệu mới | Dùng `knowledge.doc.find` để khớp theo tên; tìm theo nội dung được cập nhật khi ghi và ở lần đồng bộ kế tiếp |
| Không thấy tài liệu `base` | Chúng chỉ hiển thị trên tenant Home |

## 9. Giới hạn và lưu ý

- Chỉ hỗ trợ tệp markdown; module không quản lý tệp đính kèm hay hình ảnh.
- Chỉ được ghi trong namespace `public`. Đường dẫn ngoài namespace này bị từ chối.
- Tài liệu là tệp thuần, không phải bản ghi: không có phân quyền theo từng tài liệu, lịch sử hay phiên bản.
- Bảng chỉ mục Postgres `tbl_knowledge_document` là nội bộ, không phải model hiển thị cho người dùng.
