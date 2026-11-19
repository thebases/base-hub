## Get Bank App Info

Returns one bank app configuration (TID/MID) by its id. Use it to fill an edit form.

### Before you start

- You need a bearer token.
- Your user needs permission to read `edc.tidmid.config` records. If not, you get `403`.
- Get the `id` from "List Bank App Info".

### Request

```http
GET /base/bank-app-info/{id}
Authorization: Bearer <token>
```

| Name | Type | Required | What it means |
| --- | --- | --- | --- |
| `id` | integer | Yes | Bank app info id (in the path), e.g. `7`. |

### Example

```bash
curl "$BASE_URL/base/bank-app-info/7" \
  -H "Authorization: Bearer $TOKEN"
```

### Response

HTTP `200`:

```json
{
  "id": "111020261535481234570001",
  "result": {
    "id": 7,
    "appBankInfo": "BIDV",
    "bincode": "970418",
    "defaultApp": true,
    "encryptType": "HEXHL",
    "environment": "prod",
    "ip": "10.20.30.40",
    "port": "17109",
    "nii": "003",
    "packageName": "com.wdvn.bidv",
    "refNo": "08062026",
    "thirdPartyId": "27981340",
    "tid": "12345678",
    "mid": "000000012345678",
    "createdAt": "2026-09-20T03:15:00Z",
    "updatedAt": "2026-09-25T09:40:12Z"
  }
}
```

| Field | What it means |
| --- | --- |
| `id` | The bank app info id. Use it in the get, update and delete routes. |
| `appBankInfo`, `bincode`, `encryptType`, `environment`, `ip`, `port`, `nii`, `packageName`, `refNo`, `thirdPartyId`, `tid`, `mid` | The saved values, same meaning as in the request. |
| `defaultApp` | Present only when `true`. |
| `createdAt` / `updatedAt` | When the record was created and last changed (UTC, ISO 8601). |

`secret` is never in the response. Fields that are null, `""` or `false` are left out.

### Errors

Errors use the standard format described in the API guide (see "Response" → "Errors").

| HTTP | `error.code` | What it means | What you should do |
| --- | --- | --- | --- |
| `400` | `-32602` | `id` is not a number. | Send an integer id. |
| `401` | `-32001` | Missing or invalid token. | Get a new token. |
| `403` | `-32003` | Your user may not read bank app info. | Ask an admin for access. |
| `404` | `-32004` | No record has this id. | Check the id. |
| `500` | `-32603` | The record could not be loaded. | Retry later. Report the response `id`. |

### Good to know

- `secret` is never returned. It is write-only.
- Null, `""` and `false` fields are left out, so `defaultApp` appears only when it is `true`.
