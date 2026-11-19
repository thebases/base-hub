## Get Audit Log

Returns one audit log entry. An audit log is the history of who did what to which resource. Use it to show the detail of one history line.

### Before you start

- You need a bearer token.
- Your user needs read access to the audit log (`edc.audit.log`). Normal users (`base.group_user`) have it. If not, you get `403`.
- Get the numeric `id` from "List Audit Logs".

### Request

```http
GET /base/audit-logs/{id}
Authorization: Bearer <token>
```

| Path parameter | Type | Required | What it means |
| --- | --- | --- | --- |
| `id` | integer | Yes | The audit log id, e.g. `1024`. It must be a number, not a UUID. |

### Example

```bash
curl "$BASE_URL/base/audit-logs/1024" \
  -H "Authorization: Bearer $TOKEN"
```

### Response

HTTP `200`:

```json
{
  "id": "111020261535481234570001",
  "result": {
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
}
```

| Field | What it means |
| --- | --- |
| `result.id` | Audit log id. |
| `result.action` | What happened, e.g. `device.provisioned`. |
| `result.resourceType` | The kind of thing that changed, e.g. `device`. |
| `result.resourceId` | Which one changed, e.g. the serial `SN0001`. Omitted when empty. |
| `result.actor` | Who did it, e.g. an email. Omitted when not recorded. |
| `result.beforeData` / `result.afterData` | The values before and after the change. Free-form JSON. The keys depend on the action. Omitted when empty. |
| `result.ipAddress` / `result.userAgent` | Where the call came from. Omitted when not recorded. |
| `result.createdAt` | When it happened (UTC, ISO 8601). |

### Errors

Errors use the standard format described in the API guide (see "Response" → "Errors").

| HTTP | `error.code` | What it means | What you should do |
| --- | --- | --- | --- |
| `400` | `-32602` | `id` is not a number. | Send an integer id. |
| `401` | `-32001` | Missing or invalid token. | Get a new token. |
| `403` | `-32003` | Your user cannot read audit logs. | Ask an admin for access. |
| `404` | `-32004` | No audit log has this id. | Check the id. |
| `500` | `-32603` | Database error. | Retry later. Report the response `id`. |

### Good to know

- The audit log is append-only. There are no create, update or delete routes.
- Empty optional fields are left out of the JSON. Do not expect every key to exist.
