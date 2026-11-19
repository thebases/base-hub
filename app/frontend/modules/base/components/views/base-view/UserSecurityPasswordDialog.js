// Port of frontend-admin's user-security-password-dialog.tsx. Collects the
// next password before ModelView invokes base.users' action_set_password
// object method for a saved record. Controlled via `props.state`
// ({open, userName}) + `props.onOpenChange`/`props.onConfirm`, same contract
// as DatabaseDuplicateDialog.js.
import { Component, loadTemplate, onWillUpdateProps, useState, xml } from "nodexui/owl";
import { Alert, Button, Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle, Field, Input, FieldLabel } from "nodexui/component";

const template = await loadTemplate(new URL("./UserSecurityPasswordDialog.xml", import.meta.url));

export class UserSecurityPasswordDialog extends Component {
  static template = xml({ raw: [template] });
  static components = { Alert, Button, Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle, Field, Input, FieldLabel };

  setup() {
    this.state = useState({ password: "", error: "", submitting: false, showPassword: false });
    // Seed local edit state exactly once per open (mirrors React's `open`
    // effect dependency) since Dialog is now a controlled `open` prop rather
    // than an imperative ref.
    onWillUpdateProps((nextProps) => {
      if (nextProps.state.open && !this.props.state.open) {
        this.state.password = "";
        this.state.error = "";
        this.state.submitting = false;
        this.state.showPassword = false;
      }
    });
  }

  onOpenChange(open) {
    if (!open) this.props.onOpenChange(false);
  }

  onPasswordInput(value) {
    this.state.password = value;
  }

  togglePasswordVisibility() {
    this.state.showPassword = !this.state.showPassword;
  }

  cancel() {
    this.props.onOpenChange(false);
  }

  async confirm() {
    const trimmed = this.state.password.trim();
    if (!trimmed) {
      this.state.error = t("New password is required.");
      return;
    }
    this.state.submitting = true;
    this.state.error = "";
    try {
      await this.props.onConfirm(trimmed);
      this.props.onOpenChange(false);
    } catch (error) {
      this.state.error = error instanceof Error ? error.message : t("Failed to update password");
    } finally {
      this.state.submitting = false;
    }
  }
}
