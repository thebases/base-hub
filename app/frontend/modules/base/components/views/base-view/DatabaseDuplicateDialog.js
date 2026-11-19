// Port of frontend-admin's database-duplicate-dialog.tsx. Collects the
// target database name before ModelView invokes base.database.action_duplicate
// on a saved template/tenant record. Controlled via `props.state`
// ({open, sourceName, suggestedName}) + `props.onOpenChange`/`props.onConfirm`
// callbacks — same controlled-dialog contract as the other three
// views/base-view/*Dialog components (see frontend-context.md's Phase 6
// dialogs/x2many/PWA log entry for the shared pattern write-up).
import { Component, loadTemplate, onWillUpdateProps, useState, xml } from "nodexui/owl";
import { Alert, Button, Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle, Field, Input } from "nodexui/component";

const template = await loadTemplate(new URL("./DatabaseDuplicateDialog.xml", import.meta.url));

export class DatabaseDuplicateDialog extends Component {
  static template = xml({ raw: [template] });
  static components = { Alert, Button, Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle, Field, Input };

  setup() {
    this.state = useState({ name: "", error: "", submitting: false });
    if (this.props.state.open) this.seedFrom(this.props.state);
    // BaseUI's Dialog is now a controlled pure-Owl component (props.state.open
    // drives it directly), but the edit state below still needs seeding once
    // per open — mirrors React's `open` effect dependency — so watch for the
    // false -> true transition on incoming props.
    onWillUpdateProps((nextProps) => {
      if (nextProps.state.open && !this.props.state.open) this.seedFrom(nextProps.state);
    });
  }

  seedFrom(dialogState) {
    this.state.name = dialogState.suggestedName ?? "";
    this.state.error = "";
    this.state.submitting = false;
  }

  onNameInput(value) {
    this.state.name = value;
  }

  cancel() {
    this.props.onOpenChange(false);
  }

  async confirm() {
    const trimmed = this.state.name.trim();
    if (!trimmed) {
      this.state.error = t("Database name is required.");
      return;
    }
    this.state.submitting = true;
    this.state.error = "";
    try {
      await this.props.onConfirm(trimmed);
      this.props.onOpenChange(false);
    } catch (error) {
      this.state.error = error instanceof Error ? error.message : t("Failed to duplicate database");
    } finally {
      this.state.submitting = false;
    }
  }
}
