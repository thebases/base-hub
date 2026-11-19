import { Component, loadTemplate, onWillStart, useState, xml } from "nodexui/owl";

import { createModelRpc } from "@/services/api/jsonv2.js";
import { Alert, Button, Checkbox, Icon, Input } from "nodexui/component";

const template = await loadTemplate(new URL("./ModelAccessTable.xml", import.meta.url));

const SYS_MODEL_ACCESS_MODEL = "sys.model.access";
const PERM_FIELDS = ["perm_read", "perm_write", "perm_create", "perm_unlink"];

// ModelAccessTable is an inline, self-persisted editor for a group's
// sys.model.access rows (RBAC grants). It bypasses FormView's save pipeline
// entirely — the same "own CRUD, notify via onChange" shape as
// AttachmentWidget — because the parent group form has no [0,0,vals]/
// [1,id,vals] one2many command support to submit new/edited access rows
// through an ordinary write (see the group-permissions-tab plan).
export class ModelAccessTable extends Component {
  static template = xml({ raw: [template] });
  static components = { Alert, Button, Checkbox, Icon, Input };
  static props = {
    groupId: { type: [Number, String], optional: true },
    readonly: { type: Boolean, optional: true },
    className: { type: String, optional: true },
  };
  static defaultProps = { readonly: false, className: "" };

  setup() {
    this.state = useState({ rows: [], draftModelName: "", loading: false, savingID: null, error: "" });
    onWillStart(() => this.loadRows());
  }

  async loadRows() {
    if (!this.props.groupId) return;
    this.state.loading = true;
    try {
      const response = await createModelRpc(SYS_MODEL_ACCESS_MODEL).search(
        [["group_id", "=", this.props.groupId]],
        ["id", "model_name", "perm_read", "perm_write", "perm_create", "perm_unlink"],
        "model_name ASC"
      );
      this.state.rows = Array.isArray(response.result) ? response.result : [];
    } catch {
      // Keep the widget usable even if the initial list fails to load.
    } finally {
      this.state.loading = false;
    }
  }

  get permFields() {
    return PERM_FIELDS;
  }

  permLabel(permField) {
    return { perm_read: t("Read"), perm_write: t("Write"), perm_create: t("Create"), perm_unlink: t("Delete") }[permField];
  }

  onDraftModelNameInput(value) {
    this.state.draftModelName = value;
  }

  async addRow() {
    if (this.props.readonly) return;
    const modelName = this.state.draftModelName.trim();
    if (!modelName) return;
    this.state.error = "";
    try {
      const vals = {
        group_id: this.props.groupId,
        model_name: modelName,
        name: `${modelName} access`,
        perm_read: false,
        perm_write: false,
        perm_create: false,
        perm_unlink: false,
      };
      const response = await createModelRpc(SYS_MODEL_ACCESS_MODEL).create(vals);
      const created = response.result;
      if (!created?.id) throw new Error(t("Create returned no id"));
      this.state.rows = [...this.state.rows, { id: created.id, ...vals }];
      this.state.draftModelName = "";
    } catch (error) {
      this.state.error = error instanceof Error ? error.message : t('Could not add permission for "{name}".', { name: modelName });
    }
  }

  async togglePerm(row, permField) {
    if (this.props.readonly) return;
    const nextValue = !row[permField];
    this.state.savingID = row.id;
    this.state.error = "";
    try {
      await createModelRpc(SYS_MODEL_ACCESS_MODEL).write([row.id], { [permField]: nextValue });
      row[permField] = nextValue;
    } catch {
      this.state.error = t('Could not update "{permission}" for "{name}".', { permission: this.permLabel(permField), name: row.model_name });
    } finally {
      this.state.savingID = null;
    }
  }

  async removeRow(row) {
    if (this.props.readonly) return;
    try {
      await createModelRpc(SYS_MODEL_ACCESS_MODEL).delete([row.id]);
      this.state.rows = this.state.rows.filter((item) => item.id !== row.id);
    } catch {
      this.state.error = t('Could not remove permission for "{name}".', { name: row.model_name });
    }
  }

  dismissError() {
    this.state.error = "";
  }
}
