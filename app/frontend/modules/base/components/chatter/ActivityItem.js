import { Component, loadTemplate, useState, xml } from "nodexui/owl";

import { Badge, Button, Icon, Textarea } from "nodexui/component";
import { activityState, activityStateLabel, formatChatterDate, labelFromMany2One } from "./chatter-format.js";
import { markActivityDone } from "./chatter-rpc.js";

const template = await loadTemplate(new URL("./ActivityItem.xml", import.meta.url));

// ActivityItem renders one discuss.activity row and its "Mark done" action —
// completion always goes through the backend's mark_done RuntimeMethod
// (posts the audit message + runs chaining), never a raw unlink.
export class ActivityItem extends Component {
  static template = xml({ raw: [template] });
  static components = { Badge, Button, Icon, Textarea };
  static props = {
    activity: { type: Object },
    onDone: { type: Function },
  };

  setup() {
    this.state = useState({ feedbackOpen: false, feedbackNote: "", submitting: false });
  }

  get derivedState() {
    return activityState(this.props.activity.date_deadline);
  }

  get stateLabel() {
    return activityStateLabel(this.derivedState);
  }

  get typeName() {
    return labelFromMany2One(this.props.activity.activity_type_id, t("Activity"));
  }

  get assigneeName() {
    return labelFromMany2One(this.props.activity.user_id, "");
  }

  get deadlineLabel() {
    return formatChatterDate(this.props.activity.date_deadline);
  }

  toggleFeedback() {
    this.state.feedbackOpen = !this.state.feedbackOpen;
  }

  async markDone() {
    if (this.state.submitting) return;
    this.state.submitting = true;
    try {
      await markActivityDone(this.props.activity.id, this.state.feedbackNote || null);
      await this.props.onDone();
    } finally {
      this.state.submitting = false;
    }
  }
}
