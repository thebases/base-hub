## Thêm thiết bị vào nhóm

Đưa một thiết bị vào một nhóm. Một thiết bị chỉ ở một nhóm, nên thêm vào đây sẽ chuyển nó khỏi nhóm cũ.

### Trước khi bắt đầu

- Bạn cần bearer token của user có quyền ghi thiết bị (kiểm tra quyền, ACL) và đọc nhóm. Không có quyền sẽ nhận `403`.
- Bạn cần `id` của nhóm và `id` số của thiết bị (không phải serial).

### Request

```http
POST /base/groups/{id}/devices
Authorization: Bearer <token>
Content-Type: application/json
```

| Tham số path | Kiểu | Bắt buộc | Ý nghĩa |
| --- | --- | --- | --- |
| `id` | int | Có | Id nhóm, ví dụ `3`. |

| Trường body | Kiểu | Bắt buộc | Ý nghĩa |
| --- | --- | --- | --- |
| `deviceId` | int | Có | Id số của thiết bị, ví dụ `42`. Đây là `id` từ các API thiết bị, không phải serial. |
| `groupId` | int | Không | Bị bỏ qua. Nhóm luôn lấy từ path. |

### Ví dụ

```bash
curl -X POST "$BASE_URL/base/groups/3/devices" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"deviceId":42}'
```

### Response

HTTP `201`. `result` là thiết bị sau khi cập nhật, với `groupId` đặt theo nhóm trong path:

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

Thiết bị có nhiều trường hơn mức bạn thường cần. Trường quan trọng là `groupId`. `imei`, `mqttClientId`, `lastProvisionedAt` và `revokedAt` bị bỏ khi rỗng.

### Lỗi

Lỗi dùng định dạng chuẩn được mô tả trong hướng dẫn API (xem "Response" → "Errors").

| HTTP | `error.code` | Ý nghĩa | Bạn nên làm gì |
| --- | --- | --- | --- |
| `400` | `-32602` | `id` không phải số nguyên. | Gửi một số. |
| `400` | `-32700` | Body không phải JSON hợp lệ. | Sửa JSON. |
| `400` | `-32000` (`data.reason` = `add_device_failed`) | Thiếu `deviceId` hoặc không phải số nguyên, hoặc không lưu được thiết bị. | Gửi `deviceId` dạng số. |
| `401` | `-32001` | Thiếu hoặc sai token. | Lấy token mới. |
| `403` | `-32003` | User không có quyền với thiết bị (hoặc không đọc được nhóm). | Xin admin cấp quyền. |
| `404` | `-32004` | Không có nhóm với id này, hoặc không có thiết bị với `deviceId` này. | Kiểm tra cả hai id. |

### Cần biết

- Nhóm được kiểm tra trước (trước khi đọc body), sau đó mới đến thiết bị.
- An toàn khi thử lại: thêm thiết bị đã ở trong nhóm này sẽ trả lại thiết bị đó với `201`.
- Để đưa thiết bị ra khỏi nhóm, dùng `DELETE /base/groups/{id}/devices/{deviceId}`.
