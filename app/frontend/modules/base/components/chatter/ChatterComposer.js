import { Component, loadTemplate, useState, xml } from "nodexui/owl";

import { Button, Icon, Textarea } from "nodexui/component";
import { searchCannedResponses, useCannedResponse } from "./chatter-rpc.js";
import { UserPicker } from "./UserPicker.js";

const template = await loadTemplate(new URL("./ChatterComposer.xml", import.meta.url));

// ChatterComposer is the textarea + "/" canned-response trigger + submit
// button. Canned-response search fires while the trimmed text starts with
// "/" (e.g. "/thanks"), matching discuss.canned.response's Source shortcut
// convention; picking a suggestion inserts its Substitution and bumps
// LastUsedAt through the backend's "use" RuntimeMethod.
// "Send message" (mode === "comment") requires at least one explicit
// receiver — a base.users pick via UserPicker — matching the backend's
// post_message invariant (runtimePostMessage rejects a non-note post with no
// recipient_ids); "Log note" has no receiver requirement, since a note only
// ever notifies existing followers.
export class ChatterComposer extends Component {
  static template = xml({ raw: [template] });
  static components = { Button, Icon, Textarea, UserPicker };
  static props = {
    mode: { type: String },
    onModeChange: { type: Function },
    onSubmit: { type: Function },
    onScheduleActivity: { type: Function },
  };

  setup() {
    this.state = useState({ text: "", suggestions: [], submitting: false, collapsed: true, recipients: [] });
  }

  get placeholder() {
    return this.props.mode === "note" ? t("Log an internal note…") : t("Send a message…");
  }

  get requiresRecipients() {
    return this.props.mode !== "note";
  }

  get canSubmit() {
    if (!this.state.text.trim() || this.state.submitting) return false;
    return !this.requiresRecipients || this.state.recipients.length > 0;
  }

  addRecipient(user) {
    if (this.state.recipients.some((recipient) => String(recipient.id) === String(user.id))) return;
    this.state.recipients = [...this.state.recipients, user];
  }

  removeRecipient(id) {
    this.state.recipients = this.state.recipients.filter((recipient) => String(recipient.id) !== String(id));
  }

  // Clicking the already-active tab collapses the composer body instead of
  // re-firing onModeChange for a no-op mode switch; clicking the other tab
  // always expands and switches mode.
  onTabClick(mode) {
    if (mode === this.props.mode) {
      this.state.collapsed = !this.state.collapsed;
      return;
    }
    this.state.collapsed = false;
    this.props.onModeChange(mode);
  }

  async onInput(text) {
    this.state.text = text;
    const trimmed = text.trim();
    if (!trimmed.startsWith("/")) {
      this.state.suggestions = [];
      return;
    }
    this.state.suggestions = await searchCannedResponses(trimmed.slice(1));
  }

  async pickSuggestion(suggestion) {
    this.state.text = suggestion.substitution;
    this.state.suggestions = [];
    try {
      await useCannedResponse(suggestion.id);
    } catch {
      // Best-effort — a failed LastUsedAt bump must not block composing.
    }
  }

  async submit() {
    const text = this.state.text.trim();
    if (!this.canSubmit) return;
    this.state.submitting = true;
    try {
      await this.props.onSubmit(text, this.state.recipients.map((recipient) => recipient.id));
      this.state.text = "";
      this.state.suggestions = [];
      this.state.recipients = [];
    } finally {
      this.state.submitting = false;
    }
  }
}
