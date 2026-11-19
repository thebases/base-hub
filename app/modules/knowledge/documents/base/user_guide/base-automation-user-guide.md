# Base Automation — User Guide

| Field | Value |
|---|---|
| Version | 1.0 |
| Last updated | 2026-07-15 |
| Applies to | Base Hub — Base Automation Module (`base-automation`, v0.1.0) |
| Audience | System administrators (`base.group_system`) |

---

## Overview

**Base Automation** is the internal scheduler for Base Hub. It lets a system administrator define small automated tasks — either a script or a data operation — and have the system run them automatically on a repeating timer, without any custom code or redeployment.

It is a kernel-level module: it is always installed and cannot be disabled or uninstalled, and only users in the **System** access group can see or use it.

**Who is this for?** System administrators who need something to happen on a schedule — for example, cleaning up stale records, syncing a status field, or running a small transform script every few minutes — without asking a developer to write and deploy new code.

---

## Prerequisites

Before using Base Automation, make sure you have:

- [ ] A user account in the **System** group (`base.group_system`) — this module is not visible to other users
- [ ] A clear idea of what should run, what it should do, and how often
- [ ] If your task modifies records, the exact model name and the filter that identifies the target records

If you do not see the **Automation** menu, contact your system administrator to be added to the System group.

---

## Key Concepts

| Term | Plain-language meaning |
|---|---|
| **Automation Action** | The "what" — a single unit of work: either a Lua script or a declarative create/update/delete against a model |
| **Automation Cron** | The "when" — a schedule that runs one Automation Action every N seconds |
| **Action Type** | Whether an action runs a **Lua Script** or a **Sys Model** (declarative CRUD) operation |
| **Domain** | The filter used to find which records a Write, Unlink, or Find Or Create operation should target |
| **Values** | The field values written when creating or updating a record |
| **Dispatcher** | The internal process that checks every 30 seconds for crons that are due and runs their action |

Every automation is built from exactly two pieces: one **Action** (what to do) linked to one **Cron** (when to do it). You can also run an Action manually at any time without waiting for its schedule.

---

## Getting started

1. In the top navigation menu, click **Automation**.
2. You will see two sub-menus: **Actions** and **Crons**.
3. Always create the **Action** first — a Cron cannot exist without an Action to link to.

---

## How to create an Automation Action

An Action defines the actual work to perform. There are two types: **Lua Script** and **Sys Model**.

1. Go to **Automation → Actions**.
2. Click **New**.
3. In the **Name** field, enter a short, descriptive name (e.g. `Deactivate stale leads`).
4. In the **Action Type** field, choose:
   - **Lua Script** — for custom logic (see [How to write a Lua Script action](#how-to-write-a-lua-script-action))
   - **Sys Model** — for a straightforward create/update/delete against a model (see [How to configure a Sys Model action](#how-to-configure-a-sys-model-action))
5. Fill in the fields shown for your chosen type (they appear automatically once you pick a type).
6. *(Optional)* In the **Description** field, explain the business purpose of this action so other admins understand it later.
7. Click **Save**.

**What you will see:** The record is saved and a new tab or section appears showing when it was created and last updated (**Audit** tab).

> **Note:** You cannot save an action with an incomplete configuration. If **Action Type** is **Lua Script**, the **Lua Code** field is required. If it is **Sys Model**, the **Model** and **Operation** fields are required.

---

## How to write a Lua Script action

Use this when you need custom logic that a simple create/update/delete cannot express.

1. Set **Action Type** to **Lua Script**.
2. Open the **Lua Script** tab.
3. In the **Lua Code** field, write your script.
   - The incoming data (if any) is available as the global table `payload`.
   - To return a result, end your script with `return { ... }` — a Lua table. Anything else (or no return) produces an empty result.
4. Click **Save**.

**Example — read a field from the payload and return a result:**

```lua
local status = payload.status or "unknown"
return { checked_status = status, ok = true }
```

> **Sandbox limits:** Scripts run in a restricted sandbox for safety. Only basic scripting (variables, tables, strings, math) is available. There is **no** file access, network access, or ability to load other scripts (`require`, `dofile`, `load`, etc. are disabled). Every script is also cut off automatically after **30 seconds** if it has not finished.

---

## How to configure a Sys Model action

Use this when you want to create, update, delete, or find-or-create records in any model — no scripting required.

1. Set **Action Type** to **Sys Model**.
2. In the **Model** field, enter the exact technical model name (e.g. `crm.lead`).
3. In the **Operation** field, choose one:

   | Operation | What it does | Required fields |
   |---|---|---|
   | **Create** | Creates one new record | **Values** |
   | **Write** | Updates every record matching **Domain** | **Domain** and **Values** |
   | **Unlink** | Deletes every record matching **Domain** | **Domain** |
   | **Find Or Create** | Updates the first record matching **Domain**, or creates one from **Values** if none match | **Domain** (optional) and **Values** |

4. In the **Domain** field, enter the JSON filter that selects the target records (required for **Write** and **Unlink**).
5. In the **Values** field, enter the JSON of field values to set or create.
6. Click **Save**.

**Example — deactivate leads older than a cutoff status:**

- **Model**: `crm.lead`
- **Operation**: `write`
- **Domain**: `{"status": "stale"}`
- **Values**: `{"active": false}`

> **Safety rule:** **Write** and **Unlink** always require a non-empty **Domain**. This is intentional — it prevents an action from accidentally updating or deleting every row in a model.

---

## How to test an action manually (Run Now)

You do not have to wait for a schedule to check that an action works correctly.

1. Open the action from **Automation → Actions**.
2. Use the **Run** action on the record (available from the record's action menu).
3. The system executes the exact same code path a due cron would use, so the result is a faithful preview of scheduled behavior.

**What you will see:** The action runs immediately and returns its result (for a Lua script, whatever it returns; for a Sys Model operation, the affected record or record IDs). If it fails, the error is shown immediately instead of being logged silently.

---

## How to create an Automation Cron

A Cron links one Action to a repeating schedule.

1. Go to **Automation → Crons**.
2. Click **New**.
3. In the **Name** field, enter a descriptive name (e.g. `Nightly lead cleanup`).
4. In the **Action** field, select the Automation Action you created earlier.
5. In the **Interval Seconds** field, enter how often the action should run, in seconds.
   - **Minimum allowed: 30 seconds.** Anything lower is rejected.
6. *(Optional)* In **Max Failures**, set how many consecutive failures are allowed before the schedule automatically turns itself off. Default is **5**.
7. Make sure **Active** is checked.
8. Click **Save**.

**What you will see:** The **Next Call** field shows when the schedule will first run (defaults to now, so it typically fires within 30 seconds of saving).

---

## How the schedule runs

You do not need to start or trigger anything after saving — a background dispatcher checks for due schedules automatically.

- Every **30 seconds**, the dispatcher checks all active crons and runs any whose **Next Call** time has passed.
- After each run, **Next Call** is pushed forward by the cron's **Interval Seconds**.
- Up to 16 due crons are processed per check; if you have more than that due at once, the rest run on the next check.
- One cron's failure never affects any other cron — they are fully independent.

---

## Understanding failure handling

Each cron tracks its own run history so a broken action cannot silently keep failing forever.

| Field | What it means |
|---|---|
| **Last Run Status** | `ok` if the most recent run succeeded, `error` if it failed |
| **Fail Count** | How many **consecutive** failures have happened. Resets to 0 on the next success |
| **Max Failures** | Once **Fail Count** reaches this number, the cron is automatically set to **inactive** |

**What happens when a cron auto-deactivates:**

1. The **Active** checkbox is turned off automatically — the schedule stops running.
2. The action itself is untouched; only the schedule is paused.
3. To resume, fix the underlying problem (see [Troubleshooting](#troubleshooting)), then re-check **Active** and click **Save**.

> **Note:** Fixing the Action first is strongly recommended — simply reactivating the Cron without fixing the cause will just run into the same failures again.

---

## How to activate or deactivate a Cron

1. Open the cron from **Automation → Crons**.
2. Check or uncheck **Active**.
3. Click **Save**.

An inactive cron is skipped by the dispatcher entirely — it will not run and will not accumulate failures.

---

## Settings and configuration

| Setting | Description | Default | When to change |
|---|---|---|---|
| **Interval Seconds** | How often the cron runs, in seconds | — (required, min 30) | Set to match how frequently the task needs to run |
| **Max Failures** | Consecutive failures allowed before auto-deactivation | 5 | Lower for critical tasks you want to catch fast; raise for tasks that can tolerate occasional transient errors |
| **Active** (Action) | Whether the action can be selected/run | On | Turn off to retire an action without deleting it |
| **Active** (Cron) | Whether the schedule is currently running | On | Turn off to pause a schedule without deleting it |

---

## Troubleshooting

### "My cron never runs"

**Why it happens:** The cron may be inactive, or its **Next Call** time may be in the future.

**What to do:**
1. Go to **Automation → Crons** and confirm **Active** is checked.
2. Check the **Next Call** column — if it is in the future, wait until that time.
3. Confirm the linked **Action** exists and is filled in correctly.

---

### "The cron turned itself off"

**Why it happens:** The linked action failed enough consecutive times to reach **Max Failures**. This is automatic and by design — it prevents a broken action from failing silently forever.

**What to do:**
1. Open the linked **Action** and use **Run Now** to see the actual error.
2. Fix the problem (correct the Lua script, or correct the **Model**/**Domain**/**Values** on a Sys Model action).
3. Re-run manually to confirm it now succeeds.
4. Go back to the **Cron**, re-check **Active**, and click **Save**.

---

### "I get an error saying Domain is required"

**Why it happens:** **Write** and **Unlink** operations always require a non-empty **Domain** — this stops an action from accidentally targeting every record in a model.

**What to do:** Fill in the **Domain** field with a JSON filter that identifies exactly which records should be affected.

---

### "My Lua script fails"

**Why it happens:** Either a syntax error in the script, a runtime error (e.g. calling a function that does not exist in the sandbox), or the script ran longer than 30 seconds.

**What to do:**
1. Use **Run Now** on the action to see the exact error message.
2. Remember the sandbox only has basic scripting available — no file, network, or `require`/`dofile`/`load` access.
3. If your script needs to do heavy or long-running work, consider splitting it or reducing what it processes per run.

---

### "I can't see the Automation menu at all"

**Why it happens:** This module is restricted to the **System** access group.

**What to do:** Ask your system administrator to add your user to `base.group_system`.

---

## Frequently Asked Questions

**Q: What is the shortest interval I can set for a cron?**
A: 30 seconds. This matches the platform-wide minimum for internal schedules and prevents any single cron from overloading the dispatcher.

**Q: Can one Action be used by more than one Cron?**
A: Yes. Multiple crons can point at the same action if you want it to run on different schedules for different purposes.

**Q: Does deleting a Cron delete its Action?**
A: No. Actions and Crons are separate records. Deleting a Cron only removes the schedule; the Action remains and can be linked to a new Cron or run manually.

**Q: Is there an audit trail for what an action changed?**
A: Yes. All writes performed by an action go through the same underlying data layer as any other record change, so standard created/updated timestamps and history apply — there is no separate logging to configure.

**Q: Can I pass custom input into a manual "Run Now"?**
A: Yes, a payload can be supplied when running manually; a Lua script receives it as the `payload` table. Scheduled (cron-triggered) runs always execute with an empty payload.

**Q: My action's result isn't being returned — why?**
A: For Lua scripts, only a single `return { ... }` table is captured as the result; any other return value (string, number, no return) yields an empty result. For Sys Model **Unlink**, there is naturally no record to return — you will see the list of affected IDs instead.

---

## Reference

### Base Automation menu structure

| Menu item | What you find there |
|---|---|
| **Automation → Actions** | List and form for creating and editing Automation Actions |
| **Automation → Crons** | List and form for creating and editing Automation Crons (schedules) |

### Action Type quick reference

| Action Type | Requires | Notes |
|---|---|---|
| **Lua Script** | `lua_code` | Runs in a sandboxed VM, 30s timeout, no file/network/`require` access |
| **Sys Model** | `model`, `operation` | `write`/`unlink` also require `domain`; `create`/`write`/`find_or_create` use `values` |

### Access

| Group | Access |
|---|---|
| `base.group_system` | Full read/write/create/delete on both Actions and Crons |
| All other groups | No access — the module and menu are not visible |

---

## Getting help

- **In-app errors:** Use **Run Now** on the Action to reproduce and read the exact error.
- **Administrator:** Contact your system administrator for access issues.
- **Development team:** Contact for new action capabilities beyond Lua Script and Sys Model.
