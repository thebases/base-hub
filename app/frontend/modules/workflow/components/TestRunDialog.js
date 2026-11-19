// "Run Now" test-run dialog (T101): edits the sample payload, runs the saved draft once through the
// audited test-run route, with a "Dry run" switch (on by default) that skips every step with
// outside effects. The result shows the run status and, for a dry run, what each skipped step
// would have done (`_dry_run_intended` in the step outputs).
import { Component, loadTemplate, onWillUpdateProps, useState, xml } from "nodexui/owl";
import {
  Alert,
  Badge,
  Button,
  ClipboardButton,
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  Field,
  Spinner,
  Switch,
  Textarea,
} from "nodexui/component";

import { fetchWorkflowRun, runWorkflowTestRun } from "@/modules/workflow/api.js";
import { reportWorkflowError } from "@/modules/workflow/components/notify.js";
import { collectDryRunIntents, runStatusLabel, runStatusVariant } from "@/modules/workflow/components/run-format.js";

const template = await loadTemplate(new URL("./TestRunDialog.xml", import.meta.url));

export class TestRunDialog extends Component {
  static template = xml({ raw: [template] });
  static components = {
    Alert,
    Badge,
    Button,
    ClipboardButton,
    Dialog,
    DialogContent,
    DialogDescription,
    DialogFooter,
    DialogHeader,
    DialogTitle,
    Field,
    Spinner,
    Switch,
    Textarea,
  };

  setup() {
    this.state = useState({ dryRun: true, running: false, error: "", result: null, run: null });
    onWillUpdateProps((nextProps) => {
      if (nextProps.open && !this.props.open) this.reset();
    });
  }

  // reset clears the previous result each time the dialog opens; dry run is on again by default.
  reset() {
    this.state.dryRun = true;
    this.state.running = false;
    this.state.error = "";
    this.state.result = null;
    this.state.run = null;
  }

  get intents() {
    return collectDryRunIntents(this.state.run?.step_runs);
  }

  get statusVariant() {
    return runStatusVariant(this.state.result?.status);
  }

  get statusLabel() {
    return runStatusLabel(this.state.result?.status);
  }

  // run parses the payload, starts the test run, then reads the run back for its step outputs.
  async run() {
    let payload = {};
    const text = (this.props.sampleJSON ?? "").trim();
    if (text) {
      try {
        payload = JSON.parse(text);
      } catch {
        this.state.error = t("Sample Payload is not valid JSON.");
        return;
      }
    }
    this.state.running = true;
    this.state.error = "";
    this.state.result = null;
    this.state.run = null;
    try {
      const result = await runWorkflowTestRun(this.props.workflowID, payload, { dryRun: this.state.dryRun });
      this.state.result = result;
      if (result?.run_id) this.state.run = await fetchWorkflowRun(result.run_id);
      this.props.onRunFinished?.();
    } catch (error) {
      this.state.error = reportWorkflowError(error, t("Failed to run workflow test."));
    } finally {
      this.state.running = false;
    }
  }

  openRun() {
    this.props.onOpenRun(this.state.result.run_id);
  }
}
