# base-workflow: User and Administrator Guide

## 1. Purpose and who uses it

`base-workflow` lets you design, run and monitor automated workflows inside Base HUB. A workflow is an ordered (or graph-shaped) list of steps that is started by a trigger, for example an incoming webhook, a Kafka or MQTT message, or a change to a record. Each step does one job: parse the incoming payload, create or look up a record, transform data, sign or encrypt it, call an external endpoint, branch on a condition, or run a Lua script.

| Audience | What they do |
| --- | --- |
| Integration designers / administrators | Build workflows, triggers, steps and credentials; test and export them |
| Operators / support | Watch runs, read step history, retry failed outbound deliveries |
| Developers | Call the webhook route, the RPC/MCP tools, or write Lua actions |

Every internal user can read this module's records; only `base.group_system` can create, change or delete workflow definitions (see section 6).

## 2. Prerequisites and install

| Item | Detail |
| --- | --- |
| Module name | `base-workflow` (category System, not auto-installed) |
| Depends on | `base`, `base-automation`, `base-credential`, `base-bus` |
| Install | Install `base-workflow` from the Apps / module screen; dependencies are installed with it |
| Optional | Kafka or MQTT broker for Kafka/MQTT triggers; the Lua step needs a Lua action saved in Automation |

## 3. Key concepts

| Concept | Model | Plain-words meaning |
| --- | --- | --- |
| Workflow | `base.workflow` | The definition: name, unique `topic`, version, active flag, execution mode, optional access group |
| Action step | `base.workflow.action` | One step of a workflow, based on a catalog entry, with its own parameters, timeout and retry policy |
| Action catalog | `base.workflow.action.catalog` | The fixed list of built-in step types (read-only in the UI) |
| Trigger | `base.workflow.trigger` | What starts a workflow: Webhook, Kafka, MQTT, Model Event or TCP |
| Edge | `base.workflow.edge` | A link between two steps (used only in DAG mode) |
| Run | `base.workflow.run` | One execution with its input/output payloads, step history and retry schedule |
| Outbound delivery | `base.workflow.outbound` | A queued HTTP/notification send created by notify or post_output_object steps, with attempts and retries |
| Credential | `base.workflow.credential` | A named secret (HMAC key, AES key, API key, password, token) stored encrypted |

Built-in step types (catalog):

| Code | Use |
| --- | --- |
| `parse_payload` | Parse the incoming bytes into a JSON object |
| `flatten_json` | Flatten nested JSON into leaf keys to ease `{{field}}` placeholders |
| `transform_data` | Build a new JSON document from an inline template |
| `crud_model` | Run a model operation (default `find_or_create`) and merge the result into the payload. `find_or_create` searches, then creates: two runs at once can both create. Use it only on a model with a unique constraint on the search fields (for example `name` or a reference column marked `unique`) so the second create fails instead of duplicating, and give the step a retry policy |
| `notify` | Queue an outbound JSON delivery; the step succeeds once the delivery is stored, the send and its retries happen in the background (section 5.7) |
| `post_output_object` | Post the current payload and wait for the answer; any status other than 2xx fails the step (the error shows the status and the start of the body) |
| `hmac_action` | Compute an HMAC signature and add it to the output (default field `signature`) |
| `hmac_verify` | Check an HMAC signature that arrives inside the payload: HMAC Key (a `@cred:`/`@syscred:` reference), Algorithm `sha256` or `sha512`, Signature Field and Message Field (dotted payload paths; a text message is signed as is, any other value as compact JSON with sorted keys), Encoding `hex` or `base64`. The comparison is constant-time. A wrong or missing signature fails the step at once, without retries; on success the payload passes on with `_hmac_verified: true`. A body signed in a request header is checked by the webhook trigger's `hmac_secret` instead (steps never see headers) |
| `aes_action` / `aes_decrypt_action` | Encrypt into / decrypt from a wrapped field (default field `data`) |
| `condition` | Branch into Yes / No paths from a boolean expression. Leaf operators: `exists`, `empty`, `equals`, `not_equals`, `in`, `not_in`, `gt`/`gte`/`lt`/`lte` (numbers only: a text field such as `"7"` is an error), `contains` (substring of a text field or element of an array), `regex` (Go RE2 syntax, at most 512 characters); combine leaves with `all`, `any`, `not`. An unknown key is an error, never ignored |
| `lua_script` | Run a Lua action saved in Automation > Actions |
| `call_workflow` | Run another workflow (Target Workflow, a published one) with the current payload as a child run and continue with its final payload. Wait is `sync` and Payload Mapping `pass_through` (the only modes for now). The child runs the target's CURRENT published version (it is not pinned to the caller's version) in the same database under the same identity, and its run shows the caller in Parent Run. A signed-in caller (webhook, Run Now, replay) who is not in the target's Group ID is refused, without retries. At most 5 nested calls; a workflow that is already in the call chain is refused (no recursion); both refusals fail the step without retries. The child retries its own steps in place and ends Failed (never Dead Letter) when it gives up, which fails the calling step; the calling step's Retry Policy then starts a new child. A retry re-runs EVERY child step, with the same idempotency keys as before (`<calling step key>:<call or itemN>:<child step key>`), so systems that deduplicate on the key see the same request again. Cancelling the caller cancels the child; the caller's Max Run Seconds and the step's Timeout Seconds also stop it, so give the step a timeout long enough for the child. The target's Max Concurrent Runs applies: when it is full the step fails and is retried. A dry run only records the call |
| `for_each` | Run a published sub-workflow (Target Workflow) once per element of the array at Items Path and write the child outputs, in input order, to Output Field (default `results`), plus `_for_each: {total, failed}`. An object element is the child's payload as is; any other element is sent as `{"item": value}`. Max Concurrency 1 to 8 (default 1) children run at once; an array longer than Max Items (default 100, at most 1000) fails the step without retries. Fail Fast on (default): the first failed item stops the others and fails the step; off: every item runs and a failed one appears as `{"_error": {index, message}}`. Each item is a child run, with the same rules as `call_workflow`; a retry of the step runs every item again. Cancel, Max Run Seconds and the step timeout stop the remaining items |
| `wait_approval` | Pause the run until someone approves or rejects it (section 5.11). Expires In Seconds (0 = never, at most 30 days) fails the run when nobody decides. Approver Group (optional) lets that group's operators decide without the token. Linear workflows only |

Execution modes: `linear` (default, steps run in Sequence order) and `dag` (steps run as a graph through edges, several in parallel up to Max Parallel Steps, default 4). The DAG failure policy is either Fail Branch Only (default) or Fail Whole Workflow.

## 4. Where to find it

| Menu (under the Workflow menu) | Opens | Notes |
| --- | --- | --- |
| Workflow | `/web/base.workflow` | List, kanban and canvas views |
| Workflow Canvas | `/web/base.workflow/canvas` | Visual editor for step layout and wiring |
| Workflow Action | list of `base.workflow.action` | All configured steps |
| Workflow Run | list of `base.workflow.run` | Run monitoring; filters by status |
| Outbound Delivery | `/web/base.workflow.outbound` | Outbound sends and retries |
| MQTT Monitor | `/web/workflow/mqtt-monitor` | Live MQTT client and per-topic health of this server |

The Workflow Action Catalog is also reachable from the action screens. A "Workflow Actions" entry may also appear under Settings.

## 5. Step-by-step tasks

### 5.1 Create a workflow
1. Open Workflow and click Create.
2. Enter Workflow Name and a unique Topic (the topic identifies the workflow for webhooks and is also the key used to start it). Optionally set Group ID to restrict who can call it, Description, Execution Mode and DAG settings.
3. Save. Version and Active are shown on the list.

### 5.2 Add steps
1. In the workflow form, add rows in Action Steps (or use Workflow Action > Create and choose the Workflow).
2. Choose the Action Catalog entry; Action Code and Action Version fill in automatically.
3. Fill the inputs that appear for that type (for example Model Name and operation for `crud_model`, Target URL and Headers for `notify`, Template for `transform_data`, key fields for HMAC/AES).
4. Set Sequence (order), Step Key (a stable name used in audit and resume), Timeout Seconds (default 30), Retry Policy (default 3 attempts, 15 seconds apart) and Continue On Error if the workflow should go on after retries are exhausted. Who makes the next attempt depends on how the run started. Kafka, MQTT and model-event runs (system identity) go to Waiting Retry and the Workflow Resume Due Runs cron (every 60 seconds) makes the next attempt once the cooldown has passed, so the real gap is the cooldown plus up to one cron interval. Webhook and Run Now runs (the caller's identity, marked Caller Bound) retry inside the same request and stay Running; if the server stops meanwhile, the run is closed as Failed instead of being replayed by the system with more rights than the caller. A superadmin webhook call is handed to the cron and answered 202 `retry_scheduled`. DAG workflows retry inside the run. Each attempt is recorded before it starts, so an attempt cut short by a crash still counts: a step that keeps crashing ends in Dead Letter. Timeout Seconds is capped at 900 and the retry cooldown at one day.
5. For Yes/No routing, put a `condition` step first; on later steps set Branch Label to `yes` or `no` and, if several conditions exist, Condition Step Key.
5a. Error handler step (linear workflows): set `on_error_step_key` in the step's Extra Params to the step key of a LATER step. When the step fails for good (retries used up, or a failure that is never retried, such as a wrong HMAC signature), the run jumps to that step instead of ending in Dead Letter; the steps in between are not run. The handler receives the failed step's input plus `_error: {step_key, message}`. The failed step's history row stays Failed; the run ends Succeeded if the handler path succeeds. Validate and Publish refuse a target that does not exist (`on_error_target_missing`) or does not come after the step (`on_error_target_backward`). DAG workflows ignore it (use `failure` edges). A cancelled or timed-out run never jumps. The handler step is an ordinary later step: on the success path the run reaches it too, so gate it with a condition such as `exists: _error` when it must only run after a failure.
5b. Engine keys: `_hmac_verified`, `_error`, `_approval`, `_for_each`, `_dry_run` and `_dry_run_intended` are removed from a trigger's input (webhook body, Kafka/MQTT message, Run Now payload, sub-workflow input) when a run starts, so a caller cannot fake them. Only the engine and its steps set them.
6. Use Generate Flow Map to produce a Markdown plus Mermaid diagram of the sequence.

### 5.3 Add a trigger
Create a `base.workflow.trigger` record (via the workflow form or RPC/MCP) with the Workflow, Trigger Type and Is Active:
- Webhook: call the `/wh/<topic>` route (section 7). No row is needed; add a Webhook row only to set config `hmac_secret` (a `@cred:<name>`/`@syscred:<name>` reference, never the secret itself) and/or `max_body_bytes` (default 1 MB, at most 4 MB). With `hmac_secret` set, every call must be signed:
  `X-Webhook-Timestamp: <unix seconds>` (within 5 minutes of server time) and `X-Webhook-Signature: sha256=<hex HMAC-SHA256(secret, timestamp + "." + Idempotency-Key + "." + raw body)>`, where Idempotency-Key is the header value or empty. The canvas keeps this row when saving (switching to another transport only deactivates it). Without `hmac_secret`, anyone who can call a public webhook can also send a key first, so rely on keys only for signed or group-restricted webhooks.
- Kafka: Topic = Kafka topic; optional config `consumer_group` (default: a group of its own, `wf-<database>-<trigger id>`; a new group starts at the end of the topic). A topic listed in the server's `kafka.topics` (the built-in IPN consumer) is refused. Each offset runs once: the run is named after topic, partition and offset, and a redelivered offset whose run exists is skipped. The offset is committed only after the run succeeded, was scheduled for retry or at least created its run row; if the run could not even start (database down, workflow not loadable), the same message is retried with a growing pause (up to 30 seconds) and later messages of that partition wait. `use_dedupe` is ignored.
- MQTT: Topic = MQTT topic pattern (QoS is always 1). Without `dedupe_key` every message, including a QoS 1 redelivery, starts a new run.
- Kafka and MQTT: optional config `dedupe_key` = the payload field that identifies a message, as a dotted path (`message_id`, `data.txn_id`; a leading `$.` is accepted). The run is then named `<type>:<database>:<topic>:<value>` and a message whose run already exists is skipped as handled. Messages without a text, number or boolean at that path run normally. Values longer than 128 characters are hashed. The canvas does not show this field yet: set it on the trigger row (form, RPC or MCP).
- TCP: raw framed TCP endpoint with a synchronous reply (section 5.9). The topic is derived as `tcp:<port>`.
- Model Event: config requires `model` and `operation` (before/after create, write or unlink); optional `trigger_domain` and `domain` filters. The topic is derived as `model:<model>:<operation>`. A model trigger only observes an event and can never block the write. It fires only for writes in its own database. `before_*` events carry the values before the change but are delivered after the write is committed, like `after_*`. A workflow that writes the model it watches does not fire itself again, and a chain of model triggers stops after 3 levels (logged as a warning). Runs are queued per database (4 at a time, 256 waiting); events beyond that are dropped and logged.
- Any trigger type: optional config `audit_payload` = `full`, `hash` or `none` sets what run history stores (see 5.6). Default `hash` for TCP, `full` for the others. Set it on the trigger row (form, RPC or MCP); the canvas keeps it when saving.

### 5.4 Store secrets
Create a Credential (Name, Credential Type, value). The value is encrypted at rest; reads and exports return it redacted, never in clear. In step parameters, refer to it as `@cred:<name>` (or `@syscred:<name>` for system credentials); the Authorization field of notify/post_output_object expects this form. A credential with no Workflow is shared by all workflows.

Outbound targets of `notify` and `post_output_object` (and manual retries) must resolve to public addresses: loopback, private (10/8, 172.16/12, 192.168/16, fc00::/7), link-local (incl. cloud metadata 169.254.169.254), CGNAT and other reserved ranges are refused, also after a redirect (at most 3, http/https only). To call an internal service, list its IPs or CIDRs in `server.outboundAllowCidrs` in the config file. Response bodies are capped at 1 MB.

### 5.5 Test, validate, export
- Test run: use "Run Now" in the editor (POST test-run). It runs through the normal audited path and is tagged `manual_test`. The workflow must be active.
- DAG: use validate-graph to detect cycles before activating.
- Export / import: export gives a portable JSON document (version 2: workflow settings incl. DAG mode, steps, DAG edges, triggers, layout). It holds no database ids: a step names its type as `action_code@version`, the access group is named by its external id, secrets appear only as `@cred:`/`@syscred:` references and `credentials` lists the credential names the target database needs. Import accepts versions 1 and 2, gives the workflow a fresh name and topic, creates everything in one transaction (all or nothing) and resolves the group by external id (a group it cannot resolve, or a version 1 group id, becomes the system group, never public).
- Copy: duplicate a workflow from the record actions. Steps, DAG edges and triggers are copied in one transaction.
- Copy and import create every Kafka, MQTT, model and TCP trigger inactive (the copy would otherwise consume the same topic as its source): review it, then activate it. The webhook follows the new topic.
- Canvas save: steps and layout are saved in one transaction. If someone else saved the workflow after you opened it, the save is refused with a conflict message: refresh, then apply your changes again. Leaving the canvas with unsaved changes asks for confirmation.
- Step keys are unique per workflow, and a DAG edge must connect existing steps. Deleting a step deletes its edges; renaming its step key moves them. Existing databases get a unique index on (workflow, step key) at startup unless duplicates exist: the server log then lists them (rename them and restart).

### 5.6 Monitor runs
1. Open Workflow Run and use the status filters (Pending, Running, Waiting Retry, Dead Letter, Succeeded, Failed, Cancelled; Retry Scheduled is not a status but runs with a next retry time set) or group by status, trigger type, topic, flow version or current step.
2. Open a run: the Execution and Recovery groups show state, retry time and last error; the Payload, Step History and Audit pages show input/output and each step's result.
3. What a run stores depends on its Audit Mode (from the trigger's `audit_payload`):
   - `full`: the payload with sensitive values replaced by `[redacted]`. Always hidden: `authorization`, `cookie`, `password`, `secret`, `token`, `key`, `pan`, `card_number`, `track2`, `pin`, `pin_block`, `cvv`/`cvc` and any key ending in `_key`, `_secret`, `_token` or `_password` (case-insensitive, `-` counts as `_`). Add your own in the workflow's Audit Redact Keys: a bare name (`card_ref`) matches at any depth, a dotted path (`customer.phone`) from the root.
   - `hash`: only `{"_audit":"hash","bytes":...,"hmac_sha256":...}`. The digest is keyed with the server's `app.encryptKey` (omitted when that key is empty), so equal payloads on one server compare equal but card numbers cannot be brute-forced from it.
   - `none`: only `{"_audit":"none"}`.
   Do not set `full` on a TCP trigger: ISO 8583 fields are not matched by name, so card data would be stored in clear.
   Input Payload and Step History follow the mode at once. Output Payload is the resume point for retries, so it stays complete while the run is Running or Waiting Retry and is reduced when the run finishes. Runs from before this setting keep what they stored. Steps always run on the real payload.

### 5.7 Retry a failed delivery
Each `notify` or `post_output_object` step has one Outbound Delivery row per run, named `<run name>:<step key>`; a retry of the step reuses that row. The request carries the same name as the `Idempotency-Key` header on every attempt (unless the step's Headers set one), so the receiver can drop duplicates.

`notify` rows are sent in the background. The row is stored first; the Workflow Sweep Outbound Deliveries cron (every 30 seconds) then sends rows that were not sent yet, takes over rows stuck in `sending` for more than 5 minutes, and retries `retryable_failed` rows when Next Attempt At is reached. The pause doubles after each failed attempt (15 s, 30 s, 60 s ... at most 1 hour) until Max Attempts (3) is used, then the row becomes `permanent_failed`; a 4xx answer other than 429 is permanent at once. Rows that fell due more than 24 hours ago are left alone. A rerun of a `notify` step whose row is already `delivered` or `sending` does not send again.

`post_output_object` rows are a record of the synchronous call only; the cron never resends them, the step's Retry Policy does.

To send a row again by hand, open Outbound Delivery, pick a record in `queued`, `retryable_failed` or `permanent_failed` and click Retry (its attempt count starts over).

### 5.8 Write a Lua step
Save the script first in Automation > Actions (type lua). In the `lua_script` step pick that action. The script receives `payload` (previous output), `raw` (exact input), `data` (the node's custom JSON), `topic`, `step_key`, `idempotency_key` (`<run name>:<step key>`, the same on every attempt of the step; pass it to systems that deduplicate), and `base.call(model, method, kwargs)` for model methods. The returned table becomes the new payload; returning nothing keeps the (possibly modified) payload. Lua source is never stored in the workflow itself.

### 5.9 TCP trigger (raw frames, synchronous reply)
A `tcp` trigger turns the workflow into a raw TCP endpoint: one framed message in starts one run, and the run's final output is written back as the reply. The transport only delimits frames and protects the connection. It never parses, validates, de-duplicates or builds message contents; anything that looks inside the body belongs in a workflow step or a module the workflow calls.

A tcp run is answered once: every step gets exactly one attempt (Retry Policy is ignored), a failure ends the run as Failed (not Dead Letter), and a tcp run is never resumed or retried later, because the terminal has already given up and may have reversed the transaction.

**Turn it on (server config, off by default).** Create a `tcp` trigger only after enabling `tcp.useTcp` in the config file. Key material stays in the config file and is never stored in the database.

| Key | Default | Meaning |
| --- | --- | --- |
| `useTcp` | `false` | Master switch. Off: `tcp` triggers cannot be created and existing ones are skipped at boot |
| `bind` | `0.0.0.0` | Listen address |
| `defaultPort` / `portMin` / `portMax` | `10123` / `10000` / `49999` | Port a trigger gets when none is set, and the allowed range |
| `maxConns` / `maxConnsPerIp` | `256` / `32` | Connection caps per listener and per client address |
| `maxFrameBytes` | `4096` | Largest accepted frame body; a larger length closes the connection |
| `firstFrameS` / `idleTimeoutS` | `10` / `120` | Seconds allowed for the first frame and between frames |
| `allowCidrs` | empty | Optional network allow-list (empty allows all) |
| `proxyProtocol` / `trustedProxies` | `none` / empty | `none`, `v1`, `v2` or `auto`; only peers in `trustedProxies` may send a PROXY header |
| `tlsCertFile` / `tlsKeyFile` | empty | Certificate and key for `tls` and `mtls` triggers (re-read about every 60 s, so rotation needs no restart) |
| `tlsClientCaFile` | empty | CA used to verify client certificates for `mtls` |

**Trigger settings** (Trigger Type = TCP): one trigger owns one port.

| Setting | Default | Meaning |
| --- | --- | --- |
| Port | `10123` | Must be inside `portMin`-`portMax`; a port already used by another trigger, tenant or process is refused |
| Framing | `len2_bcd` | `len2_bcd` (2 bytes, 4 BCD digits), `len2_bin` (2 bytes big-endian), `stx_etx` (`0x02` ... `0x03` around the body), `len4_ascii` (4 ASCII digits) |
| Length Includes Header | off | Set when the sender counts the header bytes in the length |
| TLS | `none` | `none`, `tls` or `mtls` (client certificate required). A port is either plain or TLS, never auto-detected |
| Reply Timeout (ms) | `30000` | Time the workflow has to answer; on timeout the connection closes without a reply |

The topic is derived as `tcp:<port>`. Copying or importing a workflow creates its tcp trigger inactive, because the port is already taken by the source: pick a free port, then activate it. A malformed header (bad digit, length over `maxFrameBytes`, missing ETX) closes the connection. One connection per transaction is fine: the peer may close after the reply.

**Run input.** Each frame starts one run with this JSON payload. `payload_hex` is the frame body as received (header stripped) and is never decoded.

```json
{"transport":"tcp","local_port":10123,"remote_addr":"203.0.113.9:51234",
 "tls":true,"client_cn":"EDC-000123","conn_id":"...","seq":7,
 "received_at":"2026-11-08T09:00:00Z","payload_len":123,"payload_hex":"6000..."}
```

`remote_addr` is the real client address (after a PROXY header), `client_cn` is the verified client certificate name on `mtls`, and `seq` counts frames on the connection. Frames of one connection run one after another, so replies keep the request order.

**Reply contract.** The workflow's final output is a JSON object:

| Final output | What the transport does |
| --- | --- |
| `{"reply_hex":"..."}` | Hex-decodes it, adds the framing header and writes it; the connection stays open |
| `{"reply_hex":"...","close":true}` | Writes the reply, then closes |
| `{"close":true}` | Closes without writing |
| no `reply_hex`, an undecodable one, a failed run or a timeout | Writes nothing and closes. The transport never invents a reply |

**Echo check.** Save a Lua action in Automation that returns `{reply_hex = payload.payload_hex}`, create a workflow whose only step is a `lua_script` running it, add a `tcp` trigger on 10123 with `len2_bcd`, send a framed payload and expect the same frame back. Test TLS with `openssl s_client -connect host:10123`.

### 5.10 Put a TCP trigger behind a proxy
- **Cloudflare does not proxy raw TCP on its normal orange-cloud path.** It proxies HTTP(S) and WebSocket on a fixed port list that excludes most of 10000-49999. Use a DNS-only (grey cloud) record, or Cloudflare Spectrum (paid; check the plan limits for your account).
- **nginx** needs a `stream {}` block; **HAProxy** needs `mode tcp`. Send the PROXY header with `proxy_protocol on` (nginx) or `send-proxy-v2` (HAProxy) and list those proxies in `trustedProxies` with `proxyProtocol` set.
- **TLS passthrough (recommended):** the proxy forwards TCP and Base HUB terminates TLS or mTLS, so `client_cn` still reaches the workflow. The PROXY header arrives before the TLS handshake.
- **TLS terminated at the proxy:** the Base HUB port is plain TCP and must be reachable only from the proxy; mTLS identity is lost.
- Proxy idle timeouts must exceed `idleTimeoutS` and the longest delay a workflow may add. Open only the ports in use in the firewall.
- Mobile devices share carrier IPs, so keep `maxConnsPerIp` generous.

### 5.11 Wait for approval
1. Add a `wait_approval` step where the run must stop (linear workflows only: Validate and Publish refuse it in a DAG with `wait_approval_in_dag`). Optionally pick an **Approver Group** (`approver_group_id`, a `tbl.groups` row): its members who also hold the workflow operator role can decide the run without the token (step 3). Validate and Publish refuse a group that does not exist (`invalid_param`). Import drops the Approver Group (group ids are not portable between databases): pick it again after importing.
2. When a run reaches it, the run becomes **Waiting Approval**: it holds no worker, the retry cron never picks it up, and a one-time resume token is created. Only a SHA-256 of the token is stored. The token is given once to whoever started that execution: a webhook caller gets `202 {"status":"waiting_approval","run_id":..,"resume_token":..,"expires_at":..}`, Run Now returns it in its result. Replay never returns a token. Runs started by Kafka, MQTT, model events or the cron hand it to nobody, so set an Approver Group on the step, or only an administrator can decide them (step 3). When the run parks, the step's Approver Group is copied onto the run as `approval_group_id` (shown on the run); the decision uses that copy, so republishing the workflow does not change who can decide a run that is already waiting.
3. Decide with `base.workflow.run/approve/<run id>` or `reject/<run id>`, kwargs `{"token": "...", "comment": "..."}`. It needs the operator role and the token. The token may be left out by a system administrator (base.group_system) and by a member of the run's `approval_group_id` (direct or implied membership) who holds the operator role; anyone else without the token gets 403. The first decision wins; a second one, a wrong token (403), an expired wait or a cancelled run is refused. Separation of duties (secure defaults until the owner decides a policy):
   - The user who started the run cannot APPROVE it (403), even as a member of the Approver Group; they may still reject it. A system administrator may approve any run.
   - A Run Now (test) run, which ran the unpublished draft, can only be approved by a system administrator.
   - Replay never hands out a resume token, and replaying a run whose history has a `wait_approval` step needs base.group_system. A replay that stops at a wait is decided by a system administrator.
   - A lost token cannot be recovered or reissued: a system administrator (or an Approver Group operator) decides the run without it, or anyone with the operator role cancels it.
4. Approve: the run continues with the next step, with `_approval: {approved: true, by: <user id>, comment, decided_at}` added to the payload. Reject: the run ends Failed, or continues at the step's `on_error_step_key` with `_error` and `_approval`. The decision is saved in the same database write that claims the run, so a server crash right after it never loses it. The answer is `{id, status, last_error}`:
   - A run started by a system trigger (Kafka, MQTT, model event) is handed back to the Workflow Resume Due Runs cron, which continues it with system rights under the workflow's Max Concurrent Runs. The answer comes at once with `status: "waiting_retry"`.
   - A webhook or Run Now run continues inside the approve request, under the identity of the user who STARTED it (an anonymous webhook run stays anonymous), never the approver's. When the workflow's Max Concurrent Runs is full the answer is 409 `workflow concurrency limit reached` and the run keeps waiting with the same token and expiry: try again later. The answer carries a new `resume_token` when the run stops at another wait step.
   - A decision that ends the run (reject without `on_error_step_key`, approve at the last step) answers its final status.
5. Cancel Run ends a waiting run at once. An expired wait is closed as Failed (`approval wait expired`) by the Workflow Resume Due Runs cron. Waiting runs are never removed by the retention purge. A TCP run or a sub-workflow (`call_workflow`, `for_each`) cannot wait: the step fails. A dry run skips the wait and continues as approved.

## 6. Administration and configuration

| Topic | Detail |
| --- | --- |
| Access | Internal users (`base.group_user`) get the platform baseline on `base.*` models: read on everything here. Creating, changing or deleting workflows, steps, triggers, edges, credentials and the action catalog is limited to `base.group_system` (steps run with full rights, so authoring a workflow is an administrator task). Runs and outbound deliveries are not restricted beyond the baseline |
| Workflow access group | `Group ID` on a workflow restricts the webhook and test-run routes to members of that group (superadmin always allowed). Empty makes the webhook public (no sign-in needed: protect it with `hmac_secret`); the `/wf` test-run and helper routes always require a signed-in caller |
| Company scope | Steps, triggers, runs inherit the company of the parent workflow |
| Seeded workflows | `demo_txn_forward_v1` (topic `demo`), `bank_account_import_v1` (topic `bank`), a crypto demo, plus the action catalog |
| Scheduled jobs | Two `sys.cron` records are seeded inactive: Workflow Resume Due Runs (every 60 s, limit 50) and Workflow Reap Stale Running Runs (every 60 s, limit 50, stale after 300 s). Activate them in the scheduled-jobs screen for automatic retries and cleanup |
| Retry defaults | 3 attempts, 15 seconds cooldown, for steps and outbound deliveries |

## 7. API and automation touchpoints

| Touchpoint | Purpose |
| --- | --- |
| `ANY /wh/<topic>` | Starts the active webhook workflow with that topic (no active MQTT/Kafka/model/TCP trigger); body is the payload. Returns `{topic, workflow, status}`; unknown topic returns 404 "no workflow". Optional `Idempotency-Key` header (1-128 of `A-Z a-z 0-9 . _ : -`): a key the same caller (same user, or anonymous) already used for this workflow returns `status: "duplicate"` and does not run again (a signed call without a key is deduplicated by its signature). Body over the limit: 413. Unsigned or badly signed call to a signed webhook: 401. Anonymous callers get a generic error text |
| `POST /wf/workflows/:id/test-run` | Manual run with `{payload}`; returns `run_id` and `status` |
| `POST /wf/workflows/:id/validate-graph` | Cycle check for DAG edges |
| `GET /wf/workflows/:id/export`, `POST /wf/workflows/import` | Export / import a workflow |
| `GET /wf/runs/latest` | Most recent run for a topic |
| `GET /wf/node-specs`, `/wf/credentials`, `/wf/sys-credentials`, `/wf/sys-models` | Canvas helpers (step specs, credential and model lists) |
| Model methods | `replace_action_sequence`, `replace_workflow_edges`, `copy`, `export`, `import`, `writeWorkflowFlowMap`, `manual_retry`, `mqtt_metrics` |
| MCP tools | `workflow.record.search_read`, `.read`, `.create`, `.write`, `.unlink`, `workflow.export`, `workflow.import`, `workflow.replace_action_sequence`, `workflow.replace_workflow_edges` |

## 8. Troubleshooting and FAQ

| Symptom | Likely cause and action |
| --- | --- |
| Webhook returns 404 "no workflow" | Topic does not match an active workflow, the workflow uses another trigger (MQTT, Kafka, model, TCP), or the module is not installed in that database |
| 401 / 403 on the webhook | Caller is not authenticated, not in the workflow's Group ID, or the webhook has `hmac_secret` and the signature/timestamp is missing, wrong or older than 5 minutes (check the sender clock) |
| Webhook returns `status: "duplicate"` | The `Idempotency-Key` (or the same signed request) was already used for this topic; send a new key for a new event |
| Run stays in Waiting Retry | The resume cron is inactive; activate Workflow Resume Due Runs |
| Run stuck in Running | Worker stopped mid-run; activate Workflow Reap Stale Running Runs |
| Run ends in Dead Letter / Failed | Open Step History and Last Error; fix the step or input, then run again |
| Graph rejected on save | The edges contain a cycle; the error names the offending step keys |
| Outbound send fails | Check Target Ref, Headers and the `@cred:` reference; read Last Error and Response Body, then Retry |
| Kafka/MQTT trigger idle | Trigger inactive or broker down; check MQTT Monitor. A Kafka partition that stops moving logs `handler error ... not committed`: fix the cause (database, workflow) and it resumes by itself |
| Notify delivery stays `queued` | The Workflow Sweep Outbound Deliveries cron is missing or inactive (existing databases get it with a master-data import) |
| Trigger change not picked up by another server | Servers reload a changed trigger within about 10 seconds and resync every trigger every 2 minutes |
| Duplicate name/topic error | Name and Topic must be unique; import and copy add a suffix automatically |
| Canvas save says the workflow was saved by someone else | Another user (or tab) saved it after you opened it; refresh and apply your changes again |
| "step_key ... is already used" or edge "references step(s) that do not exist" | Step keys must be unique in a workflow and edges must connect saved steps; rename the step or save the canvas first |
| Copied or imported workflow does not receive Kafka/MQTT messages | Its non-webhook triggers are created inactive on purpose; activate them after review |
| Import refused: group does not exist | The exported access group (external id) is missing in this database; create it or edit `flow.group` in the document |

## 9. Limits and notes

- Test runs and webhook calls are synchronous: the HTTP response returns after the run reaches a terminal or in-flight state.
- DAG mode only takes effect once edges exist; otherwise the workflow runs linearly.
- Model-event triggers are observers only; they cannot veto a record change.
- MQTT subscriptions always use QoS 1; deduplication by a payload field needs `dedupe_key`.
- MQTT Monitor figures are per server instance, not cluster-wide.
- Credential values cannot be read back; re-enter to change them.
- Catalog entries cannot be created or deleted from the UI.
