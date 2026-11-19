// Reveals a newly generated base.users X-API-Key exactly once — the backend stores only its
// SHA-256 digest, so the admin must copy the plaintext before closing. Controlled via
// `props.state` ({open, subjectName, apiKey}) + `props.onOpenChange`; the RPC already ran before
// ModelView opens this dialog (see runArchButton's base.users branch), so "Done" is just a close.
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

const template = await loadTemplate(new URL("./UserApiKeyDialog.xml", import.meta.url));

export class UserApiKeyDialog extends Component {
  static template = xml({ raw: [template] });
  static components = { Button, Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle, Field, Icon, FieldLabel };

  copy(value) {
    if (navigator.clipboard?.writeText) void navigator.clipboard.writeText(value).catch(() => {});
  }

  done() {
    this.props.onOpenChange(false);
  }
}
