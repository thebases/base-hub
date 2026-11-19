// Run inspector (T95/T100): a side sheet listing the workflow's runs (newest first, paged) and a
// run detail view with the step timeline and the stored (already redacted) payloads. Actions:
// replay the whole run or from one step (linear workflows), cancel an unfinished run. While the
// shown run (or a run on the shown page) is unfinished it refreshes every 3 s; the timer stops
// when everything shown is finished, when the sheet closes, and on unmount.
import { Component, loadTemplate, onWillUnmount, onWillUpdateProps, useState, xml } from "nodexui/owl";
import { Alert, Badge, Button, Field, Icon, Input, Sheet, SheetContent, SheetHeader, SheetTitle, Spinner } from "nodexui/component";

import { RPC_CONFLICT, cancelWorkflowRun, decideWorkflowApproval, fetchWorkflowRun, fetchWorkflowRuns, replayWorkflowRun, workflowErrorInfo } from "@/modules/workflow/api.js";
import { notifySuccess, reportWorkflowError } from "@/modules/workflow/components/notify.js";
import {
  collectDryRunIntents,
  formatDuration,
  formatRunTime,
  isRunActive,
  prettyJSON,
  runStatusLabel,
  runStatusVariant,
} from "@/modules/workflow/components/run-format.js";

const template = await loadTemplate(new URL("./RunInspector.xml", import.meta.url));

const PAGE_SIZE = 20;
const POLL_MS = 3000;
// Statuses a finished run can be replayed from (the server checks again).
const REPLAYABLE_STATUSES = ["succeeded", "failed", "dead_letter", "cancelled"];

export class RunInspector extends Component {
  static template = xml({ raw: [template] });
  static components = { Alert, Badge, Button, Field, Icon, Input, Sheet, SheetContent, SheetHeader, SheetTitle, Spinner };

  setup() {
    this.state = useState({
      runs: [],
      total: 0,
      offset: 0,
      loading: false,
      error: "",
      runID: 0,
      run: null,
      busy: false,
      expanded: {},
      approvalToken: "",
      approvalComment: "",
      approvalMessage: "",
    });
    this.pollTimer = null;
    this.alive = true;
    if (this.props.open) void this.show(this.props.runID);
    onWillUpdateProps((nextProps) => {
      if (nextProps.open && (!this.props.open || nextProps.runID !== this.props.runID)) void this.show(nextProps.runID, nextProps);
      if (!nextProps.open && this.props.open) this.stopPolling();
    });
    onWillUnmount(() => {
      this.alive = false;
      this.stopPolling();
    });
  }

  // show opens the sheet on one run's detail, or on the first page of the list.
  async show(runID, props = this.props) {
    this.state.error = "";
    if (runID) {
      await this.openRun(runID, props);
      return;
    }
    this.state.runID = 0;
    this.state.run = null;
    this.state.offset = 0;
    await this.loadList(props);
  }

  // loadList fetches the current page of runs.
  async loadList(props = this.props) {
    this.stopPolling();
    this.state.loading = true;
    try {
      const { rows, total } = await fetchWorkflowRuns(props.workflowID, { limit: PAGE_SIZE, offset: this.state.offset });
      // The user may have opened a run (or the sheet may be gone) while the list was loading.
      if (!this.alive || this.state.runID) return;
      this.state.runs = rows;
      this.state.total = total;
      this.state.error = "";
      if (rows.some((row) => isRunActive(row.status))) this.schedulePoll(() => this.loadList());
    } catch (error) {
      if (!this.alive || this.state.runID) return;
      this.state.error = reportWorkflowError(error, t("Failed to load workflow runs."));
    } finally {
      this.state.loading = false;
    }
  }

  // openRun shows one run's detail and keeps it fresh while it is unfinished.
  async openRun(runID, props = this.props) {
    this.stopPolling();
    if (runID !== this.state.runID) {
      this.state.run = null;
      this.state.expanded = {};
      this.state.approvalToken = "";
      this.state.approvalComment = "";
      this.state.approvalMessage = "";
    }
    this.state.runID = runID;
    this.state.loading = true;
    try {
      const run = await fetchWorkflowRun(runID);
      if (!this.alive || !props.open || this.state.runID !== runID) return;
      this.state.run = run;
      this.state.error = run ? "" : t("Run #{id} was not found.", { id: runID });
      if (run && isRunActive(run.status)) this.schedulePoll(() => this.openRun(runID));
    } catch (error) {
      if (!this.alive || this.state.runID !== runID) return;
      this.state.error = reportWorkflowError(error, t("Failed to load the run."));
    } finally {
      this.state.loading = false;
    }
  }

  // schedulePoll re-runs one refresh after 3 s, only while the sheet is open.
  schedulePoll(refresh) {
    if (!this.alive || !this.props.open) return;
    this.stopPolling();
    this.pollTimer = setTimeout(() => {
      this.pollTimer = null;
      if (this.alive && this.props.open) void refresh();
    }, POLL_MS);
  }

  stopPolling() {
    if (this.pollTimer) clearTimeout(this.pollTimer);
    this.pollTimer = null;
  }

  backToList() {
    this.stopPolling();
    this.state.runID = 0;
    this.state.run = null;
    void this.loadList();
  }

  pagePrev() {
    this.state.offset = Math.max(0, this.state.offset - PAGE_SIZE);
    void this.loadList();
  }

  pageNext() {
    this.state.offset += PAGE_SIZE;
    void this.loadList();
  }

  get hasPrev() {
    return this.state.offset > 0;
  }

  get hasNext() {
    return this.state.offset + PAGE_SIZE < this.state.total;
  }

  get pageText() {
    if (!this.state.total) return "";
    const from = this.state.offset + 1;
    const to = Math.min(this.state.offset + PAGE_SIZE, this.state.total);
    return t("{from}–{to} of {total}", { from, to, total: this.state.total });
  }

  get canReplay() {
    return Boolean(this.state.run) && REPLAYABLE_STATUSES.includes(this.state.run.status) && !this.state.run.dry_run;
  }

  get canCancel() {
    return Boolean(this.state.run) && isRunActive(this.state.run.status) && !this.state.run.cancel_requested;
  }

  get canDecide() {
    return Boolean(this.state.run) && this.state.run.status === "waiting_approval";
  }

  // showCancelling: the "Cancelling" badge only means something while the run is still active.
  showCancelling(run) {
    return Boolean(run?.cancel_requested) && isRunActive(run.status);
  }

  // approvalExpiry shows when the approval wait ends ("Never" when it has no deadline).
  get approvalExpiry() {
    const at = Number(this.state.run?.approval_expires_at ?? 0);
    return at > 0 ? formatRunTime(at) : t("Never");
  }

  // canReplayFromStep: replay from one step exists for linear workflows only (the server refuses DAG).
  get canReplayFromStep() {
    return this.canReplay && !this.props.isDag;
  }

  get steps() {
    return Array.isArray(this.state.run?.step_runs) ? this.state.run.step_runs : [];
  }

  get intents() {
    return collectDryRunIntents(this.steps);
  }

  // parentRun reads the many2one [id, name] of the run this one replays (false when none).
  parentRun(run) {
    return Array.isArray(run?.parent_run_id) && run.parent_run_id.length ? { id: run.parent_run_id[0], name: run.parent_run_id[1] } : null;
  }

  // approvalGroupName is the approver group of the wait the run is parked at (empty when the step
  // names none). The field keeps the last wait's group after it ends, so it is shown only while
  // the run is waiting for approval.
  get approvalGroupName() {
    const group = this.state.run?.approval_group_id;
    return Array.isArray(group) && group.length > 1 ? group[1] : "";
  }

  stepKey(step, index) {
    return `${step.step_key || step.step_name}:${step.attempt}:${index}`;
  }

  isExpanded(key) {
    return Boolean(this.state.expanded[key]);
  }

  // toggleExpanded shows or hides one payload block (replacing the object so Owl re-renders).
  toggleExpanded(key) {
    this.state.expanded = { ...this.state.expanded, [key]: !this.state.expanded[key] };
  }

  statusVariant(status) {
    return runStatusVariant(status);
  }

  statusLabel(status) {
    return runStatusLabel(status);
  }

  time(value) {
    return formatRunTime(value);
  }

  duration(ms) {
    return formatDuration(ms);
  }

  json(value) {
    return prettyJSON(value);
  }

  triggerLabel(type) {
    return type === "manual_test" ? t("Test run") : type || "—";
  }

  // replay starts a new run from the shown one (or from one of its steps) and opens it.
  async replay(fromStepKey) {
    if (this.state.busy || !this.state.run) return;
    this.state.busy = true;
    this.state.error = "";
    try {
      const result = await replayWorkflowRun(this.state.run.id, fromStepKey);
      notifySuccess(t("Replay started as run #{id}.", { id: result.run_id }));
      this.props.onRunsChanged?.();
      if (result.run_id) await this.openRun(result.run_id);
    } catch (error) {
      this.state.error = reportWorkflowError(error, t("Failed to replay the run."));
    } finally {
      this.state.busy = false;
    }
  }

  // decide approves or rejects the run parked at a wait_approval step. A bad token (403) or a run
  // that is no longer waiting (409) shows as a message here, never as a logout.
  async decide(approve) {
    if (this.state.busy || !this.state.run) return;
    const runID = this.state.run.id;
    this.state.busy = true;
    this.state.approvalMessage = "";
    try {
      const result = await decideWorkflowApproval(runID, approve, { token: this.state.approvalToken.trim(), comment: this.state.approvalComment.trim() });
      if (!this.alive) return;
      // A run started by a trigger is handed back to the retry job after the decision, so it shows
      // waiting_retry until the job picks it up.
      if (result?.status === "waiting_retry") notifySuccess(t("Run #{id}: decision recorded, the run continues shortly.", { id: runID }));
      else notifySuccess(approve ? t("Run #{id} approved.", { id: runID }) : t("Run #{id} rejected.", { id: runID }));
      this.state.approvalToken = "";
      this.state.approvalComment = "";
      this.props.onRunsChanged?.();
      await this.openRun(runID);
    } catch (error) {
      if (!this.alive) return;
      const info = workflowErrorInfo(error);
      // 403 covers a bad token, self-approval and test runs; 409 covers an expired or decided wait
      // and a full concurrency limit. The server message tells them apart.
      if (info.permission) this.state.approvalMessage = info.message || t("The approval token is missing or wrong.");
      else if (info.status === 409 || info.code === RPC_CONFLICT) {
        this.state.approvalMessage = /concurrency limit/i.test(info.message || "")
          ? t("The workflow is at its concurrency limit. Try again shortly.")
          : t("The run is no longer waiting for approval, or the approval expired.");
      }
      else this.state.approvalMessage = info.message || t("Failed to record the decision.");
    } finally {
      this.state.busy = false;
    }
  }

  // cancel asks the engine to stop the shown run, then refreshes it.
  async cancel() {
    if (this.state.busy || !this.state.run) return;
    this.state.busy = true;
    this.state.error = "";
    try {
      await cancelWorkflowRun(this.state.run.id);
      notifySuccess(t("Cancellation requested for run #{id}.", { id: this.state.run.id }));
      await this.openRun(this.state.run.id);
    } catch (error) {
      this.state.error = reportWorkflowError(error, t("Failed to cancel the run."));
    } finally {
      this.state.busy = false;
    }
  }
}
