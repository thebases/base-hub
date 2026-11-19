## Bulk Import Devices

Tạo hoặc cập nhật nhiều thiết bị trong một lệnh gọi, khớp theo serial. Dùng để nạp danh sách thiết bị (ví dụ từ bảng tính).

> **Warning:** Thiết bị import **không** có mã kích hoạt. Trước khi máy provision được, hãy đặt mã bằng `POST /base/device/activation` (xem **Create Device** để biết toàn bộ quy trình).

### Trước khi bắt đầu

Cần bearer token của user có quyền ghi `edc.device`; nếu không sẽ nhận `403`.

### Request

```http
POST /base/devices/bulk-import
Authorization: Bearer <token>
Content-Type: application/json
```

| Tên | Kiểu | Bắt buộc | Ý nghĩa |
| --- | --- | --- | --- |
| `devices` | array | Có | Danh sách thiết bị. Ít nhất một phần tử. |
| `devices[].serial` | string | Có | Serial của máy, ví dụ `SN0001` (`serial_number` cũng được). Khoảng trắng bị bỏ. Không được trùng trong danh sách. Gửi chữ hoa. |
| `devices[].imei` | string | Không | IMEI, ví dụ `356938035643809`. Thiếu hoặc để trống sẽ xóa giá trị. |
| `devices[].ownerId` | int | Không | ID tài khoản chủ sở hữu, ví dụ `10` (`owner_id` cũng được). Thiếu hoặc không phải số sẽ thành `0`. |
| `devices[].holderId` | int | Không | ID người giữ máy (merchant hoặc user), ví dụ `25` (`holder_id` cũng được). Thiếu hoặc không phải số sẽ thành `0`. |
| `devices[].active` | bool | Không | Thiết bị mới mặc định `true`. Thiết bị đã có giữ giá trị hiện tại nếu bạn bỏ qua. |

### Ví dụ

```bash
curl -X POST "$BASE_URL/base/devices/bulk-import" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"devices":[
        {"serial":"SN0001","imei":"356938035643809","ownerId":10,"holderId":25},
        {"serial":"SN0002","imei":"356938035643810","ownerId":10,"holderId":26}
      ]}'
```

### Response

`200`:

```json
{
  "id": "111020261535481234570001",
  "result": { "created": 1, "updated": 1, "total": 2 }
}
```

| Trường | Ý nghĩa |
| --- | --- |
| `result.created` | Số serial mới được thêm. |
| `result.updated` | Số serial đã có được thay đổi. |
| `result.total` | Số phần tử bạn đã gửi. |

Thiết bị mới vào nhóm `default` và có một dòng trạng thái. Thiết bị đã có giữ nguyên nhóm và trạng thái.

### Lỗi

Lỗi dùng định dạng chuẩn được mô tả trong hướng dẫn API (xem "Response" → "Errors"). Mã chuỗi cũ nằm trong `error.data.reason`.

| HTTP | `error.code` | Ý nghĩa | Bạn nên làm gì |
| --- | --- | --- | --- |
| `400` | `-32700` / `-32000` | JSON sai, danh sách `devices` rỗng, một phần tử thiếu `serial`, serial bị lặp, hoặc lỗi cơ sở dữ liệu. `error.message` cho biết lý do. | Sửa phần tử rồi gửi lại. |
| `401` | `-32001` | Thiếu token hoặc token không hợp lệ. | Lấy token mới. |
| `403` | `-32003` | User không được ghi `edc.device`. | Nhờ admin cấp quyền. |

### Cần biết

- **Được ăn cả, ngã về không.** Nếu một phần tử lỗi, không có gì được lưu. Sửa rồi gửi lại cả danh sách. Gửi lại là an toàn vì thiết bị được khớp theo serial.
- **Thiết bị đã có bị ghi đè.** `imei`, `ownerId`, `holderId` bị thay bằng giá trị bạn gửi. Nếu bỏ qua, IMEI bị xóa và các ID thành `0`. Luôn gửi đầy đủ giá trị.
- `ownerId` / `holderId` thiếu được lưu là `0` ở đây, nhưng là `1` trong `POST /base/devices`.
- Các key khác trong từng phần tử (ví dụ `deviceConfig`) bị bỏ qua. Không giới hạn số phần tử.
- Cũng có thể gọi: `POST /base/edc/devices/bulk-import` với `{"kwargs":{"devices":[...]}}`.
