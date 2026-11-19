## Update Device

Đổi IMEI, owner, holder và cờ active của thiết bị. Chỉ các key bạn gửi mới bị đổi. Không đổi được serial.

### Trước khi bắt đầu

Bạn cần bearer token (xem API guide) của user có quyền ghi `edc.device`.

### Yêu cầu

```http
PUT /base/devices/{serial}
Authorization: Bearer <token>
Content-Type: application/json
```

| Tên | Kiểu | Bắt buộc | Ý nghĩa |
| --- | --- | --- | --- |
| `serial` | string (path) | Có | Serial chính xác của thiết bị, ví dụ `SN0001`. |
| `imei` | string | Không | IMEI mới (số phần cứng như điện thoại). Bỏ qua để giữ nguyên; gửi `null` để xóa. |
| `ownerId` | integer | Không | Id owner mới, ví dụ `10`. Bỏ qua để giữ nguyên. |
| `holderId` | integer | Không | Id holder mới, ví dụ `30`. Bỏ qua để giữ nguyên. |
| `active` | boolean | Không | `true` hoặc `false`. Bỏ qua để giữ nguyên. |
| `serial` (body) | string | Không | Nếu gửi, phải bằng serial trong path. Giá trị khác bị từ chối (`serial is immutable`). |

### Ví dụ

```bash
curl -X PUT "$BASE_URL/base/devices/SN0001" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"imei":"356938035643809","ownerId":10,"holderId":30,"active":true}'
```

### Phản hồi

`200 OK`. Thiết bị đã cập nhật như được lưu, nằm trong envelope `result` chuẩn.

```json
{
  "id": "111020261535481234570001",
  "result": {
    "id": 42,
    "serial": "SN0001",
    "imei": "356938035643809",
    "ownerId": 10,
    "holderId": 30,
    "groupId": 1,
    "active": true,
    "provisioningStatus": "ACTIVE",
    "mqttClientId": "SN0001",
    "secretVersion": 1,
    "lastProvisionedAt": "2026-09-26T08:05:00Z",
    "createdAt": "2026-09-26T08:00:00Z",
    "updatedAt": "2026-09-26T09:30:00Z"
  }
}
```

Các key khác trong body (như `groupId` hoặc `provisioningStatus`) bị bỏ qua. Đổi group bằng `POST /base/groups/{id}/devices`.

### Lỗi

Lỗi dùng định dạng chuẩn được mô tả trong API guide (xem "Response" → "Errors").

| HTTP | `error.code` | Ý nghĩa | Bạn nên làm gì |
| --- | --- | --- | --- |
| `400` | `-32700` | Body không phải JSON hợp lệ. | Sửa JSON. |
| `400` | `-32000` | `serial` trong body khác serial đã lưu, hoặc không lưu được thiết bị. | Bỏ `serial` khỏi body hoặc cho khớp. |
| `401` | `-32001` | Thiếu hoặc sai token. | Lấy token mới. |
| `403` | `-32003` | User không có quyền ghi `edc.device`. | Nhờ admin cấp quyền. |
| `404` | `-32004` | Không có thiết bị nào với serial này. | Kiểm tra chính tả. |

### Cần biết

- Đây là cập nhật một phần. Key bỏ qua giữ nguyên giá trị đã lưu.
- Dùng `"active": true` để bật lại thiết bị đã bị `DELETE /base/devices/{serial}` tắt.
- Các field provisioning (`provisioningStatus`, secret, MQTT client id) không bị đổi ở đây.

---

## Add Device to Group

Đưa thiết bị vào một group bằng cách đặt `groupId` của nó. Một thiết bị thuộc tối đa một group, nên thêm nó vào group mới sẽ chuyển nó khỏi group cũ.

### Yêu cầu

```http
POST /base/groups/{id}/devices
Authorization: Bearer <token>
Content-Type: application/json
```

Bạn cần user có quyền ghi `edc.device` (và đọc `edc.group`).

| Tên | Kiểu | Bắt buộc | Ý nghĩa |
| --- | --- | --- | --- |
| `id` | integer (path) | Có | Id của group, ví dụ `3`. |
| `deviceId` | integer | Có | Id số của thiết bị, ví dụ `42`. Đây là `id` trong các API thiết bị, **không** phải serial. |
| `groupId` | integer | Không | Bị bỏ qua. Group luôn lấy từ path. |

### Ví dụ

```bash
curl -X POST "$BASE_URL/base/groups/3/devices" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"deviceId":42}'
```

### Phản hồi

`201 Created`. Thiết bị đã cập nhật nằm trong `result`, với `groupId` đặt theo group trong path.

```json
{
  "id": "111020261535481234570001",
  "result": {
    "id": 42,
    "serial": "SN0001",
    "imei": "356938035643809",
    "ownerId": 10,
    "holderId": 25,
    "groupId": 3,
    "active": true,
    "provisioningStatus": "ACTIVE",
    "mqttClientId": "SN0001",
    "secretVersion": 1,
    "lastProvisionedAt": "2026-09-26T08:05:00Z",
    "createdAt": "2026-09-26T08:00:00Z",
    "updatedAt": "2026-09-26T08:05:00Z"
  }
}
```

### Lỗi

Lỗi dùng định dạng chuẩn được mô tả trong API guide (xem "Response" → "Errors").

| HTTP | `error.code` | Ý nghĩa | Bạn nên làm gì |
| --- | --- | --- | --- |
| `400` | `-32602` | `id` không phải số. | Gửi id là số nguyên. |
| `400` | `-32700` | Body không phải JSON hợp lệ. | Sửa JSON. |
| `400` | `-32000` | `deviceId` thiếu hoặc không phải số nguyên, hoặc không lưu được thiết bị. | Gửi `deviceId` là số nguyên. |
| `401` | `-32001` | Thiếu hoặc sai token. | Lấy token mới. |
| `403` | `-32003` | User không có quyền ghi `edc.device` hoặc đọc `edc.group`. | Nhờ admin cấp quyền. |
| `404` | `-32004` | Không có group hoặc thiết bị với id này. | Kiểm tra cả hai id. Group được kiểm tra trước. |

### Cần biết

- An toàn khi retry: thêm thiết bị đã có trong group này sẽ trả lại nó với `201`.
- Để đưa thiết bị ra khỏi group, dùng `DELETE /base/groups/{id}/devices/{deviceId}`.

---

## Remove Device from Group

Gỡ thiết bị khỏi group bằng cách xóa `groupId` của nó. Bản thân thiết bị không bị xóa.

### Yêu cầu

```http
DELETE /base/groups/{id}/devices/{memberID}
Authorization: Bearer <token>
```

Bạn cần user có quyền ghi `edc.device`.

| Tên | Kiểu | Bắt buộc | Ý nghĩa |
| --- | --- | --- | --- |
| `id` | integer (path) | Có | Id của group. Thiết bị phải đang ở trong group này. |
| `memberID` | integer (path) | Có | Id của thiết bị (`id` từ *List Devices in Group*), **không** phải serial. |

### Ví dụ

```bash
curl -X DELETE "$BASE_URL/base/groups/3/devices/101" \
  -H "Authorization: Bearer $TOKEN"
```

### Phản hồi

`200 OK`.

```json
{
  "id": "111020261535481234570001",
  "result": { "deleted": true, "id": 101 }
}
```

`result.id` là id của thiết bị.

### Lỗi

Lỗi dùng định dạng chuẩn được mô tả trong API guide (xem "Response" → "Errors").

| HTTP | `error.code` | Ý nghĩa | Bạn nên làm gì |
| --- | --- | --- | --- |
| `400` | `-32602` | `id` hoặc `memberID` không phải số. | Gửi id là số nguyên. |
| `401` | `-32001` | Thiếu hoặc sai token. | Lấy token mới. |
| `403` | `-32003` | User không có quyền ghi `edc.device`. | Nhờ admin cấp quyền. |
| `404` | `-32004` | Không có thiết bị nào với `memberID` này, hoặc nó không ở trong group `id` (cũng xảy ra khi xóa lặp lại). | Kiểm tra các id. |
| `500` | `-32603` | Lỗi server không mong đợi. | Thử lại sau và báo `id` của response. |

### Cần biết

- Group trong path được kiểm tra. Thiết bị ở group khác (hoặc không có group) trả `404` và không có gì thay đổi.
- Sau khi gỡ, thiết bị không có group. Với MQTT topic (`base/<group name>/<serial>/cmd`) nó dùng tên group `default`.

---

## Call as RPC method: `edc.device.write`

Dùng method ghi chuẩn từ code phía server.

```http
POST /rpc/edc.device/write/{id}
Authorization: Bearer <token>
Content-Type: application/json
```

Id thiết bị nằm trong path (cho phép danh sách id cách nhau bởi dấu phẩy). Đặt các giá trị trong phần tử đầu tiên của `args`.

| Tên | Kiểu | Bắt buộc | Ý nghĩa |
| --- | --- | --- | --- |
| `id` | integer (path) | Có | Id thiết bị, ví dụ `42`. |
| `args[0]` | object | Có | Giá trị field cần ghi, dạng snake_case, ví dụ `{"holder_id": 30, "active": true}`. |

```bash
curl -X POST "$BASE_URL/rpc/edc.device/write/42" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"args": [{"holder_id": 30, "active": true}], "kwargs": {}}'
```

`result` là `true` khi ghi thành công. Đổi `serial` bị từ chối (`serial is immutable`). Khác với route REST, method này nhận mọi field ghi được, nên chỉ gửi những field bạn muốn đổi.
