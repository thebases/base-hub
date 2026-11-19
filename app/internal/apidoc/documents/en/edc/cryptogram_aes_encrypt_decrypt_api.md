## AES Encrypt or Decrypt

Encrypts text, or decrypts a base64 ciphertext, with an AES key that you send. Use it to check that your device or client produces the same ciphertext as the backend.

### Before you start

- No login is needed. This is a public helper route.
- You need an AES key written as hex text (32, 48 or 64 hex characters). Get a test key from `GET /keys`.

### Request

```http
POST /aes
Content-Type: application/json
```

| Name | Type | Required | What it means |
| --- | --- | --- | --- |
| `data` | string | Yes | Encrypt: the plain text, e.g. `{"serial":"SN0001"}`. Decrypt: the base64 ciphertext. |
| `key` | string | Yes | AES key as hex: 32, 48 or 64 characters (AES-128, AES-192 or AES-256). |
| `isEncrypt` | boolean | No | `true` = encrypt. `false` or missing = decrypt. |

The algorithm is AES-CBC with an all-zero IV (starting block), PKCS#7 padding, and standard base64 output.

### Example

```bash
curl -X POST "$BASE_URL/aes" \
  -H "Content-Type: application/json" \
  -d '{"data":"{\"serial\":\"SN0001\",\"amount\":150000}","key":"<64-hex-char-aes-key>","isEncrypt":true}'
```

### Response

Encrypt (`isEncrypt: true`):

```json
{ "code": 0, "data": { "encryptedData": "<base64-ciphertext>" } }
```

Decrypt (`isEncrypt: false`):

```json
{ "code": 0, "data": { "rawData": "{\"serial\":\"SN0001\",\"amount\":150000}" } }
```

| Field | What it means |
| --- | --- |
| `code` | `0` means success. |
| `data.encryptedData` | The base64 ciphertext (encrypt only). |
| `data.rawData` | The decrypted text (decrypt only). |

This route does not use the `{"id","result"}` envelope from the API guide. It keeps the simple `{code, data}` body.

### Errors

Every error here has HTTP status `500`. The real reason is the `code` field inside the body: `{"code": 400, "message": "..."}`.

| HTTP | `code` / `message` | What it means | What you should do |
| --- | --- | --- | --- |
| `500` | `400` / `invalid request body` | The body is not valid JSON. | Fix the JSON. |
| `500` | `500` / `aes: decode key: ...` | Decrypt: `key` is not valid hex. | Send hex characters only. |
| `500` | `500` / `invalid key length: must be 16, 24, or 32 bytes` | Encrypt: the key is not 32, 48 or 64 hex characters. | Use a key from `GET /keys`. |
| `500` | `500` / `crypto/aes: invalid key size N` | Decrypt: the key has the wrong length. | Use a 32, 48 or 64 character hex key. |
| `500` | `500` / `illegal base64 data at input byte N` | Decrypt: `data` is not valid base64. | Send the exact ciphertext you received. |
| `500` | `500` / `aes: ciphertext must be a non-empty multiple of block size` | Decrypt: the ciphertext is empty or has a wrong length. | Check that it was not cut or changed. |

### Good to know

- The IV is always zero, so the same text and key always give the same ciphertext. This is fine for tests, not for protecting real data.
- A wrong key usually does not return an error when decrypting. You get garbage text in `rawData`, so compare the result yourself.
- Encrypting with a key that is not valid hex can crash the underlying helper instead of returning an error. Always send a valid hex key.
