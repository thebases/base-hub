# base-automation: user and administrator guide

## 1. Purpose and audience

`base-automation` lets administrators define **automation actions** (small pieces of work) and **automation crons** (schedules that run an action every N seconds). It is the scheduling backbone other modules reuse: for example `base-calendar` runs its cleanup job through it.

| Audience | Use |
| --- | --- |
| System administrators | Create, test, schedule and monitor actions and crons |
| Developers / integrators | Write Lua scripts, register Go handlers, call the RPC/MCP tools |
| Other modules | Hand compiled background jobs to the shared dispatcher |

## 2. Prerequisites and installation

- Depends only on `base`. The module is `auto_install` and is always installed; it cannot be disabled or uninstalled.
- The kernel scheduler must be running: one seeded scheduler job ticks the dispatcher.
- Only members of the `base.group_system` group can see and use the screens.

## 3. Key concepts

| Concept | Plain-words meaning |
| --- | --- |
| Automation Action (`automation.action`) | A reusable unit of work with a name, a type and its settings. It can be run manually or by a cron. |
| Automation Cron (`automation.cron`) | A schedule that points to exactly one action and runs it at a fixed interval. |
| Action type `lua` | A sandboxed Lua script. It receives a `payload` table and may return a table, which becomes the action result. |
| Action type `sys_model` | A declarative operation (create, write, unlink, find_or_create) on any model, without code. |
| Action type `go_handler` | A compiled handler that another module registered under a **Handler Key**. |
| Payload | Input data. The action's `Values` are the default payload; a manual run can override individual keys. |

### Action fields

| Field (UI label) | Meaning |
| --- | --- |
| Name | Human-readable name (required) |
| Action Type | Lua Script, Sys Model or Go Handler (required) |
| Description | Business purpose |
| Active | Inactive actions are not used by workflow Lua nodes |
| Lua Code | Script (Lua type) |
| Model / Operation | Target model and Create, Write, Unlink or Find Or Create (Sys Model type) |
| Domain | Filter that selects target records for write, unlink, find_or_create |
| Values | Field values for create/write, or default payload for Lua and Go Handler |
| Handler Key | Registered handler name (Go Handler type) |

### Cron fields

| Field (UI label) | Meaning |
| --- | --- |
| Name | Schedule name (required) |
| Action | The action to run (required) |
| Interval Seconds | Run cadence in seconds, minimum 30 (required) |
| Next Call | When it is next due; defaults to now on creation |
| Max Failures | Consecutive failures before auto-deactivation, default 5 |
| Fail Count | Current consecutive failures (maintained by the system) |
| Last Run Status | Ok or Error (maintained by the system) |
| Active | Whether the schedule runs |

## 4. Where to find it

| Menu | Opens |
| --- | --- |
| Automation (top-level menu, "Tự động hóa" in Vietnamese) | Parent menu |
| Automation > Actions | List and form of `automation.action` |
| Automation > Crons | List and form of `automation.cron` |

## 5. Step-by-step tasks

### Create and test a Lua action
1. Open **Automation > Actions** and click New.
2. Enter a Name and set **Action Type** to Lua Script.
3. In the **Lua Script** tab write the script. Available globals: `payload` and the `base` table (`base.call(model, method, kwargs, ids)`, `base.log(text)`, `base.json_encode`, `base.json_decode`). Return a table to use it as the result.
4. Optionally put default input in **Values** and save.
5. Run it manually through the `run` method (see section 7) to preview exactly what a cron would do.

### Create a no-code model action
1. New action, **Action Type** = Sys Model.
2. Pick **Model** and **Operation**.
3. For Write, Unlink and Find Or Create fill **Domain**; fill **Values** for the data to apply.
4. Save.

### Schedule an action
1. Open **Automation > Crons** and click New.
2. Choose a Name, the **Action** and **Interval Seconds** (30 or more).
3. Leave **Max Failures** at 5 unless you need otherwise, then save.
4. Watch **Next Call**, **Last Run Status** and **Fail Count** in the list.

### Re-enable a cron that was deactivated
1. Fix the cause (check the action by running it manually).
2. Open the cron, set **Active** back on, and reset **Fail Count** to 0, then save.

## 6. Administration and configuration

- **Access**: only `base.group_system` has read/write/create/delete on both models.
- **Seeds**: a kernel scheduler job "Automation: Dispatch Due Crons" (handler `automation.dispatch_due_crons`, every 30 seconds, 5 max failures) is installed. Do not deactivate it, or no automation cron will run.
- **Company scope**: an action takes the creator's company; a cron always inherits the company of its action and cannot be set independently.
- **Dispatch behaviour**: each tick claims up to 16 due crons, immediately moves each Next Call forward by one interval, runs the action, then records Ok or Error. A crash therefore delays a schedule by at most one interval rather than running it twice.
- **Failure policy**: each failure adds 1 to Fail Count; at Max Failures the cron is set inactive. A success resets Fail Count to 0.
- **Lua sandbox**: only base, table, string and math libraries are loaded; file and code-loading functions (`dofile`, `load`, `require`, and similar) are removed. Each run is limited to 30 seconds. `base.call` runs with the permissions of whoever started the run (the user for manual runs, system for cron runs) and raises an error on failure unless wrapped in `pcall`.

## 7. API and automation touchpoints

| Touchpoint | Purpose |
| --- | --- |
| RPC `automation.action` `run` (by id, optional `payload`) | Run an action now using the same path as a cron |
| RPC `create` / `write` on both models | Validated creation and update |
| MCP `automation.record.search_read`, `.read`, `.create`, `.write`, `.unlink` | Record CRUD limited to the two automation models; `create` requires an `idempotency_key` |
| MCP `automation.action.run` (`action_id`, optional `payload`) | Manual run |
| Go: `RegisterGoHandler(key, fn)` | Lets a module expose a handler selectable as a Go Handler action |
| Workflow Lua node | Can run a stored Lua action by id or by seed external id; inactive or non-Lua actions are rejected |

## 8. Troubleshooting and FAQ

| Symptom | Cause and fix |
| --- | --- |
| "interval_seconds must be at least 30" | Use 30 or more |
| "lua_code is required" / "model is required" / "handler_key is required" | Fill the field required by the chosen Action Type |
| "go_handler ... is not registered" | The Handler Key is misspelled or the owning module is not installed |
| "domain is required for write/unlink" | A Domain is mandatory so an action can never hit every record |
| Cron stopped running | Check Active and Fail Count; it was auto-deactivated after Max Failures |
| Cron never runs | Confirm the "Automation: Dispatch Due Crons" scheduler job is active and Next Call is in the past |
| Script fails with timeout | Scripts stop after 30 seconds; reduce the work per run |

## 9. Limits and notes

- Minimum interval is 30 seconds; scheduling is fixed-interval only (no calendar or cron expressions).
- At most 16 crons are claimed per tick; larger backlogs drain over several ticks.
- Cron runs pass an empty payload; defaults come from the action's Values.
- Actions run through the normal ORM, so changes appear in standard record audit fields (created/updated).
- Changing an action's type later does not re-check all required fields; verify with a manual run.
