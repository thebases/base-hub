## Create Bank App Info

Creates a bank app configuration: the TID/MID (terminal and merchant ids given by the bank), the bank host and the app package. You attach it to device configs afterwards.

### Before you start

- You need a bearer token.
- Your user needs permission to create `edc.tidmid.config` records. If not, you get `403`.

### Request

```http
POST /base/bank-app-info
Authorization: Bearer <token>
Content-Type: application/json
```

Every field is optional. Unknown keys are ignored.

| Name | Type | Required | What it means |
| --- | --- | --- | --- |
| `appBankInfo` | string | No | Bank or app label, e.g. `BIDV`. |
| `bincode` | string | No | Bank BIN code (the first digits of card numbers), e.g. `970418`. |
| `defaultApp` | boolean | No | `true` marks this as the default bank app. |
| `encryptType` | string | No | Encryption type, e.g. `HEXHL`. |
| `environment` | string | No | Environment label, e.g. `dev` or `prod`. |
| `ip` | string | No | IP address of the bank host, e.g. `10.20.30.40`. |
| `port` | string | No | Port of the bank host, **as a string**, e.g. `"17109"`. |
| `nii` | string | No | NII (network id the terminal uses with the bank), e.g. `003`. |
| `packageName` | string | No | Android package of the bank app, e.g. `com.wdvn.bidv`. |
| `refNo` | string | No | Reference number, e.g. `08062026`. |
| `secret` | string | No | Bank app secret. Write-only: stored, but never returned by any route. |
| `thirdPartyId` | string | No | Third-party id given by the bank, e.g. `27981340`. |
| `tid` | string | No | Terminal ID (the bank id of the terminal), e.g. `12345678`. |
| `mid` | string | No | Merchant ID (the bank id of the shop), e.g. `000000012345678`. |

### Example

```bash
curl -X POST "$BASE_URL/base/bank-app-info" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "appBankInfo": "BIDV",
    "bincode": "970418",
    "defaultApp": false,
    "environment": "prod",
    "ip": "10.20.30.40",
    "port": "17109",
    "packageName": "com.wdvn.bidv",
    "secret": "<bank-app-secret>",
    "thirdPartyId": "27981340",
    "tid": "12345678",
    "mid": "000000012345678"
  }'
```

### Response

HTTP `201`:

```json
{
  "id": "111020261535481234570001",
  "result": {
    "id": 8,
    "appBankInfo": "BIDV",
    "bincode": "970418",
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
    "createdAt": "2026-09-26T08:00:00Z",
    "updatedAt": "2026-09-26T08:00:00Z"
  }
}
```

The record is inside `result`.

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
| `400` | `-32602` or `-32000` | The body is not valid JSON, or a field has the wrong type (for example `port` sent as a number). | Fix the body. |
| `401` | `-32001` | Missing or invalid token. | Get a new token. |
| `403` | `-32003` | Your user may not create bank app info. | Ask an admin for access. |

### Good to know

- Keys you leave out get **server defaults**, not empty values: `encryptType=HEXHL`, `environment=dev`, a default `ip`, `port=17109`, `nii=003`, `packageName=com.wdvn.bidv`, and default `refNo`, `secret` and `thirdPartyId`. `defaultApp` stays unset. A key sent as `""` is stored as an empty string.
- Always send `environment`, `ip`, `port` and `secret` yourself. Then a record never uses a default by accident.
- Nothing is checked for duplicates. The same TID/MID can be created twice, and several records can have `defaultApp: true`.
