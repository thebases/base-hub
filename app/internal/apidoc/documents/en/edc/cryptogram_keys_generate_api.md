## Generate HMAC and AES Keys

Creates a new random HMAC key and AES-256 key. Use it in development to get keys for the other helper routes (`POST /aes`, `POST /aeshmac`).

### Request

```http
GET /keys
```

No login is needed. Anyone who can reach the server can call it. The keys are random and are not linked to any real secret.

### Example

```bash
curl "$BASE_URL/keys"
```

### Response

```json
{
  "code": 0,
  "data": {
    "hmacKey": "<64-hex-char-hmac-key>",
    "aesKey": "<64-hex-char-aes-key>"
  }
}
```

| Field | What it means |
| --- | --- |
| `code` | `0` means success. |
| `data.hmacKey` | A new key for HMAC signing: 32 random bytes written as 64 hex characters. |
| `data.aesKey` | A new AES-256 key: 32 random bytes written as 64 hex characters. |

This route keeps the simple `{code, data}` body, not the `{"id","result"}` envelope from the API guide.

### Errors

| HTTP | `message` | What it means | What you should do |
| --- | --- | --- | --- |
| `500` | `Fail to create keys` | The server could not make random bytes. Body: `{"code": 500, "message": "Fail to create keys"}`. | Retry. If it keeps failing, report it. |

### Good to know

- The keys are new on every call and are not stored. Save them yourself.
- For testing only. Never use keys you got over HTTP as production secrets.
