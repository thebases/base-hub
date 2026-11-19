## Build Signing String

Turns a JSON object into the `key=value&key=value` text that is used as HMAC input. Use it to check that your own client builds exactly the same text before it signs.

### Request

```http
POST /jsonstring
Content-Type: application/json
```

No login is needed.

| Name | Type | Required | What it means |
| --- | --- | --- | --- |
| `jsonObj` | object | Yes | Flat key/value data, e.g. `{"serial":"SN0001","amount":150000}`. |
| `keyList` | string[] | No | Keys to include. Empty or missing = all keys of `jsonObj`. |

Keys are always sorted alphabetically, whatever order `keyList` uses. Values are printed as they are, without URL encoding.

### Example

```bash
curl -X POST "$BASE_URL/jsonstring" \
  -H "Content-Type: application/json" \
  -d '{"jsonObj":{"serial":"SN0001","amount":150000,"billId":"INV20260926"},"keyList":["serial","billId","amount"]}'
```

### Response

```json
{
  "code": 0,
  "data": { "result": "amount=150000&billId=INV20260926&serial=SN0001" }
}
```

| Field | What it means |
| --- | --- |
| `data.result` | The text to sign. |

This route keeps the simple `{code, data}` body, not the `{"id","result"}` envelope from the API guide.

### Errors

The HTTP status of every error is `500`. The real reason is the `code` field inside the body.

| HTTP | `code` / `message` | What it means | What you should do |
| --- | --- | --- | --- |
| `500` | `400` / `invalid request body` | The body is not valid JSON, or a `keyList` entry is not a key of `jsonObj`. Both give the same message. | Check the JSON and the key spelling. |

### Good to know

- Sorting is case-sensitive: upper-case keys come before lower-case ones (`Z` before `a`).
- Large numbers may print as `1e+07` (for `10000000`). Send big amounts as strings.
- Keep `jsonObj` flat. Nested objects print in Go style (`map[k:v]`).
