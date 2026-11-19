## Upload File to S3

This route does not exist in Base HUB. There is no `POST /s3/upload`, and the old bedc route was not ported. Do not build on it.

Use the attachment upload instead. It saves the file in Base HUB's own file store and creates a `sys.attachment` record (an "attachment" is a stored file plus its details).

### Before you start

You need a bearer token (see the API guide) whose user may create attachments.

### Request

```http
POST /web/content
Authorization: Bearer <token>
Content-Type: multipart/form-data
```

| Name | Type | Required | What it means |
| --- | --- | --- | --- |
| `file` | file | Yes | The file content. `ufile` also works as the field name. The server keeps your `Content-Type` unless it is empty or HTML-like; then it detects the type from the content. |
| `name` | form field | No | File name to store. Default: the uploaded file's name. |
| `sys_model` | form field | No | Model name of a record to attach the file to, e.g. `edc.device.log`. |
| `model_id` | form field | No | Id of that record, e.g. `42`. |
| `is_public` | form field | No | `true` or `1` lets anyone download without a token. |
| `company_id`, `user_id`, `group_id` | form field | No | Who may see the file. Set at most one of them. |

Instead of multipart, you can send JSON: `{"name": "...", "datas": "<base64>", "sys_model": "...", "model_id": 42, "mimetype": "..."}`.

### Example

```bash
curl -X POST "$BASE_URL/web/content" \
  -H "Authorization: Bearer $TOKEN" \
  -F "name=receipt-20260926.pdf" \
  -F "file=@./receipt-20260926.pdf;type=application/pdf"
```

### Response

```json
{
  "result": {
    "id": 5120,
    "name": "receipt-20260926.pdf",
    "mimetype": "application/pdf",
    "checksum": "9b2cf535f27731c974343645a3985328c0d3a3c7e5f2b4a1d6e8f90123456789",
    "file_size": 48213,
    "create_uid": 7,
    "is_public": false
  }
}
```

| Field | What it means |
| --- | --- |
| `id` | Attachment id. Download with `GET /web/content/{id}`. |
| `checksum` | Fingerprint of the file content. |
| `file_size` | Size in bytes. |
| `is_public` | `true` means no token is needed to download. |

The row also has the usual record fields (`active`, `created_at`, `updated_at`, ...). Key names use snake_case.

### Errors

Errors here are plain-text bodies with the HTTP status, not the JSON error format.

| HTTP | What it means | What you should do |
| --- | --- | --- |
| `400` | No `file`/`ufile` part and no valid JSON with base64 `datas`; the file could not be read; `datas` is empty or not base64; or the name/scope/binding fields are invalid. | Read the reason in the body and fix the request. |
| `401` | Missing or invalid token. | Log in again. |
| `403` | The user cannot create attachments (`Permission denied: ...`). | Ask an admin for access. |
| `413` | The file is too big. Default limit is 20 MB (database setting `attachment.max_size_mb`). | Send a smaller file. |

### Good to know

- The same content is stored once per database, but every upload still creates its own attachment record.
- Device log files sent to `POST /base/device/log/upload` are stored the same way. The log entry's `storageUrl` is `/web/content/<attachmentId>`.
