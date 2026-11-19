// Port of frontend-admin's user-security-oauth-client-dialog.tsx. Reveals a
// newly generated sys.oauth_client secret exactly once — the backend stores
// only the bcrypt hash, so admins must copy the plaintext from this dialog
// before closing it. No onConfirm callback: the RPC already ran before
// ModelView opens this dialog (see runArchButton's sys.oauth_client branch),
// so "Done" is just a close. Controlled via `props.state`
// ({open, subjectName, result}) + `props.onOpenChange`.
import { Component, loadTemplate, xml } from "nodexui/owl";
import {
  Button,
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  Field,
  Icon,
  FieldLabel,
} from "nodexui/component";

const template = await loadTemplate(new URL("./OauthClientSecretDialog.xml", import.meta.url));

export class OauthClientSecretDialog extends Component {
  static template = xml({ raw: [template] });
  static components = { Button, Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle, Field, Icon, FieldLabel };

  get result() {
    return this.props.state.result;
  }

  copy(value) {
    if (navigator.clipboard?.writeText) void navigator.clipboard.writeText(value).catch(() => {});
  }

  done() {
    this.props.onOpenChange(false);
  }
}
