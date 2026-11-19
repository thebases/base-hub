import { Component, loadTemplate, onWillStart, onWillUpdateProps, useState, xml } from "nodexui/owl";

import { Button, Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle, Input, Textarea } from "nodexui/component";
import { fetchActivityTypes, scheduleActivity } from "./chatter-rpc.js";

const template = await loadTemplate(new URL("./ScheduleActivityDialog.xml", import.meta.url));

function todayISODate() {
  return new Date().toISOString().slice(0, 10);
}

// ScheduleActivityDialog is a thin form over discuss.activity's
// "schedule_activity" RuntimeMethod — never a raw create, so the
// res_model/res_id-required-together invariant stays server-side.
export class ScheduleActivityDialog extends Component {
  static template = xml({ raw: [template] });
  static components = { Button, Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle, Input, Textarea };
  static props = {
    open: { type: Boolean },
    onOpenChange: { type: Function },
    modelName: { type: String },
    recordId: { type: [Number, String] },
    currentUserId: { type: [Number, String], optional: true },
    onScheduled: { type: Function },
  };

  setup() {
    this.state = useState({
      activityTypes: [],
      activityTypeId: null,
      summary: "",
      note: "",
      dateDeadline: todayISODate(),
      submitting: false,
      error: "",
    });
    onWillStart(() => this.loadActivityTypes());
    onWillUpdateProps((nextProps) => {
      if (nextProps.open && !this.props.open) this.resetForm();
    });
  }

  async loadActivityTypes() {
    this.state.activityTypes = await fetchActivityTypes();
    if (!this.state.activityTypeId && this.state.activityTypes.length > 0) {
      this.state.activityTypeId = this.state.activityTypes[0].id;
    }
  }

  resetForm() {
    this.state.summary = "";
    this.state.note = "";
    this.state.dateDeadline = todayISODate();
    this.state.error = "";
    if (this.state.activityTypes.length > 0) this.state.activityTypeId = this.state.activityTypes[0].id;
  }

  onActivityTypeChange(ev) {
    this.state.activityTypeId = Number(ev.target.value);
  }

  async submit() {
    if (!this.state.activityTypeId) {
      this.state.error = t("Select an activity type.");
      return;
    }
    this.state.submitting = true;
    this.state.error = "";
    try {
      await scheduleActivity({
        modelName: this.props.modelName,
        recordId: this.props.recordId,
        activityTypeId: this.state.activityTypeId,
        userId: this.props.currentUserId,
        summary: this.state.summary || null,
        note: this.state.note || null,
        dateDeadline: this.state.dateDeadline,
      });
      this.props.onOpenChange(false);
      await this.props.onScheduled();
    } catch (error) {
      this.state.error = error instanceof Error ? error.message : t("Could not schedule activity.");
    } finally {
      this.state.submitting = false;
    }
  }
}
