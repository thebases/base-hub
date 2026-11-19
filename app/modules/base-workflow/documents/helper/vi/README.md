# base-workflow: Hướng dẫn cho người dùng và quản trị viên

## 1. Mục đích và đối tượng sử dụng

`base-workflow` cho phép thiết kế, chạy và theo dõi các quy trình (workflow) tự động trong Base HUB. Một workflow là danh sách các bước (theo thứ tự hoặc theo dạng đồ thị) được kích hoạt bởi một trigger, ví dụ webhook, tin nhắn Kafka hoặc MQTT, hay thay đổi trên một bản ghi. Mỗi bước làm một việc: phân tích payload đầu vào, tạo hoặc tìm bản ghi, biến đổi dữ liệu, ký hoặc mã hóa, gọi endpoint bên ngoài, rẽ nhánh theo điều kiện, hoặc chạy script Lua.

| Đối tượng | Công việc |
| --- | --- |
| Người thiết kế tích hợp / quản trị viên | Xây dựng workflow, trigger, bước, credential; chạy thử và xuất/nhập |
| Vận hành / hỗ trợ | Theo dõi các lần chạy, xem lịch sử bước, thử lại các lần gửi đi bị lỗi |
| Lập trình viên | Gọi route webhook, RPC/MCP, viết Lua action |

Mọi người dùng nội bộ đều đọc được bản ghi của module; chỉ `base.group_system` được tạo, sửa hoặc xóa định nghĩa workflow (xem mục 6).

## 2. Điều kiện tiên quyết và cài đặt

| Mục | Chi tiết |
| --- | --- |
| Tên module | `base-workflow` (nhóm System, không tự động cài) |
| Phụ thuộc | `base`, `base-automation`, `base-credential`, `base-bus` |
| Cài đặt | Cài `base-workflow` từ màn hình Apps / module; các module phụ thuộc được cài kèm |
| Tùy chọn | Kafka hoặc MQTT broker cho trigger Kafka/MQTT; bước Lua cần một Lua action đã lưu trong Automation |

## 3. Khái niệm chính

| Khái niệm | Model | Giải thích đơn giản |
| --- | --- | --- |
| Workflow (Quy trình) | `base.workflow` | Định nghĩa: tên, `topic` duy nhất, phiên bản, cờ hoạt động, chế độ thực thi, nhóm truy cập tùy chọn |
| Action step (Bước hành động) | `base.workflow.action` | Một bước của workflow, dựa trên một mục danh mục, có tham số, timeout và chính sách thử lại riêng |
| Action catalog (Danh mục hành động) | `base.workflow.action.catalog` | Danh sách cố định các loại bước dựng sẵn (chỉ đọc trên giao diện) |
| Trigger | `base.workflow.trigger` | Thứ khởi động workflow: Webhook, Kafka, MQTT, Model Event hoặc TCP |
| Edge (Cạnh) | `base.workflow.edge` | Liên kết giữa hai bước (chỉ dùng ở chế độ DAG) |
| Run (Lần chạy quy trình) | `base.workflow.run` | Một lần thực thi cùng payload vào/ra, lịch sử bước và lịch thử lại |
| Outbound delivery (Giao hàng đi) | `base.workflow.outbound` | Một lần gửi HTTP/thông báo được xếp hàng bởi bước notify hoặc post_output_object, có số lần thử và thử lại |
| Credential | `base.workflow.credential` | Bí mật có tên (khóa HMAC, khóa AES, API key, mật khẩu, token), lưu dạng mã hóa |

Các loại bước dựng sẵn (catalog):

| Mã | Công dụng |
| --- | --- |
| `parse_payload` | Phân tích dữ liệu đầu vào thành đối tượng JSON |
| `flatten_json` | Làm phẳng JSON lồng nhau thành các khóa lá để dễ dùng placeholder `{{field}}` |
| `transform_data` | Tạo tài liệu JSON mới từ template nội tuyến |
| `crud_model` | Chạy một thao tác model (mặc định `find_or_create`) và gộp kết quả vào payload. `find_or_create` tìm rồi mới tạo: hai lần chạy cùng lúc có thể cùng tạo. Chỉ dùng trên model có ràng buộc unique cho các trường tìm kiếm (ví dụ `name` hoặc một cột tham chiếu đánh dấu `unique`) để lần tạo thứ hai báo lỗi thay vì tạo trùng, và đặt retry policy cho bước |
| `notify` | Xếp hàng một lần gửi JSON; bước thành công khi lần gửi đã được lưu, việc gửi và thử lại chạy nền (mục 5.7) |
| `post_output_object` | Gửi payload hiện tại và chờ phản hồi; mọi mã trạng thái ngoài 2xx làm bước thất bại (lỗi ghi mã trạng thái và phần đầu của body) |
| `hmac_action` | Tính chữ ký HMAC và thêm vào đầu ra (trường mặc định `signature`) |
| `hmac_verify` | Kiểm tra chữ ký HMAC nằm trong payload: HMAC Key (tham chiếu `@cred:`/`@syscred:`), Algorithm `sha256` hoặc `sha512`, Signature Field và Message Field (đường dẫn có dấu chấm trong payload; thông điệp dạng văn bản được ký nguyên văn, giá trị khác ký dạng JSON gọn với khóa đã sắp xếp), Encoding `hex` hoặc `base64`. Phép so sánh chạy thời gian hằng. Chữ ký sai hoặc thiếu làm bước thất bại ngay, không thử lại; khi đúng, payload đi tiếp kèm `_hmac_verified: true`. Thân yêu cầu được ký trong header thì do `hmac_secret` của trigger webhook kiểm tra (các bước không thấy header) |
| `aes_action` / `aes_decrypt_action` | Mã hóa vào / giải mã từ một trường đã đóng gói (trường mặc định `data`) |
| `condition` | Rẽ nhánh Yes / No theo biểu thức boolean. Toán tử lá: `exists`, `empty`, `equals`, `not_equals`, `in`, `not_in`, `gt`/`gte`/`lt`/`lte` (chỉ với số: trường văn bản như `"7"` là lỗi), `contains` (chuỗi con của trường văn bản hoặc phần tử của mảng), `regex` (cú pháp Go RE2, tối đa 512 ký tự); kết hợp bằng `all`, `any`, `not`. Khóa không xác định là lỗi, không bị bỏ qua |
| `lua_script` | Chạy một Lua action đã lưu trong Automation > Actions |
| `call_workflow` | Chạy một quy trình khác (Target Workflow, quy trình đã xuất bản) với payload hiện tại như một lần chạy con rồi tiếp tục với payload cuối của nó. Wait là `sync` và Payload Mapping là `pass_through` (hiện chỉ có các chế độ này). Quy trình con chạy phiên bản xuất bản HIỆN TẠI của quy trình đích (không cố định theo phiên bản của lần chạy gọi) trong cùng cơ sở dữ liệu với cùng danh tính, và lần chạy con hiển thị lần chạy gọi trong Parent Run. Người gọi đã đăng nhập (webhook, Run Now, replay) không thuộc Group ID của quy trình đích bị từ chối, không thử lại. Tối đa 5 cấp gọi lồng nhau; quy trình đã có trong chuỗi gọi bị từ chối (không đệ quy); cả hai trường hợp từ chối làm bước thất bại không thử lại. Quy trình con tự thử lại các bước tại chỗ và kết thúc Failed (không bao giờ Dead Letter) khi bỏ cuộc, làm bước gọi thất bại; Retry Policy của bước gọi sẽ khởi chạy một lần chạy con mới. Mỗi lần thử lại chạy lại TẤT CẢ các bước con, với cùng idempotency key như trước (`<step key gọi>:<call hoặc itemN>:<step key con>`), nên hệ thống khử trùng lặp theo key sẽ thấy lại cùng một yêu cầu. Hủy lần chạy gọi sẽ hủy lần chạy con; Max Run Seconds của lần chạy gọi và Timeout Seconds của bước cũng dừng nó, nên hãy đặt timeout đủ dài cho quy trình con. Max Concurrent Runs của quy trình đích được áp dụng: khi đầy, bước thất bại và được thử lại. Chạy thử (dry run) chỉ ghi lại lời gọi |
| `for_each` | Chạy một quy trình con đã xuất bản (Target Workflow) cho từng phần tử của mảng tại Items Path và ghi đầu ra của các lần chạy con, theo đúng thứ tự đầu vào, vào Output Field (mặc định `results`), kèm `_for_each: {total, failed}`. Phần tử là object được dùng nguyên làm payload của quy trình con; phần tử khác được gửi dạng `{"item": value}`. Max Concurrency từ 1 đến 8 (mặc định 1) lần chạy con cùng lúc; mảng dài hơn Max Items (mặc định 100, tối đa 1000) làm bước thất bại không thử lại. Fail Fast bật (mặc định): phần tử lỗi đầu tiên dừng các phần tử khác và làm bước thất bại; tắt: mọi phần tử đều chạy và phần tử lỗi hiện dạng `{"_error": {index, message}}`. Mỗi phần tử là một lần chạy con, theo cùng quy tắc với `call_workflow`; thử lại bước sẽ chạy lại mọi phần tử. Hủy, Max Run Seconds và timeout của bước dừng các phần tử còn lại |
| `wait_approval` | Tạm dừng lần chạy cho tới khi có người phê duyệt hoặc từ chối (mục 5.11). Expires In Seconds (0 = không hết hạn, tối đa 30 ngày) làm lần chạy thất bại nếu không ai quyết định. Approver Group (tùy chọn) cho operator của nhóm đó quyết định mà không cần token. Chỉ dùng cho quy trình tuần tự |

Chế độ thực thi: `linear` (mặc định, các bước chạy theo Sequence) và `dag` (các bước chạy theo đồ thị qua edge, nhiều bước song song tối đa Max Parallel Steps, mặc định 4). Chính sách lỗi DAG là Fail Branch Only (mặc định) hoặc Fail Whole Workflow.

## 4. Tìm ở đâu

| Menu (trong menu Workflow) | Mở ra | Ghi chú |
| --- | --- | --- |
| Workflow | `/web/base.workflow` | Dạng list, kanban và canvas |
| Workflow Canvas | `/web/base.workflow/canvas` | Trình soạn trực quan bố cục và nối bước |
| Workflow Action | danh sách `base.workflow.action` | Tất cả các bước đã cấu hình |
| Workflow Run | danh sách `base.workflow.run` | Theo dõi lần chạy; lọc theo trạng thái |
| Outbound Delivery | `/web/base.workflow.outbound` | Các lần gửi đi và thử lại |
| MQTT Monitor | `/web/workflow/mqtt-monitor` | Tình trạng MQTT client và từng topic của máy chủ này |

Danh mục hành động (Action Catalog) cũng truy cập được từ các màn hình action. Mục "Workflow Actions" có thể xuất hiện trong Settings.

## 5. Các tác vụ từng bước

### 5.1 Tạo workflow
1. Mở Workflow và bấm Create.
2. Nhập Workflow Name (Tên quy trình) và Topic duy nhất (topic định danh workflow cho webhook và là khóa để khởi chạy). Tùy chọn đặt Group ID để giới hạn người gọi, Description, Execution Mode và các thiết lập DAG.
3. Lưu. Version và Active hiển thị trên danh sách.

### 5.2 Thêm các bước
1. Trong form workflow, thêm dòng ở Action Steps (hoặc dùng Workflow Action > Create rồi chọn Workflow).
2. Chọn mục Action Catalog; Action Code và Action Version tự điền.
3. Điền các đầu vào hiển thị theo loại bước (ví dụ Model Name và operation cho `crud_model`, Target URL và Headers cho `notify`, Template cho `transform_data`, các trường khóa cho HMAC/AES).
4. Đặt Sequence (thứ tự), Step Key (tên ổn định dùng cho audit và resume), Timeout Seconds (mặc định 30), Retry Policy (mặc định 3 lần, cách nhau 15 giây) và Continue On Error nếu muốn workflow tiếp tục sau khi hết số lần thử. Ai thực hiện lần thử tiếp theo tùy cách lần chạy bắt đầu. Lần chạy từ Kafka, MQTT và sự kiện model (danh tính hệ thống) chuyển sang Waiting Retry và cron Workflow Resume Due Runs (mỗi 60 giây) thử lại khi hết thời gian chờ, nên khoảng cách thực tế là thời gian chờ cộng tối đa một chu kỳ cron. Lần chạy từ webhook và Run Now (danh tính người gọi, đánh dấu Caller Bound) thử lại ngay trong cùng yêu cầu và giữ trạng thái Running; nếu máy chủ dừng giữa chừng, lần chạy bị đóng ở trạng thái Failed thay vì được hệ thống chạy lại với quyền cao hơn người gọi. Lời gọi webhook của superadmin được giao cho cron và nhận 202 `retry_scheduled`. Workflow DAG thử lại ngay trong lần chạy. Mỗi lần thử được ghi nhận trước khi bắt đầu, nên lần thử bị cắt ngang do sự cố vẫn được tính: bước liên tục làm sập tiến trình sẽ kết thúc ở Dead Letter. Timeout Seconds tối đa 900 giây và thời gian chờ thử lại tối đa một ngày.
5. Để rẽ nhánh Yes/No, đặt bước `condition` trước; ở các bước sau đặt Branch Label là `yes` hoặc `no` và, nếu có nhiều điều kiện, đặt Condition Step Key.
5a. Bước xử lý lỗi (quy trình tuần tự): đặt `on_error_step_key` trong Extra Params của bước bằng step key của một bước NẰM SAU. Khi bước thất bại hẳn (hết lượt thử lại, hoặc lỗi không bao giờ thử lại như chữ ký HMAC sai), lần chạy nhảy tới bước đó thay vì kết thúc ở Dead Letter; các bước ở giữa không chạy. Bước xử lý nhận đầu vào của bước lỗi kèm `_error: {step_key, message}`. Dòng lịch sử của bước lỗi vẫn là Failed; lần chạy kết thúc Succeeded nếu nhánh xử lý thành công. Validate và Publish từ chối bước đích không tồn tại (`on_error_target_missing`) hoặc không nằm sau bước (`on_error_target_backward`). Quy trình DAG bỏ qua thiết lập này (dùng cạnh `failure`). Lần chạy bị hủy hoặc quá thời gian không bao giờ nhảy. Bước xử lý là một bước bình thường nằm sau: khi mọi thứ thành công lần chạy cũng đi tới nó, nên hãy gắn điều kiện như `exists: _error` nếu nó chỉ được chạy sau lỗi.
5b. Khóa của engine: `_hmac_verified`, `_error`, `_approval`, `_for_each`, `_dry_run` và `_dry_run_intended` bị xóa khỏi đầu vào của trigger (thân webhook, thông điệp Kafka/MQTT, payload Run Now, đầu vào quy trình con) khi lần chạy bắt đầu, nên bên gọi không thể giả mạo. Chỉ engine và các bước đặt các khóa này.
6. Dùng Generate Flow Map để tạo sơ đồ Markdown kèm Mermaid của chuỗi bước.

### 5.3 Thêm trigger
Tạo bản ghi `base.workflow.trigger` (qua form workflow hoặc RPC/MCP) với Workflow, Trigger Type và Is Active:
- Webhook: gọi route `/wh/<topic>` (mục 7). Không cần bản ghi; chỉ thêm bản ghi Webhook để đặt config `hmac_secret` (tham chiếu `@cred:<name>`/`@syscred:<name>`, không bao giờ là giá trị bí mật) và/hoặc `max_body_bytes` (mặc định 1 MB, tối đa 4 MB). Khi có `hmac_secret`, mọi lời gọi phải được ký:
  `X-Webhook-Timestamp: <giây unix>` (lệch giờ máy chủ không quá 5 phút) và `X-Webhook-Signature: sha256=<hex HMAC-SHA256(secret, timestamp + "." + Idempotency-Key + "." + body gốc)>`, trong đó Idempotency-Key là giá trị header hoặc chuỗi rỗng. Canvas giữ lại bản ghi này khi lưu (chuyển sang transport khác chỉ tắt nó). Khi không có `hmac_secret`, bất kỳ ai gọi được webhook công khai cũng có thể gửi trước một khóa, nên chỉ dựa vào khóa với webhook có ký hoặc giới hạn group.
- Kafka: Topic = tên Kafka topic; config tùy chọn `consumer_group` (mặc định: một group riêng `wf-<database>-<trigger id>`; group mới bắt đầu từ cuối topic). Topic nằm trong `kafka.topics` của máy chủ (consumer IPN có sẵn) bị từ chối. Mỗi offset chỉ chạy một lần: lần chạy được đặt tên theo topic, partition và offset, offset gửi lại mà lần chạy đã tồn tại thì bỏ qua. Offset chỉ được commit khi lần chạy thành công, đã hẹn thử lại hoặc ít nhất đã tạo bản ghi lần chạy; nếu lần chạy không bắt đầu được (database ngừng, không nạp được workflow), cùng message đó được thử lại với khoảng nghỉ tăng dần (tối đa 30 giây) và các message sau của partition phải chờ. `use_dedupe` bị bỏ qua.
- MQTT: Topic = mẫu MQTT topic (QoS luôn là 1). Không có `dedupe_key` thì mỗi message, kể cả message QoS 1 gửi lại, tạo một lần chạy mới.
- Kafka và MQTT: config tùy chọn `dedupe_key` = trường payload định danh message, dạng đường dẫn có dấu chấm (`message_id`, `data.txn_id`; chấp nhận tiền tố `$.`). Khi đó lần chạy được đặt tên `<type>:<database>:<topic>:<giá trị>` và message có lần chạy đã tồn tại được bỏ qua như đã xử lý. Message không có chuỗi, số hoặc boolean tại đường dẫn đó vẫn chạy bình thường. Giá trị dài hơn 128 ký tự được băm. Canvas chưa hiển thị trường này: đặt trên bản ghi trigger (form, RPC hoặc MCP).
- TCP: điểm cuối TCP thô có phản hồi đồng bộ (mục 5.9). Chủ đề được suy ra là `tcp:<cổng>`.
- Model Event: config bắt buộc có `model` và `operation` (before/after create, write hoặc unlink); bộ lọc tùy chọn `trigger_domain` và `domain`. Topic được suy ra là `model:<model>:<operation>`. Model trigger chỉ quan sát sự kiện, không bao giờ chặn được thao tác ghi. Trigger chỉ chạy với thao tác ghi trong chính database của nó. Sự kiện `before_*` mang giá trị trước khi thay đổi nhưng cũng chỉ được gửi sau khi ghi xong, giống `after_*`. Workflow ghi vào chính model nó theo dõi sẽ không tự kích hoạt lại, và chuỗi model trigger dừng sau 3 cấp (ghi log cảnh báo). Các lần chạy được xếp hàng theo database (4 lần chạy đồng thời, 256 chờ); sự kiện vượt quá sẽ bị bỏ và ghi log.
- Mọi loại trigger: config tùy chọn `audit_payload` = `full`, `hash` hoặc `none` quyết định lịch sử chạy lưu gì (xem 5.6). Mặc định `hash` cho TCP, `full` cho các loại còn lại. Đặt trên bản ghi trigger (form, RPC hoặc MCP); canvas giữ nguyên giá trị khi lưu.

### 5.4 Lưu bí mật
Tạo Credential (Name, Credential Type, giá trị). Giá trị được mã hóa khi lưu; khi đọc và khi export chỉ trả về dạng đã che, không bao giờ ở dạng rõ. Trong tham số bước, tham chiếu bằng `@cred:<name>` (hoặc `@syscred:<name>` cho credential hệ thống); trường Authorization của notify/post_output_object yêu cầu dạng này. Credential không gắn Workflow sẽ dùng chung cho mọi workflow.

Đích gửi của `notify` và `post_output_object` (kể cả thử lại thủ công) phải phân giải ra địa chỉ công khai: loopback, mạng riêng (10/8, 172.16/12, 192.168/16, fc00::/7), link-local (gồm metadata cloud 169.254.169.254), CGNAT và các dải dành riêng khác bị từ chối, kể cả sau chuyển hướng (tối đa 3 lần, chỉ http/https). Để gọi dịch vụ nội bộ, khai báo IP hoặc CIDR của nó trong `server.outboundAllowCidrs` ở file cấu hình. Nội dung phản hồi giới hạn 1 MB.

### 5.5 Chạy thử, kiểm tra, xuất/nhập
- Chạy thử: dùng "Run Now" trong trình soạn (POST test-run). Lần chạy đi qua đường audit bình thường và gắn nhãn `manual_test`. Workflow phải đang hoạt động.
- DAG: dùng validate-graph để phát hiện vòng lặp trước khi kích hoạt.
- Xuất / nhập: xuất ra tài liệu JSON dùng được ở cơ sở dữ liệu khác (phiên bản 2: cài đặt workflow gồm chế độ DAG, các bước, cạnh DAG, trigger, layout). Tài liệu không chứa id cơ sở dữ liệu: mỗi bước ghi loại là `action_code@version`, nhóm truy cập ghi bằng external id, bí mật chỉ xuất hiện dạng tham chiếu `@cred:`/`@syscred:` và `credentials` liệt kê các tên credential mà cơ sở dữ liệu đích cần có. Nhập chấp nhận phiên bản 1 và 2, đặt tên và topic mới, tạo mọi thứ trong một giao dịch (tất cả hoặc không gì cả) và tìm nhóm theo external id (nhóm không tìm thấy, hoặc id nhóm của phiên bản 1, được thay bằng nhóm hệ thống, không bao giờ thành công khai).
- Sao chép: nhân bản workflow từ thao tác trên bản ghi. Các bước, cạnh DAG và trigger được sao chép trong một giao dịch.
- Sao chép và nhập tạo mọi trigger Kafka, MQTT, model và TCP ở trạng thái không hoạt động (nếu không, bản sao sẽ nhận cùng topic với bản gốc): kiểm tra rồi kích hoạt. Webhook theo topic mới.
- Lưu canvas: các bước và layout được lưu trong một giao dịch. Nếu người khác đã lưu workflow sau khi bạn mở, lần lưu bị từ chối với thông báo xung đột: làm mới rồi áp dụng lại thay đổi. Rời canvas khi còn thay đổi chưa lưu sẽ được hỏi xác nhận.
- Step key là duy nhất trong một workflow, và cạnh DAG phải nối các bước đang tồn tại. Xoá một bước sẽ xoá các cạnh của nó; đổi step key sẽ chuyển các cạnh theo. Cơ sở dữ liệu hiện có được tạo unique index trên (workflow, step key) khi khởi động, trừ khi đang có trùng lặp: khi đó log máy chủ liệt kê chúng (đổi tên rồi khởi động lại).

### 5.6 Theo dõi các lần chạy
1. Mở Workflow Run và dùng bộ lọc trạng thái (Pending, Running, Waiting Retry, Dead Letter, Succeeded, Failed, Cancelled; Retry Scheduled không phải trạng thái mà là các run đã có thời điểm thử lại) hoặc nhóm theo status, trigger type, topic, flow version, current step.
2. Mở một lần chạy: nhóm Execution và Recovery cho thấy trạng thái, thời điểm thử lại và lỗi gần nhất; các trang Payload, Step History và Audit cho thấy dữ liệu vào/ra và kết quả từng bước.
3. Dữ liệu một lần chạy lưu lại phụ thuộc Audit Mode (lấy từ `audit_payload` của trigger):
   - `full`: payload với giá trị nhạy cảm thay bằng `[redacted]`. Luôn bị ẩn: `authorization`, `cookie`, `password`, `secret`, `token`, `key`, `pan`, `card_number`, `track2`, `pin`, `pin_block`, `cvv`/`cvc` và mọi khóa kết thúc bằng `_key`, `_secret`, `_token` hoặc `_password` (không phân biệt hoa thường, `-` tính như `_`). Thêm khóa riêng ở trường Audit Redact Keys của workflow: tên đơn (`card_ref`) khớp ở mọi cấp, đường dẫn có dấu chấm (`customer.phone`) tính từ gốc.
   - `hash`: chỉ lưu `{"_audit":"hash","bytes":...,"hmac_sha256":...}`. Giá trị băm dùng khóa `app.encryptKey` của máy chủ (bỏ qua nếu khóa trống), nên hai payload giống nhau trên cùng máy chủ cho cùng giá trị nhưng không thể dò ngược số thẻ.
   - `none`: chỉ lưu `{"_audit":"none"}`.
   Không đặt `full` cho trigger TCP: các trường ISO 8583 không được nhận diện theo tên nên dữ liệu thẻ sẽ bị lưu nguyên văn.
   Input Payload và Step History áp dụng ngay. Output Payload là điểm tiếp tục khi thử lại nên được giữ đầy đủ khi lần chạy đang Running hoặc Waiting Retry, và chỉ được rút gọn khi lần chạy kết thúc. Các lần chạy cũ giữ nguyên dữ liệu đã lưu. Các bước luôn chạy trên payload thật.

### 5.7 Thử lại một lần gửi bị lỗi
Mỗi bước `notify` hoặc `post_output_object` có một bản ghi Outbound Delivery cho mỗi lần chạy, tên `<tên lần chạy>:<step key>`; lần thử lại của bước dùng lại bản ghi đó. Request mang cùng tên này trong header `Idempotency-Key` ở mọi lần thử (trừ khi Headers của bước đã đặt header này), để bên nhận loại bỏ bản trùng.

Bản ghi `notify` được gửi nền. Bản ghi được lưu trước; cron Workflow Sweep Outbound Deliveries (mỗi 30 giây) gửi các bản ghi chưa được gửi, tiếp quản bản ghi kẹt ở `sending` quá 5 phút và thử lại bản ghi `retryable_failed` khi đến Next Attempt At. Khoảng nghỉ tăng gấp đôi sau mỗi lần thất bại (15 giây, 30 giây, 60 giây ... tối đa 1 giờ) cho tới khi dùng hết Max Attempts (3), khi đó bản ghi chuyển `permanent_failed`; phản hồi 4xx (trừ 429) là lỗi vĩnh viễn ngay. Bản ghi đến hạn quá 24 giờ trước thì không được tự gửi. Chạy lại bước `notify` có bản ghi đã `delivered` hoặc đang `sending` sẽ không gửi lại.

Bản ghi `post_output_object` chỉ ghi nhận lời gọi đồng bộ; cron không bao giờ gửi lại chúng, Retry Policy của bước làm việc đó.

Để gửi lại thủ công, mở Outbound Delivery, chọn bản ghi ở trạng thái `queued`, `retryable_failed` hoặc `permanent_failed` và bấm Retry (số lần thử được đếm lại từ đầu).

### 5.8 Viết bước Lua
Lưu script trước trong Automation > Actions (loại lua). Trong bước `lua_script` chọn action đó. Script nhận `payload` (đầu ra bước trước), `raw` (đầu vào nguyên bản), `data` (JSON tùy chỉnh của node), `topic`, `step_key`, `idempotency_key` (`<tên lần chạy>:<step key>`, giống nhau ở mọi lần thử của bước; truyền cho hệ thống có chống trùng) và `base.call(model, method, kwargs)` để gọi method của model. Bảng trả về trở thành payload mới; không trả gì thì giữ payload (có thể đã chỉnh sửa). Mã Lua không bao giờ được lưu trong workflow.

### 5.9 Trigger TCP (khung thô, phản hồi đồng bộ)
Trigger `tcp` biến quy trình thành một điểm cuối TCP thô: mỗi thông điệp đã đóng khung đi vào sẽ chạy một lần, và đầu ra cuối cùng của lần chạy được ghi trả lại làm phản hồi. Lớp truyền chỉ tách khung và bảo vệ kết nối. Nó không phân tích, kiểm tra, khử trùng lặp hay dựng nội dung thông điệp; mọi việc nhìn vào bên trong thân thông điệp thuộc về bước của quy trình hoặc module mà quy trình gọi.

Một lần chạy tcp chỉ được trả lời một lần: mỗi bước chỉ được thử đúng một lần (bỏ qua Retry Policy), lỗi sẽ kết thúc lần chạy ở trạng thái Failed (không phải Dead Letter), và lần chạy tcp không bao giờ được tiếp tục hay thử lại sau đó, vì thiết bị đầu cuối đã ngừng chờ và có thể đã đảo giao dịch.

**Bật tính năng (cấu hình máy chủ, mặc định tắt).** Chỉ tạo được trigger `tcp` sau khi bật `tcp.useTcp` trong file cấu hình. Khóa và chứng chỉ nằm trong file cấu hình, không bao giờ lưu vào cơ sở dữ liệu.

| Khóa | Mặc định | Ý nghĩa |
| --- | --- | --- |
| `useTcp` | `false` | Công tắc chính. Tắt: không tạo được trigger `tcp`, trigger sẵn có bị bỏ qua khi khởi động |
| `bind` | `0.0.0.0` | Địa chỉ lắng nghe |
| `defaultPort` / `portMin` / `portMax` | `10123` / `10000` / `49999` | Cổng mặc định khi không đặt và dải cổng cho phép |
| `maxConns` / `maxConnsPerIp` | `256` / `32` | Giới hạn kết nối mỗi cổng và mỗi địa chỉ máy khách |
| `maxFrameBytes` | `4096` | Thân khung lớn nhất được nhận; độ dài lớn hơn sẽ đóng kết nối |
| `firstFrameS` / `idleTimeoutS` | `10` / `120` | Số giây cho khung đầu tiên và giữa các khung |
| `allowCidrs` | trống | Danh sách mạng được phép (trống là cho tất cả) |
| `proxyProtocol` / `trustedProxies` | `none` / trống | `none`, `v1`, `v2` hoặc `auto`; chỉ máy trong `trustedProxies` được gửi tiêu đề PROXY |
| `tlsCertFile` / `tlsKeyFile` | trống | Chứng chỉ và khóa cho trigger `tls`, `mtls` (đọc lại khoảng 60 giây một lần, đổi chứng chỉ không cần khởi động lại) |
| `tlsClientCaFile` | trống | CA dùng để xác minh chứng chỉ máy khách của `mtls` |

**Thiết lập trigger** (Loại trigger = TCP): một trigger sở hữu một cổng.

| Thiết lập | Mặc định | Ý nghĩa |
| --- | --- | --- |
| Cổng | `10123` | Phải nằm trong `portMin`-`portMax`; cổng đang dùng bởi trigger, tenant hay tiến trình khác sẽ bị từ chối |
| Cách đóng khung | `len2_bcd` | `len2_bcd` (2 byte, 4 chữ số BCD), `len2_bin` (2 byte big-endian), `stx_etx` (`0x02` ... `0x03` bao quanh thân), `len4_ascii` (4 chữ số ASCII) |
| Độ dài đã gồm phần đầu | tắt | Bật khi bên gửi tính cả byte tiêu đề vào độ dài |
| TLS | `none` | `none`, `tls` hoặc `mtls` (bắt buộc chứng chỉ máy khách). Một cổng chỉ là thuần hoặc TLS, không tự nhận diện |
| Thời gian chờ phản hồi (ms) | `30000` | Thời gian quy trình có để trả lời; quá hạn thì đóng kết nối, không gửi phản hồi |

Chủ đề được suy ra là `tcp:<cổng>`. Sao chép hoặc nhập quy trình sẽ tạo trigger tcp ở trạng thái không hoạt động vì cổng đã bị bản gốc chiếm: hãy chọn cổng trống rồi kích hoạt. Tiêu đề sai (chữ số sai, độ dài vượt `maxFrameBytes`, thiếu ETX) sẽ đóng kết nối. Một kết nối cho một giao dịch là bình thường: bên kia có thể đóng sau khi nhận phản hồi.

**Đầu vào của lần chạy.** Mỗi khung chạy một lần với payload JSON sau. `payload_hex` là thân khung đúng như nhận được (đã bỏ tiêu đề) và không bao giờ bị giải mã.

```json
{"transport":"tcp","local_port":10123,"remote_addr":"203.0.113.9:51234",
 "tls":true,"client_cn":"EDC-000123","conn_id":"...","seq":7,
 "received_at":"2026-11-08T09:00:00Z","payload_len":123,"payload_hex":"6000..."}
```

`remote_addr` là địa chỉ máy khách thật (sau tiêu đề PROXY), `client_cn` là tên chứng chỉ máy khách đã xác minh khi dùng `mtls`, `seq` đếm số khung trên kết nối. Các khung của một kết nối chạy lần lượt nên phản hồi giữ đúng thứ tự yêu cầu.

**Quy ước phản hồi.** Đầu ra cuối cùng của quy trình là một đối tượng JSON:

| Đầu ra cuối | Lớp truyền làm gì |
| --- | --- |
| `{"reply_hex":"..."}` | Giải mã hex, thêm tiêu đề khung rồi ghi; kết nối vẫn mở |
| `{"reply_hex":"...","close":true}` | Ghi phản hồi rồi đóng |
| `{"close":true}` | Đóng, không ghi |
| không có `reply_hex`, `reply_hex` không giải mã được, lần chạy lỗi hoặc quá hạn | Không ghi gì và đóng kết nối. Lớp truyền không bao giờ tự bịa phản hồi |

**Kiểm tra echo.** Lưu một Lua action trong Automation trả về `{reply_hex = payload.payload_hex}`, tạo quy trình chỉ có một bước `lua_script` chạy action đó, thêm trigger `tcp` cổng 10123 với `len2_bcd`, gửi một khung và mong nhận lại đúng khung đó. Thử TLS bằng `openssl s_client -connect host:10123`.

### 5.10 Đặt trigger TCP sau proxy
- **Cloudflare không proxy TCP thô trên đường đám mây cam thông thường.** Nó chỉ proxy HTTP(S) và WebSocket trên một danh sách cổng cố định, không gồm phần lớn dải 10000-49999. Dùng bản ghi DNS-only (đám mây xám) hoặc Cloudflare Spectrum (trả phí; kiểm tra giới hạn gói của tài khoản).
- **nginx** cần khối `stream {}`; **HAProxy** cần `mode tcp`. Gửi tiêu đề PROXY bằng `proxy_protocol on` (nginx) hoặc `send-proxy-v2` (HAProxy), liệt kê các proxy đó trong `trustedProxies` và đặt `proxyProtocol`.
- **Chuyển tiếp TLS (khuyến nghị):** proxy chuyển tiếp TCP và Base HUB tự kết thúc TLS hoặc mTLS, nên `client_cn` vẫn tới được quy trình. Tiêu đề PROXY đến trước bắt tay TLS.
- **Proxy kết thúc TLS:** cổng Base HUB là TCP thuần và chỉ được truy cập từ proxy; mất danh tính mTLS.
- Thời gian chờ nhàn rỗi của proxy phải lớn hơn `idleTimeoutS` và độ trễ dài nhất mà quy trình có thể thêm. Chỉ mở các cổng đang dùng trên tường lửa.
- Thiết bị di động dùng chung IP nhà mạng, nên để `maxConnsPerIp` rộng rãi.

### 5.11 Chờ phê duyệt
1. Thêm bước `wait_approval` tại nơi lần chạy phải dừng (chỉ quy trình tuần tự: Validate và Publish từ chối bước này trong DAG với mã `wait_approval_in_dag`). Có thể chọn **Approver Group** (`approver_group_id`, một bản ghi `tbl.groups`): thành viên của nhóm đồng thời có vai trò operator workflow được quyết định lần chạy mà không cần token (bước 3). Validate và Publish từ chối nhóm không tồn tại (`invalid_param`). Import bỏ Approver Group (id nhóm không dùng được giữa các cơ sở dữ liệu): chọn lại sau khi import.
2. Khi lần chạy tới bước này, nó chuyển sang **Waiting Approval**: không giữ worker, cron thử lại không bao giờ nhận nó, và một token tiếp tục dùng một lần được tạo. Chỉ lưu mã băm SHA-256 của token. Token được trao một lần cho bên đã khởi chạy lần thực thi đó: bên gọi webhook nhận `202 {"status":"waiting_approval","run_id":..,"resume_token":..,"expires_at":..}`, Run Now trả token trong kết quả. Replay không bao giờ trả token. Lần chạy do Kafka, MQTT, sự kiện model hoặc cron khởi chạy không trao token cho ai, nên hãy đặt Approver Group cho bước, nếu không chỉ quản trị viên có thể quyết định (bước 3). Khi lần chạy dừng chờ, Approver Group của bước được chép vào lần chạy thành `approval_group_id` (hiển thị trên lần chạy); quyết định dùng bản chép này, nên xuất bản lại quy trình không thay đổi ai được quyết định một lần chạy đang chờ.
3. Quyết định bằng `base.workflow.run/approve/<run id>` hoặc `reject/<run id>`, kwargs `{"token": "...", "comment": "..."}`. Cần vai trò operator và token. Được bỏ qua token: quản trị hệ thống (base.group_system) và thành viên (trực tiếp hoặc qua nhóm kế thừa) của `approval_group_id` của lần chạy có vai trò operator; người khác không có token nhận 403. Quyết định đầu tiên thắng; quyết định thứ hai, token sai (403), lần chờ đã hết hạn hoặc lần chạy đã hủy đều bị từ chối. Phân tách nhiệm vụ (mặc định an toàn cho tới khi chủ sở hữu quyết định chính sách):
   - Người đã khởi chạy lần chạy không được PHÊ DUYỆT nó (403), kể cả khi là thành viên Approver Group; vẫn được từ chối. Quản trị hệ thống được phê duyệt mọi lần chạy.
   - Lần chạy Run Now (thử), vốn chạy bản nháp chưa xuất bản, chỉ quản trị hệ thống được phê duyệt.
   - Replay không bao giờ trả token, và replay một lần chạy có bước `wait_approval` trong lịch sử cần base.group_system. Lần replay dừng ở bước chờ do quản trị hệ thống quyết định.
   - Token bị mất không thể khôi phục hay cấp lại: quản trị hệ thống (hoặc operator thuộc Approver Group) quyết định lần chạy mà không cần token, hoặc bất kỳ ai có vai trò operator hủy nó.
4. Phê duyệt: lần chạy tiếp tục với bước kế tiếp, payload có thêm `_approval: {approved: true, by: <user id>, comment, decided_at}`. Từ chối: lần chạy kết thúc Failed, hoặc tiếp tục tại `on_error_step_key` của bước với `_error` và `_approval`. Quyết định được lưu trong cùng lần ghi cơ sở dữ liệu nhận quyền xử lý lần chạy, nên máy chủ sập ngay sau đó cũng không làm mất. Kết quả trả về là `{id, status, last_error}`:
   - Lần chạy do trigger hệ thống (Kafka, MQTT, sự kiện model) khởi chạy được giao lại cho cron Workflow Resume Due Runs, cron tiếp tục nó với quyền hệ thống theo Max Concurrent Runs của quy trình. Kết quả trả về ngay với `status: "waiting_retry"`.
   - Lần chạy webhook hoặc Run Now tiếp tục ngay trong yêu cầu phê duyệt, dưới danh tính người đã KHỞI CHẠY nó (lần chạy webhook ẩn danh vẫn ẩn danh), không phải người phê duyệt. Khi Max Concurrent Runs của quy trình đã đầy, kết quả là 409 `workflow concurrency limit reached` và lần chạy tiếp tục chờ với cùng token và hạn: hãy thử lại sau. Kết quả kèm `resume_token` mới nếu lần chạy dừng ở một bước chờ khác.
   - Quyết định kết thúc lần chạy (từ chối khi không có `on_error_step_key`, phê duyệt ở bước cuối) trả về trạng thái cuối.
5. Cancel Run kết thúc ngay lần chạy đang chờ. Lần chờ hết hạn được cron Workflow Resume Due Runs đóng thành Failed (`approval wait expired`). Lần chạy đang chờ không bao giờ bị xóa bởi tác vụ dọn dữ liệu. Lần chạy TCP hoặc quy trình con (`call_workflow`, `for_each`) không thể chờ: bước sẽ thất bại. Chạy thử (dry run) bỏ qua bước chờ và tiếp tục như đã phê duyệt.

## 6. Quản trị và cấu hình

| Chủ đề | Chi tiết |
| --- | --- |
| Quyền truy cập | Người dùng nội bộ (`base.group_user`) có quyền nền của nền tảng trên các model `base.*`: đọc mọi model ở đây. Tạo, sửa, xóa workflow, bước, trigger, cạnh, credential và danh mục action chỉ dành cho `base.group_system` (các bước chạy với toàn quyền, nên soạn workflow là việc của quản trị viên). Run và outbound delivery không bị giới hạn thêm ngoài quyền nền |
| Nhóm truy cập workflow | `Group ID` của workflow giới hạn route webhook và test-run cho thành viên nhóm đó (superadmin luôn được phép). Để trống thì webhook công khai (không cần đăng nhập: hãy bảo vệ bằng `hmac_secret`); các route `/wf` (test-run, trợ giúp) luôn yêu cầu người gọi đã đăng nhập |
| Phạm vi công ty | Bước, trigger, lần chạy kế thừa công ty của workflow cha |
| Workflow có sẵn | `demo_txn_forward_v1` (topic `demo`), `bank_account_import_v1` (topic `bank`), một workflow demo mã hóa, cùng danh mục hành động |
| Tác vụ định kỳ | Hai bản ghi `sys.cron` được seed ở trạng thái không hoạt động: Workflow Resume Due Runs (mỗi 60 giây, limit 50) và Workflow Reap Stale Running Runs (mỗi 60 giây, limit 50, quá hạn sau 300 giây). Hãy kích hoạt trong màn hình tác vụ định kỳ để có thử lại và dọn dẹp tự động |
| Mặc định thử lại | 3 lần, cách nhau 15 giây, cho cả bước và outbound delivery |

## 7. API và điểm tích hợp tự động hóa

| Điểm tích hợp | Mục đích |
| --- | --- |
| `ANY /wh/<topic>` | Khởi chạy workflow webhook đang hoạt động theo topic (không có trigger MQTT/Kafka/model/TCP); body là payload. Trả về `{topic, workflow, status}`; topic không tồn tại trả 404 "no workflow". Header `Idempotency-Key` tùy chọn (1-128 ký tự `A-Z a-z 0-9 . _ : -`): khóa mà cùng người gọi (cùng user, hoặc ẩn danh) đã dùng cho workflow này trả `status: "duplicate"` và không chạy lại (lời gọi có ký mà không có khóa được chống trùng theo chữ ký). Body vượt giới hạn: 413. Webhook có ký nhận lời gọi thiếu/sai chữ ký: 401. Người gọi ẩn danh chỉ nhận thông báo lỗi chung |
| `POST /wf/workflows/:id/test-run` | Chạy thử với `{payload}`; trả về `run_id` và `status` |
| `POST /wf/workflows/:id/validate-graph` | Kiểm tra vòng lặp của các edge DAG |
| `GET /wf/workflows/:id/export`, `POST /wf/workflows/import` | Xuất / nhập workflow |
| `GET /wf/runs/latest` | Lần chạy gần nhất của một topic |
| `GET /wf/node-specs`, `/wf/credentials`, `/wf/sys-credentials`, `/wf/sys-models` | Hỗ trợ canvas (đặc tả bước, danh sách credential và model) |
| Method của model | `replace_action_sequence`, `replace_workflow_edges`, `copy`, `export`, `import`, `writeWorkflowFlowMap`, `manual_retry`, `mqtt_metrics` |
| Công cụ MCP | `workflow.record.search_read`, `.read`, `.create`, `.write`, `.unlink`, `workflow.export`, `workflow.import`, `workflow.replace_action_sequence`, `workflow.replace_workflow_edges` |

## 8. Xử lý sự cố và câu hỏi thường gặp

| Hiện tượng | Nguyên nhân có thể và cách xử lý |
| --- | --- |
| Webhook trả 404 "no workflow" | Topic không khớp workflow đang hoạt động, workflow dùng trigger khác (MQTT, Kafka, model, TCP), hoặc module chưa cài trong cơ sở dữ liệu đó |
| 401 / 403 ở webhook | Người gọi chưa xác thực, không thuộc Group ID của workflow, hoặc webhook có `hmac_secret` mà chữ ký/timestamp thiếu, sai hoặc cũ hơn 5 phút (kiểm tra đồng hồ bên gửi) |
| Webhook trả `status: "duplicate"` | `Idempotency-Key` (hoặc cùng một request đã ký) đã được dùng cho topic này; gửi khóa mới cho sự kiện mới |
| Lần chạy kẹt ở Waiting Retry | Cron resume chưa hoạt động; hãy kích hoạt Workflow Resume Due Runs |
| Lần chạy kẹt ở Running | Worker dừng giữa chừng; hãy kích hoạt Workflow Reap Stale Running Runs |
| Lần chạy kết thúc ở Dead Letter / Failed | Mở Step History và Last Error; sửa bước hoặc dữ liệu rồi chạy lại |
| Đồ thị bị từ chối khi lưu | Các edge chứa vòng lặp; thông báo lỗi nêu các step key liên quan |
| Gửi đi bị lỗi | Kiểm tra Target Ref, Headers và tham chiếu `@cred:`; đọc Last Error và Response Body rồi bấm Retry |
| Trigger Kafka/MQTT không chạy | Trigger không hoạt động hoặc broker ngừng; kiểm tra MQTT Monitor. Partition Kafka đứng yên sẽ ghi log `handler error ... not committed`: sửa nguyên nhân (database, workflow) và nó tự chạy tiếp |
| Lần gửi notify kẹt ở `queued` | Cron Workflow Sweep Outbound Deliveries chưa có hoặc chưa bật (database có sẵn nhận cron này khi import master data) |
| Máy chủ khác chưa nhận thay đổi trigger | Các máy chủ nạp lại trigger đã đổi trong khoảng 10 giây và đồng bộ lại toàn bộ trigger mỗi 2 phút |
| Lỗi trùng name/topic | Name và Topic phải duy nhất; import và copy tự thêm hậu tố |
| Lưu canvas báo workflow đã được người khác lưu | Người dùng (hoặc tab) khác đã lưu sau khi bạn mở; làm mới rồi áp dụng lại thay đổi |
| "step_key ... is already used" hoặc cạnh "references step(s) that do not exist" | Step key phải duy nhất trong workflow và cạnh phải nối các bước đã lưu; đổi tên bước hoặc lưu canvas trước |
| Workflow sao chép hoặc nhập không nhận tin Kafka/MQTT | Trigger không phải webhook được cố ý tạo ở trạng thái không hoạt động; kích hoạt sau khi kiểm tra |
| Nhập bị từ chối: nhóm không tồn tại | Nhóm truy cập đã xuất (external id) không có trong cơ sở dữ liệu này; tạo nhóm hoặc sửa `flow.group` trong tài liệu |

## 9. Giới hạn và lưu ý

- Chạy thử và gọi webhook là đồng bộ: phản hồi HTTP trả về sau khi lần chạy đạt trạng thái kết thúc hoặc đang xử lý.
- Chế độ DAG chỉ có hiệu lực khi đã có edge; nếu không workflow chạy tuần tự.
- Model-event trigger chỉ là bộ quan sát; không thể phủ quyết thay đổi bản ghi.
- Đăng ký MQTT luôn dùng QoS 1; chống trùng theo trường payload cần `dedupe_key`.
- Số liệu MQTT Monitor tính theo từng máy chủ, không phải toàn cụm.
- Không thể đọc lại giá trị credential; nhập lại để thay đổi.
- Không thể tạo hoặc xóa mục catalog từ giao diện.
