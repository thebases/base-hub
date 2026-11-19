import { Component, loadTemplate, onWillStart, useState, xml } from "nodexui/owl";

import { Button, Icon } from "nodexui/component";
import { authStore } from "@/stores/auth-service.js";
import { Chatter } from "./Chatter.js";
import { FollowerButton } from "./FollowerButton.js";
import { fetchFollowers, subscribeFollower, unsubscribeFollower } from "./chatter-rpc.js";
import { idFromMany2One } from "./chatter-format.js";

const template = await loadTemplate(new URL("./ChatterPanel.xml", import.meta.url));

function collapseStorageKey(modelName) {
  return `basehub.chatter.collapsed.${modelName}`;
}

// readChatterCollapsed/writeChatterCollapsed persist the panel's collapsed
// state per model so FormView (which owns the toggle button in its action
// bar) and ChatterPanel (which renders the collapsed/expanded chrome) share
// one source of truth instead of duplicating the localStorage key.
export function readChatterCollapsed(modelName) {
  try {
    return localStorage.getItem(collapseStorageKey(modelName)) === "1";
  } catch {
    // Storage unavailable (private mode, quota, etc.) — default to expanded.
    return false;
  }
}

export function writeChatterCollapsed(modelName, collapsed) {
  try {
    localStorage.setItem(collapseStorageKey(modelName), collapsed ? "1" : "0");
  } catch {
    // Best-effort only — the panel still works uncollapsed for this session.
  }
}

// ChatterPanel is the docked right-side container FormView mounts once a
// form's arch contains a <chatter/> marker node (see ArchNode.xml/FormView.js).
// Collapse/expand is controlled by FormView (its action-bar button sits next
// to Edit/Delete); this component just renders the collapsed rail or the
// expanded panel for the given state.
//
// Followers live here (not in Chatter.js) because the FollowerButton renders
// in this component's own header — Chatter is only handed a refresh callback
// (onFollowersChanged) to call after actions that change follower membership
// server-side (e.g. a "Send message" auto-subscribing its recipients).
export class ChatterPanel extends Component {
  static template = xml({ raw: [template] });
  static components = { Button, Icon, Chatter, FollowerButton };
  static props = {
    modelName: { type: String },
    recordId: { type: [Number, String], optional: true },
    collapsed: { type: Boolean },
    onToggle: { type: Function },
  };

  setup() {
    this.state = useState({ followers: [] });
    this.currentUserId = authStore.getState().uid ?? null;
    onWillStart(() => {
      if (this.hasRecord) return this.refreshFollowers();
    });
  }

  get hasRecord() {
    return this.props.recordId !== undefined && this.props.recordId !== null && this.props.recordId !== "";
  }

  get isFollowing() {
    return this.state.followers.some((follower) => idFromMany2One(follower.user_id) === this.currentUserId);
  }

  async refreshFollowers() {
    this.state.followers = await fetchFollowers(this.props.modelName, this.props.recordId);
  }

  async toggleFollow() {
    if (!this.currentUserId) return;
    if (this.isFollowing) {
      await unsubscribeFollower(this.props.modelName, this.props.recordId, this.currentUserId);
    } else {
      await subscribeFollower(this.props.modelName, this.props.recordId, this.currentUserId, []);
    }
    await this.refreshFollowers();
  }

  // addFollower/removeFollower let any base.users be added/removed as a
  // follower, not just the current viewer — both go through the backend's
  // subscribe/unsubscribe RuntimeMethods, same as toggleFollow.
  async addFollower(userId) {
    if (!userId) return;
    await subscribeFollower(this.props.modelName, this.props.recordId, userId, []);
    await this.refreshFollowers();
  }

  async removeFollower(userId) {
    if (!userId) return;
    await unsubscribeFollower(this.props.modelName, this.props.recordId, userId);
    await this.refreshFollowers();
  }
}
