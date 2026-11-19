## List Device Software Versions

Returns the different app versions and firmware versions that terminals have reported. Use it to fill the version filters on the device status screen.

### Before you start

You need a bearer token (see the API guide) of a user who may read `edc.device.status`.

### Request

```http
GET /base/device/status/versions
Authorization: Bearer <token>
```

There are no parameters.

### Example

```bash
curl "$BASE_URL/base/device/status/versions" \
  -H "Authorization: Bearer $TOKEN"
```

### Response

`200 OK`.

```json
{
  "id": "111020261535481234570001",
  "result": {
    "appVersions": ["1.4.2", "1.4.1", "1.3.9"],
    "firmwareVersions": ["V2.0.1", "V1.9.7"]
  }
}
```

| Field | What it means |
| --- | --- |
| `appVersions` | Distinct app versions, newest first. |
| `firmwareVersions` | Distinct firmware versions, newest first. |

Both lists are always present. They are `[]` when no device has reported yet.

### Errors

Errors use the standard format described in the API guide (see "Response" → "Errors").

| HTTP | `error.code` | What it means | What you should do |
| --- | --- | --- | --- |
| `401` | `-32001` | Missing or invalid token. | Get a new token. |
| `403` | `-32003` | You cannot read `edc.device.status`. | Ask an admin for access. |
| `500` | `-32603` | Database error. | Retry later and report the response `id`. |

### Good to know

- Sorting is plain text order, so `1.10.0` comes before `1.9.0`.
- Empty and null versions are skipped and each list has no duplicates.
- This call only reads data.
- Also available as `POST /base/edc/device/status/versions` and `POST /rpc/edc.device.status/versions` (JSON-RPC body, no parameters). On those, a denied read returns `{"result": null}` instead of `403`.
