// Form widget `project_helpdesk_convert`, opened by the bound client actions in
// modules/project-helpdesk/views. One component serves both directions, chosen
// by the action's `direction` param:
//   ticket_to_task — pick a project (+ optional stage), POST .../tickets/:id/convert-to-task
//   task_to_ticket — pick a helpdesk team (+ optional stage), POST .../tasks/:id/convert-to-ticket
// Every lookup goes through the generic model RPC, so the viewer's ACL and
// record rules decide which projects/teams/stages are offered.
import { Component, loadTemplate, onWillStart, useState, xml } from "nodexui/owl";

import {
  authenticatedHeaders,
  buildBackendURL,
  createModelRpc,
  readResponsePayload,
} from "@/services/api/jsonv2.js";

const template = await loadTemplate(new URL("./ConvertRecord.xml", import.meta.url));

// DIRECTIONS holds everything that differs between the two conversions.
const DIRECTIONS = {
  ticket_to_task: {
    targetModel: "tbl.project",
    targetDomain: [["is_template", "=", false]],
    targetLabel: "Project",
    stageModel: "tbl.project_task_type",
    stageDomain: [["user_id", "=", false]],
    stageLink: "project_ids",
    path: (id) => `/project-helpdesk/tickets/${id}/convert-to-task`,
    body: (targetID, stageID) => ({ project_id: targetID, ...(stageID ? { stage_id: stageID } : {}) }),
    resultKey: "task_id",
    resultRoute: (id) => `/web/tbl.project_task/${id}`,
    submitLabel: "Convert to task",
    hint: "The ticket is archived and a task is created from its subject, description and customer.",
  },
  task_to_ticket: {
    targetModel: "tbl.helpdesk_team",
    targetDomain: [],
    targetLabel: "Helpdesk team",
    stageModel: "tbl.helpdesk_stage",
    stageDomain: [],
    stageLink: "team_ids",
    path: (id) => `/project-helpdesk/tasks/${id}/convert-to-ticket`,
    body: (targetID, stageID) => ({ team_id: targetID, ...(stageID ? { stage_id: stageID } : {}) }),
    resultKey: "ticket_id",
    resultRoute: (id) => `/web/tbl.helpdesk_ticket/${id}`,
    submitLabel: "Convert to ticket",
    hint: "The task is archived and a ticket is created from its title, description and customer.",
  },
};

// idOf unwraps a many2many entry that may be a bare id or an [id, label] tuple.
function idOf(entry) {
  return Array.isArray(entry) ? Number(entry[0]) : Number(entry);
}

async function searchRows(model, domain, fields) {
  const response = await createModelRpc(model).search(domain, fields, "id ASC", 0, 200, false);
  return Array.isArray(response.result) ? response.result : [];
}

export class ConvertRecord extends Component {
  static template = xml({ raw: [template] });
  static props = {
    ctx: { type: Object, optional: true },
    node: { type: Object, optional: true },
    record: { type: Object, optional: true },
    readonly: { type: Boolean, optional: true },
  };

  setup() {
    this.state = useState({ targets: [], stages: [], targetID: "", stageID: "", loading: true, submitting: false, error: "" });
    onWillStart(() => this.loadOptions());
  }

  get config() {
    return DIRECTIONS[this.props.node?.attrs?.direction] ?? DIRECTIONS.ticket_to_task;
  }

  get targetOptions() {
    return this.state.targets.map((row) => ({ value: String(row.id), label: String(row.name ?? row.id) }));
  }

  // stageOptions lists only the stages linked to the chosen target, so a stage
  // that the target does not use can never be sent.
  get stageOptions() {
    const targetID = Number(this.state.targetID);
    return this.state.stages
      .filter((row) => (row[this.config.stageLink] ?? []).map(idOf).includes(targetID))
      .map((row) => ({ value: String(row.id), label: String(row.name ?? row.id) }));
  }

  get canSubmit() {
    return Boolean(this.state.targetID) && !this.state.submitting && !this.state.loading;
  }

  async loadOptions() {
    try {
      const config = this.config;
      const [targets, stages] = await Promise.all([
        searchRows(config.targetModel, config.targetDomain, ["id", "name"]),
        searchRows(config.stageModel, config.stageDomain, ["id", "name", config.stageLink]),
      ]);
      this.state.targets = targets;
      this.state.stages = stages;
    } catch (error) {
      this.state.error = error?.message || "Could not load the available options.";
    } finally {
      this.state.loading = false;
    }
  }

  onTargetChange(value) {
    this.state.targetID = value ? String(value) : "";
    this.state.stageID = "";
  }

  onStageChange(value) {
    this.state.stageID = value ? String(value) : "";
  }

  // submit posts the conversion; the endpoint creates the new record and
  // archives the source, so on success we jump to the new record.
  async submit() {
    if (!this.canSubmit) return;
    const config = this.config;
    const recordID = Number(this.props.record?.id);
    this.state.submitting = true;
    this.state.error = "";
    try {
      const response = await fetch(buildBackendURL(config.path(recordID)), {
        method: "POST",
        cache: "no-store",
        headers: authenticatedHeaders({ "Content-Type": "application/json" }),
        body: JSON.stringify(config.body(Number(this.state.targetID), Number(this.state.stageID) || 0)),
      });
      const payload = await readResponsePayload(response);
      if (!response.ok) {
        throw new Error(payload?.message || payload?.error?.message || payload?.error || `Conversion failed (HTTP ${response.status})`);
      }
      const newID = payload?.result?.[config.resultKey];
      this.props.ctx?.closeAndNavigate?.(newID ? config.resultRoute(newID) : `/web/${this.props.ctx?.model ?? ""}`);
    } catch (error) {
      this.state.error = error?.message || "Conversion failed.";
    } finally {
      this.state.submitting = false;
    }
  }

  cancel() {
    this.props.ctx?.close?.();
  }
}
