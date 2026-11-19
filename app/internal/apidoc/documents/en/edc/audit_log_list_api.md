## List Audit Logs

Returns a page of audit log entries, newest first. An audit log is the history of who did what to which resource. Use it to build an activity or history screen.

### Before you start

- You need a bearer token.
- Your user needs read access to the audit log (`edc.audit.log`). Normal users (`base.group_user`) have it. If not, you get `403`.

### Request

```http
GET /base/audit-logs?limit={limit}&offset={offset}&actor={actor}&action={action}&resource_type={resource_type}&resource_id={resource_id}
Authorization: Bearer <token>
```

| Query parameter | Type | Required | What it means |
| --- | --- | --- | --- |
| `limit` | integer | No | Page size. Default `50`, max `500`. A value that is `<= 0`, above `500` or not a number becomes `50`. |
| `offset` | integer | No | How many rows to skip. Default `0`. |
| `actor` | string | No | Partial match on who did it, ignoring case. E.g. `admin` matches `admin@thebase.vn`. |
| `action` | string | No | Exact match, case-sensitive. E.g. `device.provisioned`. |
| `resource_type` | string | No | Exact match. E.g. `device`, `config`, or a model name like `edc.transaction`. |
| `resource_id` | string | No | Exact match on the resource identifier. E.g. the serial `SN0001`. |

All filters are combined with AND. Spaces around a value are ignored. A blank value is skipped.

### Example

```bash
curl "$BASE_URL/base/audit-logs?limit=20&offset=0&resource_type=device&resource_id=SN0001" \
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
        "id": 1024,
        "action": "device.provisioned",
        "resourceType": "device",
        "resourceId": "SN0001",
        "afterData": {
          "provisioningStatus": "ACTIVE",
          "secretVersion": 1,
          "mqttClientId": "SN0001"
        },
        "createdAt": "2026-09-26T08:15:02Z"
      }
    ],
    "total": 1
  }
}
```

| Field | What it means |
| --- | --- |
| `result.items` | The entries of this page, ordered by `createdAt`, newest first. |
| `result.total` | How many entries match the filters in all pages. It ignores `limit` and `offset`. Use it for paging. |
| `items[].action` / `resourceType` / `resourceId` | What happened, to which kind of thing, and to which one. |
| `items[].actor`, `beforeData`, `afterData`, `ipAddress`, `userAgent` | Omitted when empty. `beforeData` and `afterData` are free-form JSON. The keys depend on the action. |
| `items[].createdAt` | When it happened (UTC, ISO 8601). |

### Errors

Errors use the standard format described in the API guide (see "Response" → "Errors").

| HTTP | `error.code` | What it means | What you should do |
| --- | --- | --- | --- |
| `401` | `-32001` | Missing or invalid token. | Get a new token. |
| `403` | `-32003` | Your user cannot read audit logs. | Ask an admin for access. |
| `500` | `-32603` | Database error. | Retry later. Report the response `id`. |

### Good to know

- The audit log is append-only. There are no create, update or delete routes.
- To see one entry in full, call `GET /base/audit-logs/{id}`.
