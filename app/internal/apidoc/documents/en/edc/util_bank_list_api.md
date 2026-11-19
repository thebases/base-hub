## Get Bank by Code

Looks up one Vietnamese bank by its NAPAS BIN (the 6-digit bank code, e.g. `970436` = Vietcombank). Use it to show a bank name next to a BIN, or to check a BIN before you build a VietQR code.

### Request

```http
GET /banklist/{bankCode}
```

No login is needed.

| Name | Type | Required | What it means |
| --- | --- | --- | --- |
| `bankCode` (path) | string | Yes | 6-digit bank BIN, e.g. `970436`, `970418`, `970407`. |

### Example

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

| Field | What it actually holds |
| --- | --- |
| `code` | `0` means success. |
| `data.bank.FullName` | The BIN code (the name does not match the value). |
| `data.bank.Abbrev` | The short bank name. |
| `data.bank.Code` | The full bank name (the name does not match the value). |

> **Warning:** the values are shifted in the current data. Do not trust the field names; use the table above.

This route keeps the simple `{code, data}` body, not the `{"id","result"}` envelope from the API guide.

### Errors

The HTTP status of the error is `500`. The real reason is the `code` field inside the body.

| HTTP | `code` / `message` | What it means | What you should do |
| --- | --- | --- | --- |
| `500` | `404` / `Bank not found` | The BIN is not in the bank list. | Check the 6 digits. |
| `404` | (route not found) | `bankCode` is missing (`GET /banklist` or `/banklist/`). | Add the code to the path. |

### Good to know

- There is no "list all banks" call in practice: a code is always required.
