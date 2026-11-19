## Sign and/or Encrypt JSON

Takes a JSON object and, if you ask, signs it with HMAC-SHA256 (a signature that proves the data was not changed) and/or encrypts it with AES-256. The server makes new random keys for each call and returns them, so you can repeat the same steps in your own client.

### Before you start

No login is needed. This is a public helper route for development and testing.

### Request

```http
POST /aeshmac
Content-Type: application/json
```

| Name | Type | Required | What it means |
| --- | --- | --- | --- |
| `jsonObj` | object | Yes | The data to sign or encrypt. Use flat key/value pairs, e.g. `{"serial":"SN0001","amount":150000}`. |
| `keyList` | string[] | No | Keys of `jsonObj` to include in the signature, e.g. `["serial","amount"]`. Empty or missing = all keys. |
| `needSign` | boolean | No | `true` to build the HMAC signature. Default `false`. |
| `needAES256` | boolean | No | `true` to encrypt the whole `jsonObj`. Default `false`. |

How each part is built:

- **HMAC:** the chosen keys are sorted alphabetically (not in `keyList` order) and joined as `key1=value1&key2=value2`. That text is signed with HMAC-SHA256. The signature is lower-case hex. The HMAC key is used as the literal 64-character hex text, not decoded to bytes.
- **AES-256:** `jsonObj` is minified (keys in alphabetical order), then encrypted with AES-CBC, zero IV, PKCS#7 padding, base64 output. The AES key is hex-decoded to 32 bytes.

### Example

```bash
curl -X POST "$BASE_URL/aeshmac" \
  -H "Content-Type: application/json" \
  -d '{
    "jsonObj": {"serial":"SN0001","amount":150000,"billId":"INV20260926"},
    "keyList": ["serial","amount"],
    "needSign": true,
    "needAES256": true
  }'
```

### Response

All six fields are always present. Parts you did not ask for are empty strings.

```json
{
  "code": 0,
  "data": {
    "hmacRawData": "amount=150000&serial=SN0001",
    "hmacKey": "<64-hex-char-hmac-key>",
    "hmacSignData": "<64-hex-char-hmac-sha256-signature>",
    "aesMinifiedJson": "{\"amount\":150000,\"billId\":\"INV20260926\",\"serial\":\"SN0001\"}",
    "aesKey": "<64-hex-char-aes-key>",
    "aesEncryptData": "<base64-ciphertext>"
  }
}
```

| Field | What it means |
| --- | --- |
| `hmacRawData` | The exact text that was signed. |
| `hmacKey` | The random HMAC key used. |
| `hmacSignData` | The HMAC-SHA256 signature (hex). |
| `aesMinifiedJson` | The exact JSON text that was encrypted. |
| `aesKey` | The random AES key used. |
| `aesEncryptData` | The base64 ciphertext. |

This route keeps the simple `{code, data}` body, not the `{"id","result"}` envelope from the API guide.

### Errors

Every error here has HTTP status `500`. The real reason is the `code` field inside the body.

| HTTP | `code` / `message` | What it means | What you should do |
| --- | --- | --- | --- |
| `500` | `400` / `invalid request body` | The body is not valid JSON. | Fix the JSON. |
| `500` | `500` / `key 'x' not found in data` | A `keyList` entry is not a key of `jsonObj`. Checked only when `needSign` is `true`. | Fix the spelling in `keyList`. |

### Good to know

- You cannot send your own keys. To use a key you choose, call `POST /aes`.
- Keep `jsonObj` flat. Nested objects are printed in Go style (e.g. `map[a:1]`) inside `hmacRawData`.
- Numbers of 10,000,000 and above are printed as `1e+07` in `hmacRawData`. Send large amounts as strings.
