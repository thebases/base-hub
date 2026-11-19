## EDC Batch Settle

The terminal calls this after the bank app closes (settles) a batch of payments. The server saves the batch totals as a **settlement** and marks that batch's transactions as settled.

> **Warning:** This is a device callback with a frozen body. The terminal firmware parses the response, so field names and shapes do not change.

### In a nutshell

- A **batch** is a group of payments made on one terminal since the last close. It has a `batchNo`, e.g. `000012`.
- **Settlement** means the bank and the terminal agree on the batch totals (how many sales, how many voids, how much money).
- After this call, approved transactions of that batch change from `APPROVED` to `SETTLED`. `REVERSED` ones stay as they are.
- Send settle only after all results for the batch have arrived. See the diagram on "EDC Transaction Init".

### Before you start

Same device authentication as "EDC Transaction Init": mode `public` (default; the server finds the device from `deviceId`) or `token` (`Authorization: Bearer <serial>:<secretVersion>:<deviceSecret>`). A header you send is always verified. `X-Device-Serial`, `X-Timestamp`, `X-Nonce`, `X-Signature` are ignored.

### Request

```http
POST /base/transaction/settle
Content-Type: application/json
```

| Body field | Type | Required | What it means |
| --- | --- | --- | --- |
| `batchNo` | string (max 32) | Yes | Batch number, e.g. `000012`. |
| `deviceId` | string (max 128) | Yes | Terminal serial, e.g. `SN0001`. Old key `device_id`. |
| `requestBody` | string | Yes | Bank-app settle request **as a JSON string**; must be valid JSON. The server reads `transactionID` (or `merchant_transID`) and `tranxType`. Old key `request_body`. |
| `response` | string | Yes | Bank-app settle response **as a JSON string**; must be valid JSON. The server reads `batchNo`, `currency`, `isoResponseCode`, `merchantId`, `terminalId`, `acqr_name`, `merchantName`, `quantity`, `totalAmount`, `saleQuantity`, `saleTotalAmount`, `voidQuantity`, `voidTotalAmount`. An optional `Issuer` array can carry per-issuer `totalQuantity`, `totalAmount`, `saleQuantity`, `saleTotalAmount`, `voidQuantity`, `voidTotalAmount` (used when the top-level value is missing). |
| `transactionTime` | string (max 64) | Yes | Settle time: RFC 3339, `2006-01-02 15:04:05+07:00`, or `20060102150405` (UTC). If it cannot be read, the receive time is used. Old key `transaction_time`. |

Counts and amounts are **integer strings**. Amounts are in whole currency units (`"3750000"` = 3,750,000 VND). Values with decimals or separators become `0`.

### Example

```bash
curl -X POST "$BASE_URL/base/transaction/settle" \
  -H "Content-Type: application/json" \
  -d '{
    "batchNo": "000012",
    "deviceId": "SN0001",
    "transactionTime": "2026-09-26T22:00:00+07:00",
    "requestBody": "{\"transactionID\":\"STL20260926000012\",\"tranxType\":\"SETTLE\"}",
    "response": "{\"batchNo\":\"000012\",\"currency\":\"VND\",\"isoResponseCode\":\"00\",\"merchantId\":\"000000012345678\",\"terminalId\":\"70010001\",\"acqr_name\":\"BANKAPP\",\"quantity\":\"25\",\"totalAmount\":\"3750000\",\"saleQuantity\":\"26\",\"saleTotalAmount\":\"3900000\",\"voidQuantity\":\"1\",\"voidTotalAmount\":\"150000\",\"transDateTime\":\"2026-09-26T22:00:00+07:00\"}"
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
    "batchNo": "000012",
    "transactionId": "STL20260926000012",
    "receivedAt": "2026-09-26T22:00:01.207+07:00",
    "logId": "5602"
  }
}
```

| Field | What it means |
| --- | --- |
| `accepted` | `true` when the server saved the call. |
| `duplicate` | `true` when this settle was already recorded (a retry). |
| `batchNo` | The batch number. |
| `transactionId` | `requestBody.transactionID` (or `merchant_transID`); if missing, the `batchNo`. |
| `receivedAt` / `logId` | When the server received it, and the raw callback log ID. |

### Errors

Callback errors keep the old device format: `{"code": "...", "message": "..."}`. Validation errors look like `{"errors":[{"field":"BatchNo","message":"..."}]}`.

| HTTP | `code` | What it means | What you should do |
| --- | --- | --- | --- |
| `400` | `invalid_json` | Body is not valid JSON. | Fix the JSON. |
| `400` | none (`errors` list) | A required field is missing, empty or too long. | Send all required fields. |
| `401` | none | Token missing or wrong (`token` mode, or a bearer header was sent). | Fix the token. |
| `404` | `device_not_found` | No device has this serial. | Check the serial (upper-case). |
| `500` | `internal_error` | `requestBody` or `response` is not valid JSON, or a server/database error. | Invalid JSON is not retryable: fix the payload. |

### Good to know

- **Retries are safe but final.** A settle is identified by `deviceId` + `batchNo` + settle `transactionId`. Resending returns `200` with `duplicate: true`, and **new totals in the retry are ignored**. To correct totals, use a new settle `transactionId` or ask an operator (`PUT /base/settlement`).
- The settlement gets `settleId = "<deviceId>:<batchNo>:<transactionId>"`, e.g. `SN0001:000012:STL20260926000012`. Find it with `POST /base/settlement/search`.
- Every transaction line of **this terminal** with the same `batchNo` (from `response.batchNo`, else the body `batchNo`) gets that `settleId`, `isSettled: true` and `settledAt`. Lines already settled under another `settleId` are left alone.
- If `response.isoResponseCode` is present and not `00`, the settlement is saved (with `de39AuthCode` = that code) but no transactions are marked settled. The log is `IGNORED`.
- The settlement, the line updates and a check of the terminal totals against the settled lines (`MATCHED` or `MISMATCH`) are saved in the same database transaction. The check result is stored on the settlement and readable through `/rpc`.
