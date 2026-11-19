## List Bank App Info

Returns a page of bank app configurations (TID/MID, bank host, package name), newest first. Use it to show a table, or to pick one to attach to a device config.

### Before you start

- You need a bearer token.
- Your user needs permission to read `edc.tidmid.config` records. If not, you get `403`.

### Request

```http
GET /base/bank-app-info?limit=50&offset=0&search={text}&default_app={bool}&environment={env}
Authorization: Bearer <token>
```

| Name | Type | Required | What it means |
| --- | --- | --- | --- |
| `limit` | integer | No | Page size, 1 to 500. Default `50`. A value that is invalid or out of range becomes `50`. |
| `offset` | integer | No | How many rows to skip. Default `0`. |
| `search` | string | No | Partial match, ignoring case, on `appBankInfo`, `packageName`, `tid`, `mid` or `bincode`. E.g. `BIDV`. |
| `default_app` | boolean | No | `true` or `false`. An invalid value is ignored (no filter). |
| `environment` | string | No | Exact match, e.g. `dev` or `prod`. |

### Example

```bash
curl "$BASE_URL/base/bank-app-info?search=BIDV&environment=prod&limit=20" \
  -H "Authorization: Bearer $TOKEN"
```

### Response

HTTP `200`:

```json
{
  "id": "111020261535481234570001",
  "result": {
    "items": [
      {
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
    ],
    "total": 1
  }
}
```

| Field | What it means |
| --- | --- |
| `result.items` | The records of this page. Each item has the same fields as in "Get Bank App Info". |
| `result.total` | How many records match the filters in all pages. Use it for paging. |

### Errors

Errors use the standard format described in the API guide (see "Response" → "Errors").

| HTTP | `error.code` | What it means | What you should do |
| --- | --- | --- | --- |
| `401` | `-32001` | Missing or invalid token. | Get a new token. |
| `403` | `-32003` | Your user may not read bank app info. | Ask an admin for access. |
| `500` | `-32603` | The list could not be loaded. | Retry later. Report the response `id`. |

### Good to know

- Null, `""` and `false` fields are left out of each item. For example there is no `tid` key when TID is not set, and `defaultApp` appears only when `true`.
- `secret` is never returned by any bank app info route.
