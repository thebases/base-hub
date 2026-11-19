## EDC Transaction Result

The terminal calls this after the bank app answers a payment (sale, void, QR...). This call makes the payment appear, with its final status and amount, in the admin transaction list.

> **Warning:** This is a device callback with a frozen body. The terminal firmware parses the response, so field names and shapes do not change.

### In a nutshell

1. Terminal calls **init** (payment starts), then the bank answers.
2. Terminal calls **result** with the bank's request and response, sent as JSON strings.
3. The server decides the status: `APPROVED`, `FAILED` or `TIMEOUT`. An approved **VOID** (cancelling an earlier sale) moves that sale to `REVERSED`.
4. Later the terminal calls **settle** (see "EDC Batch Settle"). A settlement is the bank's end-of-batch total.

Transaction statuses in plain words:

| Status | Meaning |
| --- | --- |
| `INITIATED` | Payment started; no answer yet. |
| `PENDING_DEVICE` | A start command was sent to the terminal and the server waits for it. |
| `PROCESSING` | The terminal is processing the payment. |
| `APPROVED` | The bank accepted the payment. |
| `DECLINED` / `FAILED` | The bank refused it, or it failed. |
| `TIMEOUT` | No answer in time. |
| `REVERSED` | An approved sale was cancelled by a void. |
| `SETTLED` | The batch containing this payment was closed with the bank. |

Status only moves forward. A late `FAILED` after `APPROVED` is ignored.

### Before you start

Same device authentication as "EDC Transaction Init": mode `public` (default, no token, the server finds the device from `deviceId`) or `token` (`Authorization: Bearer <serial>:<secretVersion>:<deviceSecret>`). A header you send is always verified. `X-Device-Serial`, `X-Timestamp`, `X-Nonce`, `X-Signature` are ignored.

### Request

```http
POST /base/transaction/result
Content-Type: application/json
```

The body is the same as `/transaction/init`.

| Body field | Type | Required | What it means |
| --- | --- | --- | --- |
| `deviceId` | string (max 128) | Yes | Terminal serial, e.g. `SN0001`. Old key `device_id`. |
| `transactionId` | string (max 128) | Yes | Same ID sent to init. For a **VOID**, send the original sale's `transactionId`. Old key `transaction_id`. |
| `requestBody` | string | See note | Bank-app request **as a JSON string**: `transactionID`, `transactionAmount` (integer string), `currencyName`, `tranxType`, `transDateTime`, `thirdPartyId`. Old key `request_body`. |
| `response` | string | See note | Bank-app response **as a JSON string**: `{"Code","Desc","tranxType","Response":{isoResponseCode, approveCode, totalAmount, currency, cardNumber (masked), cardHolder, cardType, swipeType, aid, appName, merchantId, terminalId, refNo, traceNo, batchNo, invoiceNo, merchantTransId, transDateTime}}`. A flat object with the `Response` fields also works. |
| `type` | string (max 32) | No | `SALE`, `VOID`... Used only when the JSON strings have no `tranxType`. |
| `gps`, `appVersion`, `bankApp` | string (max 128) | No | Terminal context. |

Note: `requestBody` and `response` are optional for validation, but without a readable `response` the payment is saved as `FAILED`. If either is not valid JSON, the payment never reaches the admin list.

How the server decides:

- **Status:** `APPROVED` when `Code` is `"200"` or `isoResponseCode` is `"00"`. `TIMEOUT` when `Desc` contains "timeout". Otherwise `FAILED`.
- **Amount:** `Response.totalAmount`, else `requestBody.transactionAmount`. Integer string in whole units (`"150000"` = 150,000 VND). Decimals or separators are ignored (amount `0`).
- **Time:** `Response.transDateTime`, else `requestBody.transDateTime` (RFC 3339, `2006-01-02 15:04:05+07:00`, or `20060102150405` in UTC), else the time the server received the call.

### Example

```bash
curl -X POST "$BASE_URL/base/transaction/result" \
  -H "Content-Type: application/json" \
  -d '{
    "deviceId": "SN0001",
    "transactionId": "TXN20260926000123",
    "type": "SALE",
    "requestBody": "{\"transactionID\":\"TXN20260926000123\",\"transactionAmount\":\"150000\",\"currencyName\":\"VND\",\"tranxType\":\"SALE\"}",
    "response": "{\"Code\":\"200\",\"Desc\":\"Success\",\"tranxType\":\"SALE\",\"Response\":{\"isoResponseCode\":\"00\",\"approveCode\":\"123456\",\"totalAmount\":\"150000\",\"currency\":\"VND\",\"cardNumber\":\"411111******1111\",\"cardType\":\"CHIP\",\"merchantId\":\"000000012345678\",\"terminalId\":\"70010001\",\"refNo\":\"626908123456\",\"traceNo\":\"000321\",\"batchNo\":\"000012\",\"invoiceNo\":\"000045\",\"transDateTime\":\"2026-09-26T08:15:00+07:00\"}}",
    "bankApp": "BANKAPP"
  }'
```

### Response

HTTP `200`, own format (no `result` envelope).

```json
{
  "success": true,
  "data": {
    "accepted": true,
    "duplicate": false,
    "transactionId": "TXN20260926000123",
    "status": "APPROVED",
    "receivedAt": "2026-09-26T08:15:02.401+07:00",
    "logId": "5531"
  }
}
```

| Field | What it means |
| --- | --- |
| `accepted` | `true` when the server saved the call. |
| `duplicate` | `true` when this result was already recorded (a retry). Nothing changed. |
| `transactionId` | The payment ID. |
| `status` | Stored status after this call: `APPROVED`, `FAILED`, `TIMEOUT`, or `REVERSED` for an approved void. |
| `receivedAt` | When the server first received this result. |
| `logId` | ID of the raw callback log. |

### Errors

Callback errors keep the old device format: `{"code": "...", "message": "..."}`. Validation errors look like `{"errors":[{"field":"TransactionID","message":"..."}]}`.

| HTTP | `code` | What it means | What you should do |
| --- | --- | --- | --- |
| `400` | `invalid_json` | Body is not valid JSON. | Fix the JSON. |
| `400` | none (`errors` list) | `deviceId` or `transactionId` missing, or a field too long. | Send the required fields. |
| `401` | none | Token missing or wrong (`token` mode, or a bearer header was sent). Body: `{"error":"missing_device_token"` or `"invalid_device_token", ...}`. | Fix the token. |
| `404` | `device_not_found` | No device has this serial. | Check the serial (upper-case). |
| `409` | none | A result of the same type already exists for this device and `transactionId` with a **different result code**, or the `transactionId` belongs to another device. | The first result stands. Ask an operator to correct it; do not resend. |
| `500` | `internal_error` | Server or database error. | Safe to retry. |

### Good to know

- **Retries are safe.** One result is kept per `deviceId` + type (`SALE`, `VOID`...) + `transactionId`. Resending the same result code returns `200` with `duplicate: true`. **Changed amounts or card data in a retry are ignored**; a retry is not an update.
- A `VOID` is its own result on the original sale's `transactionId`. An approved void changes the sale from `APPROVED` to `REVERSED`. A declined void leaves the sale unchanged. If the sale is not saved yet, the void log stays `PENDING` and the server retries it.
- A result without an earlier init is accepted. The payment is created from the result.
- The log, the status change and the journal line are saved in one database transaction, so they are visible when the call returns. If the payload cannot be processed (for example invalid JSON inside `requestBody`), you still get `200 accepted: true` but the log is `FAILED`. Check `POST /base/transaction/logs/search` (`parseStatus`, `parseError`).
- With a valid token, the serial comes from the token; otherwise from `deviceId`, which must be a registered device.
