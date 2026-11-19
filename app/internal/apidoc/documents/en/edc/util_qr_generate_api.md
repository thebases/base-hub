## Generate VietQR Code

Builds a VietQR (NAPAS) bank-transfer QR code for an account and an amount. It returns the QR text (so you can draw it yourself) and a ready PNG image in base64.

### Request

```http
GET /qr?bankAccount={account}&binCode={bin}&amount={amount}&billId={billId}&narrative={text}
```

No login is needed.

| Name | Type | Required | What it means |
| --- | --- | --- | --- |
| `bankAccount` | string | Yes (not checked) | Receiving bank account or virtual account number, e.g. `0071000123456`. |
| `binCode` | string | Yes | 6-digit bank BIN, e.g. `970436`. Look it up with `GET /banklist/{bankCode}`. |
| `amount` | string | Yes | Amount in VND, digits only, e.g. `150000`. |
| `billId` | string | No | Bill or order reference, put in the transfer description, e.g. `INV20260926`. |
| `narrative` | string | No | Start of the description. Default `CT`. |
| `logo` | string | No | PNG logo as raw base64 (no `data:` prefix, URL-encoded). It is drawn small and round in the centre. URLs are not supported. |

The transfer description inside the QR is `<narrative> BAS<billId length, 2 digits><billId>`, for example `CT BAS11INV20260926`.

### Example

```bash
curl -G "$BASE_URL/qr" \
  --data-urlencode "bankAccount=0071000123456" \
  --data-urlencode "binCode=970436" \
  --data-urlencode "amount=150000" \
  --data-urlencode "billId=INV20260926"
```

### Response

The raw JSON, with no envelope:

```json
{
  "qrBase64": "iVBORw0KGgoAAAANSUhEUgAAAQAAAAEACAIAAADT...",
  "qrString": "00020101021238570010A00000072701270006970436011300710001234560208QRIBFTTA530370454061500005802VN62230819CT BAS11INV20260926630473CD"
}
```

| Field | What it means |
| --- | --- |
| `qrBase64` | A 256 x 256 PNG. Show it with `<img src="data:image/png;base64,{qrBase64}">`. |
| `qrString` | The EMVCo/VietQR text. Use any QR library to draw it at another size. |

### Errors

This route does not return error statuses. Problems with `logo` still give HTTP `200`, with the error text placed in `qrBase64`.

| HTTP | `qrBase64` value | What it means | What you should do |
| --- | --- | --- | --- |
| `200` | `Base64 decode error` | `logo` is not valid base64. | Send raw base64, URL-encoded. |
| `200` | `Decode image error` | `logo` decodes but is not a PNG. | Send a PNG. |

### Good to know

- Missing parameters are not rejected. An empty `amount` or `bankAccount` still returns a QR that banking apps will refuse. Validate on your side.
- Check that `qrBase64` starts with `iVBOR` (the PNG signature) before you show it.
- URL-encode `logo` and `narrative` (`--data-urlencode` or `encodeURIComponent`). A raw `+` in base64 becomes a space and breaks the logo.
