// Validation summary (T98): lists the issues `base.workflow/validate` reported that are not tied to
// one step (edges, triggers, workflow-level) and counts the per-step ones, which the canvas shows as
// badges on their nodes. Codes are translated here; the server's English message is the detail.
import { Component, loadTemplate, xml } from "nodexui/owl";
import { Alert, Badge, Button, Icon } from "nodexui/component";

const template = await loadTemplate(new URL("./ValidationPanel.xml", import.meta.url));

// issueCodeLabel translates one validate issue code into a short readable title.
export function issueCodeLabel(code) {
  switch (code) {
    case "no_steps":
      return t("The workflow has no steps");
    case "no_active_trigger":
      return t("No active trigger");
    case "step_key_empty":
      return t("A step has no step key");
    case "step_key_duplicate":
      return t("Duplicate step key");
    case "unknown_action":
      return t("Unknown action");
    case "invalid_param":
      return t("Invalid parameter");
    case "edge_missing_step":
      return t("A connection points to a missing step");
    case "condition_expr_empty":
      return t("A condition connection has no expression");
    case "cycle":
      return t("The graph has a cycle");
    case "step_unreachable":
      return t("Step can never run");
    case "trigger_config_invalid":
      return t("Invalid trigger settings");
    case "on_error_target_missing":
      return t("The on-error target step does not exist");
    case "on_error_target_backward":
      return t("The on-error target must be a later step");
    case "wait_approval_in_dag":
      return t("Wait for approval works in linear workflows only");
    default:
      return code || t("Issue");
  }
}

// issueText is the one-line text for a tooltip or list row: translated title, then server detail.
export function issueText(issue) {
  const title = issueCodeLabel(issue.code);
  const detail = issue.param ? `${issue.param}: ${issue.message || ""}` : issue.message || "";
  return detail ? `${title}: ${detail}` : title;
}

export class ValidationPanel extends Component {
  static template = xml({ raw: [template] });
  static components = { Alert, Badge, Button, Icon };

  // generalIssues are the ones no node can show: edges, triggers, the workflow itself.
  get generalIssues() {
    return this.props.issues.filter((issue) => !issue.step_key);
  }

  get stepIssueCount() {
    return this.props.issues.filter((issue) => issue.step_key).length;
  }

  get errorCount() {
    return this.props.issues.filter((issue) => issue.severity === "error").length;
  }

  get warningCount() {
    return this.props.issues.length - this.errorCount;
  }

  // rowKey gives each issue row a stable key (several issues can share one code).
  rowKey(issue, index) {
    return `${issue.code}:${issue.edge_id || issue.trigger_id || ""}:${index}`;
  }

  label(issue) {
    return issueCodeLabel(issue.code);
  }

  detail(issue) {
    return issue.param ? `${issue.param}: ${issue.message || ""}` : issue.message || "";
  }
}
