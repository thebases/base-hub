## Lấy ngân hàng theo mã

Tra cứu một ngân hàng Việt Nam theo mã NAPAS BIN (mã ngân hàng 6 chữ số, ví dụ `970436` = Vietcombank). Dùng để hiển thị tên ngân hàng cạnh BIN, hoặc kiểm tra BIN trước khi tạo mã VietQR.

### Request

```http
GET /banklist/{bankCode}
```

Không cần đăng nhập.

| Tên | Kiểu | Bắt buộc | Ý nghĩa |
| --- | --- | --- | --- |
| `bankCode` (path) | string | Có | Mã BIN ngân hàng 6 chữ số, ví dụ `970436`, `970418`, `970407`. |

### Ví dụ

```bash
curl "$BASE_URL/banklist/970436"
```

### Response

```json
{
  "code": 0,
  "data": {
    "bank": {
      "FullName": "970436",
      "Abbrev": "VCB",
      "Code": "TMCP Ngoại Thương Việt Nam"
    }
  }
}
```

| Trường | Thực tế chứa gì |
| --- | --- |
| `code` | `0` nghĩa là thành công. |
| `data.bank.FullName` | Mã BIN (tên trường không khớp với giá trị). |
| `data.bank.Abbrev` | Tên viết tắt của ngân hàng. |
| `data.bank.Code` | Tên đầy đủ của ngân hàng (tên trường không khớp với giá trị). |

> **Warning:** trong dữ liệu hiện tại các giá trị bị lệch. Đừng tin tên trường; hãy dùng bảng trên.

Route này giữ dạng đơn giản `{code, data}`, không dùng envelope `{"id","result"}` trong hướng dẫn API.

### Lỗi

HTTP status của lỗi là `500`. Lý do thật nằm ở trường `code` trong body.

| HTTP | `code` / `message` | Ý nghĩa | Bạn nên làm gì |
| --- | --- | --- | --- |
| `500` | `404` / `Bank not found` | BIN không có trong danh sách ngân hàng. | Kiểm tra lại 6 chữ số. |
| `404` | (route not found) | Thiếu `bankCode` (`GET /banklist` hoặc `/banklist/`). | Thêm mã vào path. |

### Cần biết

- Thực tế không có lệnh "liệt kê tất cả ngân hàng": luôn cần một mã.
