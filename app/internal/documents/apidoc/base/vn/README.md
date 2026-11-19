# Hướng dẫn API Base HUB

**Dành cho ai:** lập trình viên chưa từng dùng API này. **Bạn nhận được gì:** sau khoảng 10 phút bạn có thể đăng nhập,
liệt kê bản ghi, tạo mới, sửa và xóa một bản ghi, đồng thời biết phải làm gì khi một lời gọi bị lỗi.

Tab **Reference** liệt kê mọi model và method. Hướng dẫn này giải thích các quy tắc chung mà tất cả đều dùng, nên hãy đọc một lần trước.

## 1. Tổng quan

Mọi thứ trong Base HUB là một **model** (một loại bản ghi, ví dụ `tbl.partner` = khách hàng hoặc liên hệ) và mỗi model có các
**method** (việc bạn có thể làm với nó: `search_read`, `create`, `write`, `unlink`).

Để làm bất cứ việc gì, bạn gửi một HTTP `POST`:

```
POST {BASE_URL}/rpc/{model}/{method}
```

Đó là toàn bộ API. Tab Reference cho biết có những model, method nào và phải đặt gì vào body.

Ba quy tắc bao quát 90% công việc:

1. **Đăng nhập trước** và gửi token trong mọi lời gọi.
2. **Mọi câu trả lời có cùng một dạng:** `{"id", "result"}` khi thành công, `{"id", "error"}` khi thất bại.
3. **Quyết định theo `error.code`, đừng bao giờ theo `error.message`.** Code là một số ổn định; message chỉ để người đọc.

## 2. Thực hiện lời gọi đầu tiên

Bạn cần `BASE_URL` (địa chỉ server) và một tài khoản. Các ví dụ dùng `curl`; client HTTP nào cũng được.

### Bước 1: đăng nhập và lấy token

```bash
curl -s -X POST "$BASE_URL/oauth/token" \
  -H "Content-Type: application/json" \
  -d '{"grant_type":"password","username":"operator01","password":"<password>"}'
```

```json
{"access_token": "eyJ...", "refresh_token": "5f9d...", "expires_in": 900, "token_type": "Bearer"}
```

Lưu `access_token`: `TOKEN=eyJ...`. Token dùng được trong `expires_in` giây (900 = 15 phút). Hãy giữ cả `refresh_token` (mục 5).

### Bước 2: liệt kê bản ghi

```bash
curl -s -X POST "$BASE_URL/rpc/tbl.partner/search_read" \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"id": "my-first-call", "kwargs": {"fields": ["id", "name"], "limit": 5}}'
```

### Bước 3: đọc câu trả lời

```json
{"id": "my-first-call", "result": [{"id": 1, "name": "Acme"}]}
```

`id` chỉ là nhãn do bạn đặt, được trả lại để bạn ghép câu trả lời với request. Dữ liệu nằm trong `result`.

Nếu có gì sai, bạn nhận `error` thay cho `result`:

```json
{"id": "my-first-call", "error": {"code": -32001, "message": "invalid or expired token"}}
```

`-32001` nghĩa là token thiếu hoặc đã hết hạn: hãy đăng nhập lại (Bước 1).

## 3. Tạo, đọc, sửa, xóa (ví dụ đầy đủ)

Lần nào cũng cùng một mẫu: `POST /rpc/tbl.partner/<method>`, token trong header, body JSON. Chỉ có hai thứ thay đổi là
**tên method** và việc **id của bản ghi có nằm trong path hay không**.

| Tôi muốn... | Method | URL | Body |
| --- | --- | --- | --- |
| Liệt kê / tìm kiếm | `search_read` | `/rpc/tbl.partner/search_read` | `{"kwargs": {"filter": {"active": true}, "fields": ["id","name"], "limit": 20}}` |
| Tạo mới | `create` | `/rpc/tbl.partner/create` | `{"args": [{"name": "Acme"}]}` |
| Đọc một bản ghi theo id | `read` | `/rpc/tbl.partner/read/8` | `{"kwargs": {"fields": ["id","name"]}}` |
| Cập nhật | `write` | `/rpc/tbl.partner/write/8` | `{"args": [{"name": "Acme Ltd"}]}` |
| Xóa | `unlink` | `/rpc/tbl.partner/unlink/8` | `{}` |

Cách đọc bảng:

* `args` là danh sách giá trị theo thứ tự; `kwargs` là các tùy chọn có tên. Tab Reference cho thấy chữ ký của từng method,
  và panel **Try it** đưa sẵn một body chạy được. Hãy copy nó.
* **Id trong path:** `read`, `write` và `unlink` tác động lên bản ghi đã có, nên id nằm trong URL (`/write/8`).
  Có thể truyền nhiều id cách nhau bằng dấu phẩy (`/unlink/8,9`). Các method tạo mới hoặc tìm kiếm không có id trong path.
* Sau `create` bạn nhận lại bản ghi mới, kèm `id`. Dùng `id` đó cho các lời gọi tiếp theo.

```bash
# tạo mới
curl -s -X POST "$BASE_URL/rpc/tbl.partner/create" \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"args": [{"name": "Acme"}]}'
# -> {"id": "...", "result": {"id": 8, "name": "Acme"}}

# cập nhật bản ghi 8
curl -s -X POST "$BASE_URL/rpc/tbl.partner/write/8" \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"args": [{"name": "Acme Ltd"}]}'
# -> {"id": "...", "result": true}
```

## 4. Các thói quen giúp tránh bug

Hãy coi đây là checklist trước khi release.

1. **Luôn kiểm tra `error` trước.** Chỉ nhìn HTTP status là chưa đủ. Hãy parse body và tìm `error`.
2. **Rẽ nhánh theo `error.code`.** Đừng so khớp chữ trong `message`; nó có thể thay đổi.
3. **Gửi `X-Idempotency-Key` cho mọi lời gọi tạo dữ liệu hoặc chuyển tiền.** Nếu mạng đứt và bạn gọi lại,
   server trả câu trả lời đầu tiên thay vì làm hai lần (lưu 30 phút). Dùng giá trị duy nhất cho mỗi hành động nghiệp vụ,
   ví dụ `order-42-create`.
   ```bash
   -H "X-Idempotency-Key: order-42-create"
   ```
4. **Ngày giờ là số nguyên:** Unix seconds UTC, ví dụ `1791676800`. Thời gian chưa đặt là `false`.
5. **Phân trang danh sách.** Truyền `limit` và `offset`. Route list REST mặc định `limit=50` và tối đa 500.
6. **Không ghi log token hay mật khẩu.**
7. **Danh sách rỗng có thể nghĩa là "không có quyền".** Method đọc (search, read, count) bị từ chối sẽ trả `200` với `[]`, `0`, `false`,
   `{}` hoặc `null`, không phải lỗi. Nếu danh sách rỗng bất ngờ, hãy kiểm tra quyền của user trước khi nghi ngờ code của bạn.

## 5. Giữ phiên đăng nhập

* Access token sống **15 phút**. Gửi dưới dạng `Authorization: Bearer <access_token>`.
* Khi một lời gọi trả `-32001`, hãy lấy token mới bằng refresh token thay vì hỏi lại mật khẩu:

```bash
curl -s -X POST "$BASE_URL/oauth/token" \
  -H "Content-Type: application/json" \
  -d '{"grant_type":"refresh_token","refresh_token":"<refresh_token>"}'
```

* Refresh token sống **30 ngày** và **đổi sau mỗi lần dùng**. Luôn lưu `refresh_token` mới trong câu trả lời;
  token cũ sẽ ngừng hoạt động.
* Nhập sai mật khẩu quá nhiều lần sẽ trả HTTP `429` kèm header `Retry-After` (số giây phải chờ).
* Caller giữa các server có thể gửi `X-API-Key: <key>` thay thế (chỉ khi server bật `app.supportApikey`).
* Một server có thể chứa nhiều database công ty. Database được lấy từ token (claim `db`) hoặc từ header
  `X-Database-Name`. Token chỉ dùng được trên database mà nó được cấp.
* Mọi lời gọi còn được kiểm tra theo quyền của user (ACL) và record rule.

## 6. Khi một lời gọi thất bại

Tìm triệu chứng của bạn trước, rồi tra bảng code bên dưới.

| Bạn thấy | Nguyên nhân có thể | Cách sửa |
| --- | --- | --- |
| `error.code` `-32001` / HTTP 401 | Token thiếu, sai hoặc hết hạn | Refresh token hoặc đăng nhập lại |
| `error.code` `-32003` / HTTP 403 | User này không được phép | Dùng tài khoản có quyền; thử lại cũng không giúp ích |
| `error.code` `-32601` / 404 | Gõ sai model, method hoặc URL | So với tab Reference |
| `error.code` `-32602` / 400 | Thiếu hoặc sai tham số | Đọc `error.message`; copy body từ **Try it** |
| `error.code` `-32004` / 404 | Id của bản ghi không tồn tại | Kiểm tra id hoặc serial |
| `error.code` `-32009` / 409 | Bản ghi đang ở sai trạng thái, hoặc bị người khác đổi | Tải lại bản ghi rồi quyết định |
| `error.code` `-32603` / 500 | Lỗi phía server | Thử lại sau; nếu còn lỗi, báo cáo `id` của response |
| `result` là `[]` nhưng dữ liệu có thật | Vấn đề quyền hoặc filter | Kiểm tra quyền của user và `filter` của bạn |

Câu trả lời lỗi luôn có dạng này, cho cả `/rpc` lẫn route REST (kể cả route không tồn tại và sai HTTP verb):

```json
{"id": "my-request-1", "error": {"code": -32009, "message": "device is not active", "data": {"reason": "device_inactive"}}}
```

* HTTP status đi theo code, nên proxy và monitoring vẫn hoạt động.
* `error.data` là tùy chọn. Mã chữ riêng của module không có số trong registry được giữ ở `data.reason`.

| code | HTTP | Ý nghĩa | Nên làm gì |
| --- | --- | --- | --- |
| -32700 | 400 | Body không phải JSON hợp lệ | Sửa body request |
| -32600 | 400 | Envelope request không hợp lệ | Sửa cấu trúc request |
| -32601 | 404 | Model, method hoặc route không tồn tại | Kiểm tra chính tả và tab Reference |
| -32602 | 400 | Validation thất bại (thiếu hoặc sai tham số, path id sai) | Sửa tham số; đọc `message` |
| -32603 | 500 | Lỗi server không mong đợi (message luôn là `an unexpected error occurred`) | Thử lại sau; báo cáo `id` của response |
| -32000 | 400 | Lỗi method chung (mặc định cho lỗi thường) | Đọc `message` |
| -32001 | 401 | Thiếu hoặc sai thông tin xác thực | Refresh token; nếu không được thì đăng nhập lại |
| -32003 | 403 | Bị cấm (ACL, hoặc chỉ superadmin) | Không thử lại; dùng tài khoản có quyền |
| -32004 | 404 | Không tìm thấy record | Kiểm tra id hoặc serial |
| -32009 | 409 | Xung đột hoặc chuyển trạng thái không hợp lệ | Tải lại record rồi quyết định |
| -32010 | 409 | Xung đột idempotency (dự trữ) | Chưa được phát ra |
| -32022 | 422 | Vi phạm quy tắc nghiệp vụ (dự trữ) | Chưa được phát ra |
| -32029 | 429 | Bị giới hạn tốc độ (dự trữ; chặn đăng nhập đã trả `429` + `Retry-After`) | Chờ `Retry-After` giây |
| -32050 | 503 | Không khả dụng (cũng 502/504), ví dụ broker hoặc database sập | Thử lại với backoff |

Các số là cố định: một code không bao giờ được dùng lại hay đánh số lại. Xử lý lỗi theo code:

```js
const res = await fetch(`${host}/rpc/edc.device/get_by_serial`, { method: "POST", headers, body });
const body = await res.json();
if (body.error) {
  if (body.error.code === -32004) showNotFound();
  else if (body.error.code === -32001) await refreshToken();
  else throw new Error(`${body.error.code}: ${body.error.message} (request ${body.id})`);
}
```

## 7. Các điểm vào khác

Phần lớn công việc dùng `POST /rpc/...` và nhận envelope `{"id","result"}` / `{"id","error"}`. Một số surface thì khác:

| Surface | Route | Dùng cho | Định dạng response |
| --- | --- | --- | --- |
| RPC | `POST /rpc/{model}/{method}` và `/rpc/{model}/{method}/{id}` | Mọi method đã đăng ký của model | Envelope ở trên |
| REST alias | `GET/POST/PUT/DELETE /base/...` | Path kiểu resource mà một số module cung cấp (EDC admin, users). Xóa trả `{"deleted": true, "id": N}` | Envelope ở trên |
| DB và metadata | `/rpc/db/{method}`, `/rpc_meta` | Danh sách/khởi tạo database và metadata | `{"id","result"}` hoặc `{"id","error":{"code","message"}}` với HTTP status lịch sử của chúng |
| OAuth | `/oauth/token`, `/oauth/authorize`, `/oauth/revoke`, `/oauth/me` (cũng dưới `/auth/*`) | Lấy và thu hồi token | OAuth chuẩn (RFC 6749): object token, hoặc `{"error": "invalid_grant", "error_description": "..."}` |
| MCP | `POST /mcp` | Truy cập công cụ AI | JSON-RPC 2.0 với `result.content`; lỗi tool có dạng `"<code>: <message>"` |
| API docs | `GET /doc/index.json`, `/doc/{model}.json`, `/doc/openapi.json` | Nguồn để dựng nên hướng dẫn này | Tài liệu JSON |
| EDC device callback | `POST /base/transaction/{init,result,settle}`, `GET /base/devices/config/:serial`, `POST /base/device/log/upload`, `POST /device/provision` | Các lời gọi do máy thanh toán thực hiện | Body cố định mà terminal đã parse; mỗi trang nêu định dạng riêng |

* Mọi thứ đánh dấu `Public` trong tab Reference đều gọi được qua RPC.
* Kết quả MQTT và Lua dùng `{"ok": true}` hoặc `{"ok": false, "reason": "..."}`.

## 8. Bảng thuật ngữ

| Thuật ngữ | Ý nghĩa đơn giản |
| --- | --- |
| Model | Một loại bản ghi, ví dụ `tbl.partner` hoặc `edc.device`. |
| Method | Một hành động trên model: `search_read`, `create`, `write`, `unlink`, hoặc method riêng. |
| RPC | "Remote procedure call": gọi một method theo tên bằng `POST /rpc/{model}/{method}`. |
| REST alias | Đường dẫn thân thiện hơn như `GET /base/devices`, chạy giúp bạn một RPC method. |
| Envelope | Lớp JSON bọc mọi câu trả lời: `{"id", "result"}` hoặc `{"id", "error"}`. |
| Bearer token | Chuỗi mật khẩu tạm thời lấy từ `/oauth/token`, gửi dưới dạng `Authorization: Bearer <token>`. |
| Refresh token | Chuỗi sống lâu hơn, dùng để lấy access token mới mà không cần mật khẩu. |
| ACL | "Access control list": bước kiểm tra quyền xem user được đọc hay sửa gì. |
| Idempotency key | Chuỗi duy nhất trong `X-Idempotency-Key`; gọi lại sẽ trả câu trả lời đầu tiên thay vì lặp lại hành động. |
| Serial | Số định danh duy nhất của một terminal EDC (máy thanh toán), ví dụ `SN0001`. |
| Activation code | Mã dùng một lần gồm 8 chữ số do operator tạo để terminal tự đăng ký. |
| Provisioning | Bước terminal đổi serial và activation code lấy secret vĩnh viễn của riêng nó. |
| MQTT | Hệ thống nhắn tin nhẹ mà server dùng để đẩy command xuống terminal. |

Để xem toàn bộ luồng của terminal, mở hướng dẫn EDC trong tab Reference.

## 9. Dành cho tác giả module: viết tài liệu cho một method

Các file dưới `internal/documents/apidoc/<lang>/` (`en`, `vn`, gồm cả thư mục con) được nhúng vào binary lúc build.
Tham chiếu một file bằng `DescriptionFile` trên `orm.ModelSpec` hoặc `orm.RuntimeMethod`, đường dẫn tương đối theo thư mục ngôn ngữ.
Thêm cùng file đó ở mọi ngôn ngữ; bản dịch thiếu sẽ dùng `en`.

```go
{Name: "ping", Description: "Short summary.", DescriptionFile: "x/ping.md"}
```

* `Description` là văn bản thuần. `DescriptionFile` hiển thị sau **Show Description** trong tab Reference.
* README này được phục vụ làm tab Guide qua `GET /doc/guide.md`.
* Hãy trả typed error (`orm.NotFoundf`, `orm.Forbiddenf`, `orm.Conflictf`, `orm.InvalidParamsf`) thay vì `error` thường
  để caller nhận code chính xác. `error` thường trở thành `-32000`.
