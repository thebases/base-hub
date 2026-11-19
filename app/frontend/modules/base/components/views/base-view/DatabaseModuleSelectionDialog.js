// Port of frontend-admin's database-module-selection-dialog.tsx. Lets an
// admin check/uncheck modules from the scanned catalog before Re-init:
// checked modules are installed (and their master data replayed), unchecked
// modules that were previously enabled are uninstalled — one combined flow
// behind base.database.action_reinit's `modules` kwarg.
// Controlled via `props.state` ({open, databaseName, availableModules,
// enabledModules, loadingModules}) + `props.onOpenChange`/`props.onConfirm`,
// same contract as DatabaseDuplicateDialog.js.
import { Component, loadTemplate, onWillUpdateProps, useState, xml } from "nodexui/owl";
import { Alert, Button, Checkbox, Dialog, DialogContent, DialogFooter, DialogHeader } from "nodexui/component";

const template = await loadTemplate(new URL("./DatabaseModuleSelectionDialog.xml", import.meta.url));

export class DatabaseModuleSelectionDialog extends Component {
  static template = xml({ raw: [template] });
  static components = { Alert, Button, Checkbox, Dialog, DialogContent, DialogFooter, DialogHeader };

  setup() {
    this.state = useState({ selected: {}, error: "", submitting: false });
    if (this.props.state.open) this.seedFrom(this.props.state);
    // BaseUI's Dialog is now a controlled pure-Owl component (props.state.open
    // drives it directly), but the selection state below still needs seeding
    // once per open — mirrors React's `open` effect dependency — so watch for
    // the false -> true transition on incoming props.
    onWillUpdateProps((nextProps) => {
      if (nextProps.state.open && !this.props.state.open) this.seedFrom(nextProps.state);
    });
  }

  seedFrom(dialogState) {
    const enabled = dialogState.enabledModules ?? [];
    this.state.selected = Object.fromEntries(enabled.map((name) => [name, true]));
    this.state.error = "";
    this.state.submitting = false;
  }

  isSelected(name) {
    return Boolean(this.state.selected[name]);
  }

  toggleModule(name, checked) {
    // Replace the whole map rather than mutate one key in place — writing to
    // a previously-absent useState key is a silent no-op on first write in
    // this Owl runtime (same reactivity gotcha documented for KanbanView's
    // fold map).
    this.state.selected = { ...this.state.selected, [name]: checked };
  }

  cancel() {
    this.props.onOpenChange(false);
  }

  async confirm() {
    const selectedNames = Object.entries(this.state.selected)
      .filter(([, checked]) => checked)
      .map(([name]) => name);
    if (selectedNames.length === 0) {
      this.state.error = t("Select at least one module.");
      return;
    }
    this.state.submitting = true;
    this.state.error = "";
    try {
      await this.props.onConfirm(selectedNames);
      this.props.onOpenChange(false);
    } catch (error) {
      this.state.error = error instanceof Error ? error.message : t("Failed to reinit database");
    } finally {
      this.state.submitting = false;
    }
  }
}
