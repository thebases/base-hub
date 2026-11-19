import { Component, loadTemplate, xml } from "nodexui/owl";

import { ActivityItem } from "./ActivityItem.js";
import { ScheduleActivityDialog } from "./ScheduleActivityDialog.js";

const template = await loadTemplate(new URL("./ActivityList.xml", import.meta.url));

// ActivityList shows discuss.activity rows pinned above the message feed
// (chatter layout) and renders the "Schedule Activity"
// dialog — its own open/close toggle now lives on Chatter (state.scheduleOpen)
// since the trigger button moved into ChatterComposer's tab row.
export class ActivityList extends Component {
  static template = xml({ raw: [template] });
  static components = { ActivityItem, ScheduleActivityDialog };
  static props = {
    activities: { type: Array },
    modelName: { type: String },
    recordId: { type: [Number, String] },
    currentUserId: { type: [Number, String], optional: true },
    onChanged: { type: Function },
    scheduleOpen: { type: Boolean },
    onScheduleOpenChange: { type: Function },
  };
}
