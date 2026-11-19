import { Component, loadTemplate, onWillDestroy, onWillStart, useState, xml } from "nodexui/owl";

import { Button, Icon } from "nodexui/component";
import { authStore } from "@/stores/auth-service.js";
import { busService } from "@/modules/base-bus/index.js";
import { ChatterComposer } from "./ChatterComposer.js";
import { MessageFeed } from "./MessageFeed.js";
import { ActivityList } from "./ActivityList.js";
import { fetchActivities, fetchMessages, findInternalNoteSubtypeId, postMessage } from "./chatter-rpc.js";

const template = await loadTemplate(new URL("./Chatter.xml", import.meta.url));

// Chatter owns the composer-mode toggle and the two RPC-backed lists
// (messages/activities), plus realtime refresh — matching plan §3.3 option
// (a): the viewer's own base-bus "discuss:user:<id>" channel carries live
// notification_created events; anything for another user on the same record
// only refreshes on tab-focus (see onWindowFocus). Followers live in the
// parent ChatterPanel (its header renders FollowerButton) — this component
// only calls props.onFollowersChanged() when an action here changes follower
// membership server-side.
export class Chatter extends Component {
  static template = xml({ raw: [template] });
  static components = { ChatterComposer, MessageFeed, ActivityList, Button, Icon };
  static props = {
    modelName: { type: String },
    recordId: { type: [Number, String] },
    onFollowersChanged: { type: Function, optional: true },
  };

  setup() {
    this.state = useState({
      mode: "comment",
      messages: [],
      activities: [],
      loading: true,
      scheduleOpen: false,
    });
    this.currentUserId = authStore.getState().uid ?? null;
    this.onNotification = (payload) => this.onBusNotification(payload);
    this.onWindowFocus = () => void this.refreshAll();

    onWillStart(() => this.refreshAll());
    if (this.currentUserId) {
      busService.addChannel(`discuss:user:${this.currentUserId}`);
      busService.subscribe("notification_created", this.onNotification);
    }
    window.addEventListener("focus", this.onWindowFocus);
    onWillDestroy(() => {
      if (this.currentUserId) {
        busService.unsubscribe("notification_created", this.onNotification);
        busService.deleteChannel(`discuss:user:${this.currentUserId}`);
      }
      window.removeEventListener("focus", this.onWindowFocus);
    });
  }

  onBusNotification(payload) {
    if (payload?.res_model !== this.props.modelName || String(payload?.res_id) !== String(this.props.recordId)) return;
    void this.refreshMessages();
  }

  async refreshAll() {
    this.state.loading = true;
    try {
      await Promise.all([this.refreshMessages(), this.refreshActivities()]);
    } finally {
      this.state.loading = false;
    }
  }

  async refreshMessages() {
    this.state.messages = await fetchMessages(this.props.modelName, this.props.recordId);
  }

  async refreshActivities() {
    this.state.activities = await fetchActivities(this.props.modelName, this.props.recordId);
  }

  setMode(mode) {
    this.state.mode = mode;
  }

  openSchedule() {
    this.state.scheduleOpen = true;
  }

  setScheduleOpen(open) {
    this.state.scheduleOpen = open;
  }

  async onComposerSubmit(body, recipientUserIds) {
    if (!body?.trim()) return;
    const isNote = this.state.mode === "note";
    // "Send message" requires at least one receiver — ChatterComposer already
    // blocks submit without one, this is the same invariant the backend's
    // post_message RuntimeMethod enforces server-side.
    if (!isNote && (!recipientUserIds || recipientUserIds.length === 0)) return;
    const subtypeId = isNote ? await findInternalNoteSubtypeId(this.props.modelName) : null;
    await postMessage(this.props.modelName, this.props.recordId, {
      messageType: "comment",
      subtypeId,
      body,
      recipientUserIds: isNote ? undefined : recipientUserIds,
      isNote,
    });
    // A non-note send auto-subscribes its recipients server-side (see
    // discuss/models/service.go subscribeRecipients), so the follower count
    // (owned by the parent ChatterPanel, whose header renders FollowerButton)
    // must refresh alongside the message feed.
    await Promise.all([this.refreshMessages(), this.props.onFollowersChanged?.()]);
  }

  async onActivityChanged() {
    await Promise.all([this.refreshActivities(), this.refreshMessages()]);
  }
}
