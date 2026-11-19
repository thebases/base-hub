// Trigger list (T104, multi-trigger editing): every trigger row of the workflow in one sheet. Kafka,
// MQTT, model and TCP rows can be added, edited (the shared node parameter dialog, opened by the
// page), switched on/off and removed; each shows its listener health (T99).
// Webhook (Phase 6 follow-up): /wh/<topic> serves while the workflow has no other trigger row (implicit
// webhook), or, next to other triggers, while an explicit webhook row is active (backend
// workflowWebhookSettingsFor). An explicit webhook row is added and edited here (HMAC secret
// credential ref, body limit) and switched on/off, but never deleted or turned into another
// transport: it carries the signature config.
import { Component, loadTemplate, useState, xml } from "nodexui/owl";
import { Alert, Badge, Button, Field, Icon, Input, NativeSelect, Sheet, SheetContent, SheetHeader, SheetTitle, Switch, Tooltip } from "nodexui/component";

import { fetchWorkflowCredentials, fetchWorkflowSysCredentials } from "@/modules/workflow/api.js";
import { removeWorkflowTriggerRow, saveWorkflowWebhookRow, setWorkflowTriggerRowActive } from "@/modules/workflow/canvas-graph.js";
import { reportWorkflowError } from "@/modules/workflow/components/notify.js";
import { healthText, healthTone } from "@/modules/workflow/components/trigger-health.js";
import { SYS_CREDENTIAL_REF_PREFIX, credentialsForParam } from "@/modules/workflow/node-params.js";

const template = await loadTemplate(new URL("./TriggerListPanel.xml", import.meta.url));

const WORKFLOW_CREDENTIAL_REF_PREFIX = "@cred:";
// Mirrors backend WebhookMaxBodyBytesLimit (4 MiB); the server default is 1 MiB.
const WEBHOOK_MAX_BODY_BYTES_LIMIT = 4 << 20;

export class TriggerListPanel extends Component {
  static template = xml({ raw: [template] });
  static components = { Alert, Badge, Button, Field, Icon, Input, NativeSelect, Sheet, SheetContent, SheetHeader, SheetTitle, Switch, Tooltip };

  setup() {
    this.state = useState({ addType: "", busyID: 0, error: "", webhookEdit: null, credentials: [], sysCredentials: [] });
  }

  // transportRows are the non-webhook trigger rows (added, edited and removed through the dialog).
  get transportRows() {
    return this.props.triggers.filter((row) => row.type !== "webhook");
  }

  // webhookRows are the explicit webhook rows (normally at most one).
  get webhookRows() {
    return this.props.triggers.filter((row) => row.type === "webhook");
  }

  // webhookServes mirrors the /wh rule: an active webhook row, or no other trigger row at all.
  get webhookServes() {
    return this.webhookRows.some((row) => row.is_active) || this.transportRows.length === 0;
  }

  // webhookHint says in one sentence why the webhook serves or not right now.
  get webhookHint() {
    if (this.transportRows.length === 0) return t("Built in: the webhook serves this workflow while it has no other trigger.");
    if (this.webhookRows.some((row) => row.is_active)) return t("Serving next to the other triggers while the webhook row is active.");
    if (this.webhookRows.length) return t("Off: switch the webhook row on to serve it next to the other triggers.");
    return t("Off while the workflow has other triggers. Add a Webhook trigger to serve it next to them.");
  }

  // Webhook is offered while the workflow has no webhook row yet; a row is never added twice.
  get addableSpecs() {
    if (!this.webhookRows.length) return this.props.specs;
    return this.props.specs.filter((spec) => spec.type !== "webhook");
  }

  get selectedAddType() {
    return this.state.addType || this.addableSpecs[0]?.type || "";
  }

  // secretOptions lists the credentials a webhook secret can point at (workflow and system ones),
  // keeping the stored ref even when the credential is not listed (deleted or not visible).
  get secretOptions() {
    const options = [
      ...credentialsForParam(this.state.credentials, "hmac_key").map((credential) => ({
        value: `${WORKFLOW_CREDENTIAL_REF_PREFIX}${credential.name}`,
        label: t("{name} (workflow credential)", { name: credential.name }),
      })),
      ...this.state.sysCredentials.map((credential) => ({
        value: `${SYS_CREDENTIAL_REF_PREFIX}${credential.name}`,
        label: t("{name} (system credential)", { name: credential.name }),
      })),
    ];
    const current = this.state.webhookEdit?.hmacSecret ?? "";
    if (current && !options.some((option) => option.value === current)) options.unshift({ value: current, label: current });
    return options;
  }

  typeLabel(type) {
    return this.props.specs.find((spec) => spec.type === type)?.label ?? type;
  }

  tone(row) {
    return healthTone(this.props.health[row.id]);
  }

  toneText(row) {
    return healthText(this.props.health[row.id]);
  }

  // webhookSummary describes a webhook row's signature and body limit in one line.
  webhookSummary(row) {
    const secret = row.config?.hmac_secret;
    const limit = row.config?.max_body_bytes;
    const signature = secret ? t("Signed with {ref}", { ref: secret }) : t("Unsigned requests accepted");
    return limit ? t("{signature} · body limit {bytes} bytes", { signature, bytes: limit }) : signature;
  }

  // addTrigger opens the parameter dialog on a new, unsaved row of the chosen type (the webhook
  // settings form for a webhook).
  addTrigger() {
    const type = this.selectedAddType;
    if (!type) return;
    if (type === "webhook") {
      this.openWebhookEditor(null);
      return;
    }
    this.props.onEdit({ id: 0, type, topic: "", config: {}, is_active: true });
  }

  editRow(row) {
    if (row.type === "webhook") {
      this.openWebhookEditor(row);
      return;
    }
    this.props.onEdit({ ...row, config: { ...row.config } });
  }

  // openWebhookEditor shows the webhook settings form for a row (null: a new row) and loads the
  // credential lists; a failed list load leaves only the stored ref to pick.
  async openWebhookEditor(row) {
    const limit = row?.config?.max_body_bytes;
    this.state.error = "";
    this.state.webhookEdit = { row, hmacSecret: row?.config?.hmac_secret ?? "", maxBodyBytes: limit ? String(limit) : "" };
    try {
      this.state.credentials = await fetchWorkflowCredentials();
    } catch {
      this.state.credentials = [];
    }
    try {
      this.state.sysCredentials = await fetchWorkflowSysCredentials();
    } catch {
      this.state.sysCredentials = [];
    }
  }

  closeWebhookEditor() {
    this.state.webhookEdit = null;
  }

  // saveWebhook creates or rewrites the webhook row, then lets the page re-read the trigger rows.
  async saveWebhook() {
    const edit = this.state.webhookEdit;
    if (!edit || this.state.busyID) return;
    const maxBodyBytes = String(edit.maxBodyBytes ?? "").trim();
    if (maxBodyBytes !== "" && !(/^\d+$/.test(maxBodyBytes) && Number(maxBodyBytes) > 0 && Number(maxBodyBytes) <= WEBHOOK_MAX_BODY_BYTES_LIMIT)) {
      this.state.error = t("Max body bytes must be a whole number between 1 and {limit}.", { limit: WEBHOOK_MAX_BODY_BYTES_LIMIT });
      return;
    }
    this.state.busyID = -1;
    this.state.error = "";
    try {
      await saveWorkflowWebhookRow(this.props.workflowID, this.props.workflowTopic, edit.row, { hmacSecret: edit.hmacSecret, maxBodyBytes });
      this.state.webhookEdit = null;
      await this.props.onChanged();
    } catch (error) {
      this.state.error = reportWorkflowError(error, t("Failed to save the webhook settings."));
    } finally {
      this.state.busyID = 0;
    }
  }

  // toggleRow switches one trigger on or off right away (no canvas save needed).
  async toggleRow(row, active) {
    await this.runRowAction(row, () => setWorkflowTriggerRowActive(row, active), t("Failed to update the trigger."));
  }

  // removeRow deletes one trigger row after a confirmation.
  async removeRow(row) {
    if (!window.confirm(t("Remove the {type} trigger \"{topic}\"?", { type: this.typeLabel(row.type), topic: row.topic || "—" }))) return;
    await this.runRowAction(row, () => removeWorkflowTriggerRow(row), t("Failed to remove the trigger."));
  }

  // runRowAction runs one row change, reports a failure inline (or as a permission toast) and
  // lets the page re-read the trigger rows.
  async runRowAction(row, action, fallback) {
    if (this.state.busyID) return;
    this.state.busyID = row.id;
    this.state.error = "";
    try {
      await action();
    } catch (error) {
      this.state.error = reportWorkflowError(error, fallback);
    } finally {
      this.state.busyID = 0;
      await this.props.onChanged();
    }
  }
}
