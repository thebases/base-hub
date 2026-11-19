// Owl port of WorkflowCanvas.tsx's DagSettingsDialog: edits
// max_parallel_steps / dag_failure_policy, shown only in DAG mode. Draft state
// resets from props each time the dialog opens, same as the React version.
import { Component, loadTemplate, onWillUpdateProps, useState, xml } from "nodexui/owl";
import { Alert, Button, Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle, Field, Input, NativeSelect } from "nodexui/component";


const template = await loadTemplate(new URL("./DagSettingsDialog.xml", import.meta.url));

export class DagSettingsDialog extends Component {
  static template = xml({ raw: [template] });
  static components = { Alert, Button, Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle, Field, Input, NativeSelect };

  setup() {
    this.state = useState({ maxParallel: "4", failurePolicy: "fail_branch", error: "" });
    this.resetDraft(this.props);
    onWillUpdateProps((nextProps) => {
      if (nextProps.state.open && !this.props.state.open) this.resetDraft(nextProps);
    });
  }

  resetDraft(props) {
    this.state.maxParallel = String(props.state.maxParallelSteps ?? 4);
    this.state.failurePolicy = props.state.dagFailurePolicy ?? "fail_branch";
    this.state.error = "";
  }

  get parsedMaxParallel() {
    return Number.parseInt(this.state.maxParallel, 10);
  }

  get isValid() {
    return Number.isFinite(this.parsedMaxParallel) && this.parsedMaxParallel > 0;
  }

  cancel() {
    this.props.onOpenChange(false);
  }

  async save() {
    if (!this.isValid) {
      this.state.error = t("Max Parallel Steps must be a positive number.");
      return;
    }
    this.state.error = "";
    await this.props.onSave(this.parsedMaxParallel, this.state.failurePolicy);
  }
}
