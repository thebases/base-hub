## Update Bank App Info

Changes the fields you send on an existing bank app configuration (TID/MID). Fields you do not send stay as they are.

### Before you start

- You need a bearer token.
- Your user needs permission to edit `edc.tidmid.config` records. If not, you get `403`.

### Request

```http
PUT /base/bank-app-info/{id}
Authorization: Bearer <token>
Content-Type: application/json
```

| Name | Type | Required | What it means |
| --- | --- | --- | --- |
| `id` | integer | Yes | Bank app info id (in the path), e.g. `7`. |

Body fields are the same as in "Create Bank App Info". Send only the keys you want to change. Unknown keys are ignored.

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
curl -X PUT "$BASE_URL/base/bank-app-info/7" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"ip": "10.20.30.41", "port": "17109"}'
```

### Response

HTTP `200`. The updated record is inside `result`:

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
    "ip": "10.20.30.41",
    "port": "17109",
    "nii": "003",
    "packageName": "com.wdvn.bidv",
    "refNo": "08062026",
    "thirdPartyId": "27981340",
    "tid": "12345678",
    "mid": "000000012345678",
    "createdAt": "2026-09-20T03:15:00Z",
    "updatedAt": "2026-09-26T08:05:00Z"
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
| `400` | `-32602` or `-32000` | `id` is not a number, the body is not valid JSON, or a field has the wrong type. | Fix the request. |
| `401` | `-32001` | Missing or invalid token. | Get a new token. |
| `403` | `-32003` | Your user may not edit bank app info. | Ask an admin for access. |
| `404` | `-32004` | No record has this id. | Check the id. |

### Good to know

- This is a **partial update**. Keys you leave out keep their value, including `secret`. A key sent as `""` is stored as an empty string. Server defaults are not applied again.
- You cannot read `secret` back. Send it only when you want to change it.
- A change applies to every device config this record is attached to.
