## Bulk Import Devices

Creates or updates many devices in one call, matched by serial. Use it to load a device list (for example from a spreadsheet).

> **Warning:** Imported devices get **no** activation code. Before a terminal can provision, set one with `POST /base/device/activation` (see **Create Device** for the full flow).

### Before you start

You need a bearer token whose user may write `edc.device`; otherwise you get `403`.

### Request

```http
POST /base/devices/bulk-import
Authorization: Bearer <token>
Content-Type: application/json
```

| Name | Type | Required | What it means |
| --- | --- | --- | --- |
| `devices` | array | Yes | The list of devices. At least one item. |
| `devices[].serial` | string | Yes | Terminal serial, e.g. `SN0001` (`serial_number` also works). Spaces are trimmed. It must be unique inside the list. Send it upper-case. |
| `devices[].imei` | string | No | IMEI, e.g. `356938035643809`. Missing or blank clears it. |
| `devices[].ownerId` | int | No | Owner account ID, e.g. `10` (`owner_id` also works). Missing or not a number becomes `0`. |
| `devices[].holderId` | int | No | Holder (merchant or user) ID, e.g. `25` (`holder_id` also works). Missing or not a number becomes `0`. |
| `devices[].active` | bool | No | New devices default to `true`. Existing devices keep their current value if you omit it. |

### Example

```bash
curl -X POST "$BASE_URL/base/devices/bulk-import" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"devices":[
        {"serial":"SN0001","imei":"356938035643809","ownerId":10,"holderId":25},
        {"serial":"SN0002","imei":"356938035643810","ownerId":10,"holderId":26}
      ]}'
```

### Response

`200`:

```json
{
  "id": "111020261535481234570001",
  "result": { "created": 1, "updated": 1, "total": 2 }
}
```

| Field | What it means |
| --- | --- |
| `result.created` | New serials that were added. |
| `result.updated` | Existing serials that were changed. |
| `result.total` | Number of items you sent. |

New devices join the `default` group and get a status row. Existing devices keep their group and status.

### Errors

Errors use the standard format described in the API guide (see "Response" → "Errors"). The old string code is in `error.data.reason`.

| HTTP | `error.code` | What it means | What to do |
| --- | --- | --- | --- |
| `400` | `-32700` / `-32000` | Bad JSON, an empty `devices` list, an item without `serial`, a repeated serial, or a database error. `error.message` says which. | Fix the item and resend. |
| `401` | `-32001` | Missing or invalid token. | Get a new token. |
| `403` | `-32003` | Your user may not write `edc.device`. | Ask an admin for access. |

### Good to know

- **All or nothing.** If one item fails, nothing is saved. Fix it and resend the whole list. Resending is safe because devices are matched by serial.
- **Existing devices are overwritten.** `imei`, `ownerId` and `holderId` are replaced by what you send. If you omit them, the IMEI is cleared and the IDs become `0`. Always send full values.
- A missing `ownerId` / `holderId` is stored as `0` here, but as `1` in `POST /base/devices`.
- Other per-item keys (for example `deviceConfig`) are ignored. There is no limit on the number of items.
- Also available as: `POST /base/edc/devices/bulk-import` with `{"kwargs":{"devices":[...]}}`.
