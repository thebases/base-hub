# Workflow Module — User Guide

| Field | Value |
|---|---|
| Version | 1.0 |
| Last updated | 2026-05-17 |
| Applies to | Base Hub — Workflow Module |
| Audience | Business users, operations staff, non-technical administrators |

---

## Overview

The **Workflow** module lets you build automated processes that run whenever your system receives data from an external source. Think of a workflow like a recipe: when an ingredient arrives (data comes in), the system follows the recipe steps automatically — saving data, transforming it, sending notifications, calling other services — without any manual work.

You design these recipes through a visual canvas, configure each step, and the system handles the rest.

**Who is this for?** Anyone who needs to automate what happens when a payment notification, bank data, device message, or any other data arrives — without writing code.

---

## Prerequisites

Before using the Workflow module, make sure you have:

- [ ] A user account with access to the **Workflow** menu
- [ ] Permission to create and edit workflows (ask your administrator if you do not see the Workflow menu)
- [ ] A clear idea of what data will arrive and what you want to do with it

If you do not have the required access, contact your system administrator.

---

## Key Concepts

Understanding these five terms will make everything else click:

| Term | Plain-language meaning |
|---|---|
| **Workflow** | The complete automated process — a named recipe with a unique address where data arrives and a list of steps to run |
| **Topic** | The unique name for the workflow's "inbox". It becomes the URL where external systems send data: `/wh/{topic}` |
| **Action Step** | One task inside the workflow (e.g., "save this data", "send a notification", "transform the data") |
| **Trigger** | The event that starts the workflow — most commonly an HTTP webhook call to `/wh/{topic}` |
| **Workflow Run** | A record of one execution: when it started, which steps ran, whether it succeeded or failed |

---

## Getting Started — Create Your First Workflow

This section walks you through creating a simple workflow from scratch.

### Step 1 — Open the Workflow list

1. In the top navigation menu, click **Workflow**.
2. Select **Workflow** from the dropdown.

You will see the list of all existing workflows.

### Step 2 — Create a new workflow

1. Click the **New** button (usually in the top-left area of the list).
2. The workflow form opens.

### Step 3 — Fill in the basic information

Fill in these fields on the form:

1. In the **Name** field, enter a short unique name for your workflow.
   - Example: `txn_forward_v1`
   - Use only letters, numbers, and underscores. No spaces.
2. In the **Topic** field, enter the keyword that will form your webhook address.
   - Example: `payment` → your webhook URL will be `/wh/payment`
   - Use only lowercase letters and numbers.
3. In the **Version** field, enter `1` (this is your first version).
4. Make sure **Is Active** is checked (turned on).
5. In the **Description** field, describe what this workflow does in plain language.
   - Example: `Receives payment notifications and saves them to the database, then notifies the finance team.`

### Step 4 — Save the workflow

1. Click **Save** (or the save icon) to save your new workflow.

You have created an empty workflow. Next, you will design what it actually does by adding action steps.

---

## Step by Step — Make a Workflow from Scratch

There are three ways a workflow can be started. You choose the trigger type on the canvas when building the workflow.

| Trigger type | How it starts | Best for |
|---|---|---|
| **HTTP Webhook** | An external system calls a URL | Payment callbacks, bank APIs, any HTTP integration |
| **Kafka Topic** | A message is published to a Kafka topic | High-volume event streams, internal system events |
| **MQTT Topic** | A message arrives on an MQTT broker | IoT devices, real-time sensor data, device commands |

The three walkthroughs below share the same first three steps (create the workflow record). They differ only in how you configure the trigger node on the canvas.

---

### Walkthrough A — HTTP Webhook trigger

Use this when an external system (a bank, a payment gateway, a partner API) sends data by calling a URL.

**What you will end up with:** A URL at `/wh/{topic}` that the external system can POST data to, and a workflow that processes that data automatically.

#### Step 1 — Create the workflow record

1. Navigate to **Workflow → Workflow** and click **New**.
2. Fill in:
   - **Name** — e.g. `payment_receive_v1`
   - **Topic** — e.g. `payment` (this becomes the URL: `/wh/payment`)
   - **Version** — `1`
   - **Is Active** — checked (on)
   - **Description** — e.g. `Receives payment notifications from the gateway and saves them to the database.`
3. Click **Save**.

#### Step 2 — Open the Canvas

1. Click the **Canvas** tab on the workflow form.
2. You will see a single **Trigger** node on the left labelled **HTTP Webhook**.

#### Step 3 — Configure the trigger node

1. Click the **Trigger** node to open the configuration panel on the right.
2. The **Trigger Type** is already set to **HTTP Webhook**. Leave it as is.
3. The **Webhook Path** field shows `/wh/<topic>` — this is read-only and is filled automatically from the workflow's Topic field. No changes needed here.
4. *(Optional)* If you want only certain users to be able to call this webhook, select a group in the **Access Group** field.
5. *(Optional)* Paste a sample JSON payload in the **Sample Payload** box — this lets you reference field names as `{{field}}` placeholders when you configure action steps later.
6. Click **Save Trigger**.

#### Step 4 — Add action steps

Add the steps the workflow should perform. A typical HTTP workflow looks like this:

```
[HTTP Trigger] → [Parse Payload] → [your steps] → [Save To DB or Post Output Object]
```

For each step:

1. Click the **+** (add node) button on the canvas or drag an action node from the node palette.
2. In the panel that opens, select the **Action Type** and fill in its fields.
3. Click **Save Node**.

> Always add **Parse Payload** as your first action step. It converts the raw HTTP request body into a usable JSON object for all following steps.

#### Step 5 — Save the canvas

1. Click the **Save** button (floppy disk icon) in the canvas toolbar.
2. The workflow is now live.

#### Step 6 — Test it

Send a test request to your workflow URL:

```
POST /wh/payment
Content-Type: application/json

{
  "txn_id": "TXN001",
  "amount": 500000
}
```

Then check **Workflow → Workflow Run** to confirm a run appeared and its status is **succeeded**.

---

### Walkthrough B — Kafka Topic trigger

Use this when your system publishes events to a Kafka topic and you want the workflow to react to each message automatically.

**What you will end up with:** A Kafka consumer subscribed to a topic. Every message published to that topic fires the workflow.

#### B1 — Create the workflow record

1. Navigate to **Workflow → Workflow** and click **New**.
2. Fill in:
   - **Name** — e.g. `txn_event_processor_v1`
   - **Topic** — e.g. `txn_events` (this is just the workflow's internal name, not the Kafka topic — you will set the Kafka topic in Step B3)
   - **Version** — `1`
   - **Is Active** — checked
   - **Description** — e.g. `Processes transaction events from the txn.settled Kafka topic.`
3. Click **Save**.

#### B2 — Open the Canvas

1. Click the **Canvas** tab.
2. You will see the **Trigger** node. By default it shows **HTTP Webhook**.

#### B3 — Configure the trigger node as Kafka

1. Click the **Trigger** node to open the configuration panel.
2. In the **Trigger Type** dropdown, select **Kafka Topic**.
3. Fill in the fields:

   | Field | What to enter | Required? |
   | --- | --- | --- |
   | **Kafka Topic** | The exact Kafka topic name your system publishes to (e.g. `txn.settled`) | Yes |
   | **Consumer Group** | Leave as `ipn-consumer-group` unless your administrator tells you otherwise | No |
   | **Use Dedupe** | Leave checked — this prevents the same message from being processed twice if delivered more than once | No |

4. *(Optional)* Paste a sample Kafka message payload in the **Sample Payload** box.
5. Click **Save Trigger**.

> **What happens when you save:** The system registers a Kafka consumer in the background. It will start listening to the topic immediately. You do not need to restart anything.

#### B4 — Add action steps

Add your steps the same way as in Walkthrough A. A typical Kafka workflow:

```
[Kafka Trigger] → [Parse Payload] → [your steps] → [Save To DB or Notify]
```

> Always start with **Parse Payload** — Kafka messages arrive as raw bytes and must be parsed into JSON first.

#### B5 — Save the canvas

1. Click **Save** in the canvas toolbar.
2. The Kafka subscription is now active.

#### B6 — Verify the subscription is running

1. Publish a test message to your Kafka topic using your existing tooling.
2. Check **Workflow → Workflow Run** — a new run should appear within seconds.
3. If no run appears after publishing a message, check that the Kafka topic name matches exactly and that **Is Active** is on.

---

### Walkthrough C — MQTT Topic trigger

Use this when IoT devices or a messaging broker publish events via MQTT (for example, device heartbeats, sensor readings, or status updates).

**What you will end up with:** An MQTT subscription on a topic pattern. Every matching message fires the workflow.

#### C1 — Create the workflow record

1. Navigate to **Workflow → Workflow** and click **New**.
2. Fill in:
   - **Name** — e.g. `device_heartbeat_v1`
   - **Topic** — e.g. `device_hb` (internal workflow name, not the MQTT topic)
   - **Version** — `1`
   - **Is Active** — checked
   - **Description** — e.g. `Processes heartbeat messages from all devices on the MQTT broker.`
3. Click **Save**.

#### C2 — Open the Canvas

1. Click the **Canvas** tab.
2. You will see the **Trigger** node showing **HTTP Webhook** by default.

#### C3 — Configure the trigger node as MQTT

1. Click the **Trigger** node to open the configuration panel.
2. In the **Trigger Type** dropdown, select **MQTT Topic**.
3. Fill in the fields:

   | Field | What to enter | Required? |
   | --- | --- | --- |
   | **MQTT Topic** | The MQTT topic or wildcard pattern to subscribe to (e.g. `devices/+/heartbeat` or `sensors/#`) | Yes |
   | **QoS** | Leave as `1` for most cases. Set to `0` for maximum speed (may lose messages), `2` for guaranteed exactly-once delivery | No |

   **MQTT wildcard cheat sheet:**

   - `+` matches exactly one level: `devices/+/heartbeat` matches `devices/abc/heartbeat` but not `devices/abc/xyz/heartbeat`
   - `#` matches everything below: `devices/#` matches `devices/abc`, `devices/abc/heartbeat`, and any deeper path

4. *(Optional)* Paste a sample MQTT message payload in the **Sample Payload** box.
5. Click **Save Trigger**.

> **What happens when you save:** The system subscribes to the MQTT broker immediately. You do not need to restart anything.

#### C4 — Add action steps

A typical MQTT workflow:

```
[MQTT Trigger] → [Parse Payload] → [your steps] → [Save To DB or Notify]
```

> Always start with **Parse Payload** — MQTT messages arrive as raw bytes.

#### C5 — Save the canvas

1. Click **Save** in the canvas toolbar.
2. The MQTT subscription is now active.

#### C6 — Verify the subscription is running

1. Publish a test message to your MQTT topic using your broker's tooling or a MQTT client.
2. Check **Workflow → Workflow Run** — a new run should appear within seconds of the message arriving.
3. If no run appears, confirm the topic pattern is correct (wildcards are case-sensitive and path-separator-sensitive) and that **Is Active** is on.

---

## How to Design Your Workflow Using the Canvas

The **Canvas** is a visual drag-and-drop designer where you can see your workflow as connected boxes (nodes). This is the easiest way to build and understand a workflow.

### Opening the Canvas

1. Open a workflow from the list (click its name).
2. In the workflow form, click the **Canvas** tab.

You will see a visual area with a **Trigger** node already placed on the left. This represents where data arrives.

### Understanding the Canvas

- **Trigger node** (left side) — where data enters your workflow. It shows the webhook URL (e.g., `/wh/payment`).
- **Action nodes** (connected boxes to the right) — each box is one step the workflow will perform.
- **Arrows** — show the order in which steps run (left to right).

### Adding an Action Step

1. On the canvas, click the **+** button or the **Add Step** option (visible when you hover near an existing node or the empty canvas area).
2. A panel opens on the right side.
3. In the panel:
   - **Step Name**: Give this step a clear name (e.g., `Parse incoming data`)
   - **Action Type**: Select what this step should do from the dropdown (see the Action Types section below)
   - Fill in any additional fields the selected action requires
4. Click **Save** in the panel.

The new step appears on the canvas as a box connected to the previous node.

### Connecting Steps in Order

Steps run in the order they are connected by arrows. To change the order:

1. Drag a node to a new position on the canvas.
2. The arrows update automatically to reflect the visual layout.

### Saving the Canvas Layout

1. After adding or moving nodes, click the **Save** button (floppy disk icon) inside the canvas area.
2. The system saves the layout and rebuilds the step sequence.

> **Important:** Always save from inside the canvas (the save button on the canvas toolbar) — not just the form's save button. The canvas save updates the action sequence.

---

## How to Add and Configure Action Steps

Each step in your workflow does one specific thing. You choose the step type from a catalog of available actions.

### How to add a step (outside the canvas)

1. Open a workflow form.
2. Click the **Action Sequence** tab.
3. Click **Add a line** at the bottom of the action list.
4. Fill in the step details (see the table below for fields).
5. Click **Save**.

### Action step fields

| Field | What it means | Required? |
|---|---|---|
| **Name** | A label for this step — shown in the canvas and run history | Recommended |
| **Step Key** | A short unique code for this step within the workflow (e.g., `parse_data`) | Recommended |
| **Action Type** | What this step does — selected from the catalog | Yes |
| **Sequence** | The execution order number (10 = first, 20 = second, etc.) | Yes |
| **Timeout (seconds)** | How long the step is allowed to run before it is considered stuck | Recommended (15–30s) |
| **Retry** | How many times to retry if the step fails | Optional |
| **Continue On Error** | If turned on, the workflow continues even if this step fails | Optional |

---

## Action Types — What Each Step Can Do

The system has a library of pre-built action types. Select the one that matches what you need the step to do.

| Action Type | What it does | When to use it |
|---|---|---|
| **Parse Payload** | Reads the incoming raw data and converts it into a structured format for the next steps | Always use this as your **first step** |
| **Flatten JSON** | Simplifies nested data into a flat structure so it is easier to reference fields | When your incoming data has deeply nested fields |
| **Transform Data** | Rearranges or renames fields using a template you define | When you need to reshape data before sending it somewhere |
| **Save To DB** | Saves the current data into the system database | When you want to store the incoming data permanently |
| **CRUD Model** | Creates, finds, or updates a record in any system model | When you need to find or create a related record (e.g., a partner, account) |
| **Notify** | Sends a notification to a channel or webhook endpoint | When you want to alert a team or system about the event |
| **Post Output Object** | Sends the data as an HTTP request to an external URL (webhook) | When you need to forward data to another system |
| **HMAC Action** | Adds a security signature to the data | When the receiving system requires a signed payload |
| **AES Action** | Encrypts the data | When you need to send sensitive data securely |
| **AES Decrypt Action** | Decrypts encrypted incoming data | When the incoming data arrives in encrypted form |

### Recommended first workflow structure

For most use cases, start with this order:

```
[Trigger] → [Parse Payload] → [your steps] → [Post Output Object or Notify]
```

---

## How to Trigger Your Workflow (Send Data to It)

Once your workflow is active, any external system can send data to it using a simple HTTP request.

### Your workflow's webhook URL

```
POST /wh/{topic}
```

Replace `{topic}` with the **Topic** value you set when creating the workflow.

**Example:** If your topic is `payment`, the URL is:
```
POST /wh/payment
```

**What to send:** Send any JSON data in the request body. The workflow will receive it, parse it, and run all your steps on it.

> **Note:** The webhook accepts `GET`, `POST`, `PUT`, and `PATCH` requests. For sending data, always use `POST`.

### Testing your workflow manually

You can test by sending a request using any HTTP tool (such as Postman, Insomnia, or `curl`):

```
POST /wh/payment
Content-Type: application/json

{
  "txn_id": "TXN001",
  "amount": 500000,
  "currency": "VND"
}
```

After sending, check the **Workflow Run** view to see the result.

---

## How to Monitor Workflow Runs

Every time your workflow runs, the system records the full execution log. You can see what happened, which steps succeeded or failed, and the exact data at each point.

### Opening the Workflow Run list

1. In the **Workflow** menu, click **Workflow Run**.
2. You see a list of all executions, newest first.

### Understanding the run list columns

| Column | What it means |
|---|---|
| **Request ID** | A unique identifier for this execution |
| **Status** | Whether the run succeeded, failed, or is still in progress (see status table below) |
| **Topic** | Which workflow was triggered |
| **Flow Version** | Which version of the workflow ran |
| **Current Step** | The last step that ran (useful when diagnosing failures) |
| **Created At** | When the run started |
| **Attempt Count** | How many times the system tried (including retries) |

### Run status meanings

| Status | What it means |
|---|---|
| **pending** | The run has been received and is waiting to start |
| **running** | The workflow is currently executing steps |
| **succeeded** | All steps completed successfully |
| **waiting_retry** | A step failed and the system is waiting before retrying |
| **failed** | The workflow could not complete after all retry attempts |
| **dead_letter** | The run failed permanently and has been moved aside for manual review |
| **cancelled** | The run was stopped before it finished |
| **skipped** | The run was skipped (e.g., a duplicate was detected) |

### Viewing the details of a single run

1. Click on any run in the list to open its detail form.
2. You will see:
   - **Execution** section: status, topic, version, current step
   - **Recovery** section: attempt count, scheduled retry time, last error message
   - **Payload** tab: the exact data the workflow received and the final output
   - **Step History** tab: a record of every step that ran, including timing and status for each

### Filtering runs by status

Use the filter buttons at the top of the Workflow Run list:
- Click **Succeeded** to see only successful runs
- Click **Failed** to see only failed runs
- Click **Waiting Retry** to see runs that will be retried soon

---

## How to View the Flow Map (Diagram)

The **Flow Map** is an automatically generated diagram showing your workflow's step sequence. It is useful for documentation and for quickly understanding what a workflow does.

### Viewing the flow map

1. Open a workflow from the **Workflow** list.
2. Click the **Flow Map** tab.
3. You will see a diagram showing the trigger and all connected steps.

### Regenerating the flow map

If you made changes to the workflow's steps, the flow map updates automatically when you save. You can also regenerate it manually:

1. On the **Flow Map** tab, click the **Generate Flow Map** button.

---

## How to Activate or Deactivate a Workflow

You can turn a workflow on or off without deleting it.

### To deactivate (pause) a workflow

1. Open the workflow from the list.
2. Uncheck the **Is Active** checkbox.
3. Click **Save**.

The workflow will no longer respond to incoming data while inactive.

### To reactivate a workflow

1. Open the workflow from the list.
2. Check the **Is Active** checkbox.
3. Click **Save**.

---

## How to Version a Workflow

When you need to make significant changes to an existing workflow (and want to keep the old version for reference), create a new version instead of editing the existing one.

### Steps

1. Open the existing workflow.
2. Change the **Version** field from `1` to `2` (or the next number).
3. Alternatively, create a new workflow with a new name (e.g., `txn_forward_v2`) and the same topic.
4. Make your changes.
5. Set the old workflow to **inactive** and the new one to **active**.

> **Important:** Only one active workflow per topic should exist at a time. If two active workflows share the same topic, the system runs the most recently updated one.

---

## Settings and Configuration

| Setting | Description | Default | When to change |
|---|---|---|---|
| **Is Active** | Turns the workflow on or off | On (checked) | When you want to pause without deleting |
| **Version** | The version number of this workflow definition | 1 | When you create a new iteration of the same workflow |
| **Group ID** | Restricts which user group can trigger this workflow | None (anyone can trigger) | When only certain users or systems should be allowed |
| **Timeout (seconds)** | Per-step time limit before it is considered stuck | 15–30s | When a step calls a slow external service |
| **Retry** | Number of retry attempts if a step fails | 1 | Increase for steps that call unreliable external systems |
| **Continue On Error** | Allow the workflow to continue even if this step fails | Off | Turn on for non-critical steps (e.g., logging) |

---

## Troubleshooting

### "My workflow is not running when I send data"

**Why it happens:** The workflow may be inactive, or the topic in your request does not match the topic configured on the workflow.

**What to do:**
1. Go to **Workflow → Workflow** and check that **Is Active** is checked.
2. Confirm the topic in the workflow form matches the URL you are calling (`/wh/{topic}`).
3. Try sending the request again.
4. Check **Workflow → Workflow Run** to see if a run was created.

---

### "The workflow run shows status: failed"

**Why it happens:** One of the steps encountered an error it could not recover from.

**What to do:**
1. Go to **Workflow → Workflow Run** and open the failed run.
2. Read the **Last Error** field — it will describe what went wrong.
3. Click the **Step History** tab to see which step failed.
4. Fix the configuration of that step (e.g., correct an external URL, fix a template).
5. You cannot re-run a failed run directly — re-send the original data to the webhook to start a new run.

---

### "The workflow run shows status: dead_letter"

**Why it happens:** The run failed and exhausted all retry attempts. It has been set aside so it does not keep retrying indefinitely.

**What to do:**
1. Open the run and read the **Last Error** to understand the root cause.
2. Fix the underlying issue (e.g., fix the external URL, correct credentials).
3. Re-send the original data to the webhook to trigger a fresh run.
4. Contact your administrator if the error message is unclear.

---

### "A step is not visible in the canvas"

**Why it happens:** The step may have **Is Active** set to off, which hides it from the canvas and prevents it from running.

**What to do:**
1. Go to the **Action Sequence** tab on the workflow form.
2. Find the step in the list and check its **Is Active** setting.
3. Turn it on, then save.

---

### "The action I need is not in the catalog"

**What to do:** Contact your system administrator or development team. New action types must be added by a developer.

---

## Frequently Asked Questions

**Q: Can I have two workflows with the same topic?**
A: Yes, but only one should be active at a time. If two active workflows share a topic, the behavior is unpredictable. Always deactivate the old version before activating a new one.

**Q: How do I know which URL to give to an external system?**
A: Your webhook URL is `https://your-domain.com/wh/{topic}`, where `{topic}` is the Topic value on your workflow. If you are unsure of your domain, ask your administrator.

**Q: How long does a workflow run keep its records?**
A: Records are kept indefinitely by default. Ask your administrator if your organization has a data retention policy that applies.

**Q: Can a workflow call another workflow?**
A: Not directly through the UI. Each workflow is independent. If you need chained workflows, use a **Post Output Object** step to send data to the other workflow's webhook URL.

**Q: What happens if the same data arrives twice?**
A: The system creates a new run for each incoming request. If you want to prevent duplicate processing, ask your development team to enable deduplication on the trigger.

**Q: Can I see the exact data that arrived?**
A: Yes. Open any workflow run, then click the **Payload** tab to see the exact input data received and the final output produced.

**Q: What is the difference between Timeout and Retry?**
A: **Timeout** is how long a single attempt is allowed to take before it is abandoned. **Retry** is how many times the system will try again after a failure. For example: timeout = 30s, retry = 3 means the system will try up to 3 times, each attempt cut off at 30 seconds.

**Q: How do I delete a workflow?**
A: Deactivate it first (uncheck Is Active), then contact your administrator to permanently remove it. Deleting a workflow also removes all its steps and history.

---

## Reference

### Workflow Module Menu Structure

| Menu item | What you find there |
|---|---|
| **Workflow → Workflow** | List and form for creating and editing workflows |
| **Workflow → Workflow Canvas** | Opens the canvas view directly (visual editor) |
| **Workflow → Workflow Action** | List of all individual action steps across all workflows |
| **Workflow → Workflow Run** | Execution history and run status monitoring |
| **Workflow → Outbound Delivery** | Queue of outbound webhook deliveries sent by workflow steps |

### Webhook URL format

```
POST https://{your-domain}/wh/{topic}
Content-Type: application/json

{ ...your data... }
```

### Run status quick reference

| Status | Meaning | Action needed? |
|---|---|---|
| pending | Queued, not started yet | None |
| running | In progress | None |
| succeeded | Finished successfully | None |
| waiting_retry | Scheduled to retry | None (wait) |
| failed | All retries exhausted | Investigate and re-send |
| dead_letter | Permanently failed | Investigate and re-send |
| cancelled | Stopped manually | None |
| skipped | Skipped (duplicate detected) | None |

---

## Getting Help

- **In-app errors:** Check the **Last Error** field on the failed Workflow Run record.
- **Administrator:** Contact your system administrator for permission issues or missing action types.
- **Development team:** Contact for new action types, complex automation requirements, or system-level errors.
