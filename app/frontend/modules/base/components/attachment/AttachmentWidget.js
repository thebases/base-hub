import { Component, loadTemplate, onWillStart, useRef, useState, xml } from "nodexui/owl";

import { authenticatedHeaders, buildBackendURL, createModelRpc } from "@/services/api/jsonv2.js";
import { NoContent } from "@/modules/base/components/NoContent.js";
import { Alert, Button, Icon } from "nodexui/component";

const template = await loadTemplate(new URL("./AttachmentWidget.xml", import.meta.url));

const SYS_ATTACHMENT_MODEL = "sys.attachment";

// attachmentDownloadURL builds the /web/content static-file link used by
// both the download anchor and (implicitly) any <img> preview.
function attachmentDownloadURL(id) {
  return buildBackendURL(`/web/content/${id}?download=1`);
}

// Generic attachment widget — upload dropzone + a list of attached files,
// backed by sys.attachment ("attachments_for_record" RPC + /web/content
// multipart upload), ported from attachment-widget.tsx. Multi-file mode
// appends; single-file mode replaces (and best-effort deletes the stale row).
export class AttachmentWidget extends Component {
  static template = xml({ raw: [template] });
  static components = { NoContent, Alert, Button, Icon };
  static props = {
    modelName: { type: String, optional: true },
    recordId: { type: [Number, String], optional: true },
    companyId: { type: Number, optional: true },
    userId: { type: Number, optional: true },
    groupId: { type: Number, optional: true },
    isPublic: { type: Boolean, optional: true },
    multi: { type: Boolean, optional: true },
    readonly: { type: Boolean, optional: true },
    className: { type: String, optional: true },
    onChange: { type: Function, optional: true },
  };
  static defaultProps = { multi: false, readonly: false, className: "" };

  setup() {
    this.state = useState({ attachments: [], uploading: false, dragging: false, error: "" });
    this.fileInput = useRef("fileInput");
    onWillStart(() => this.loadAttachments());
  }

  attachmentDownloadURL(id) {
    return attachmentDownloadURL(id);
  }

  async loadAttachments() {
    if (!this.props.modelName || !this.props.recordId) return;
    try {
      const response = await createModelRpc(SYS_ATTACHMENT_MODEL).call("attachments_for_record", [], {
        sys_model: this.props.modelName,
        model_id: this.props.recordId,
      });
      this.state.attachments = Array.isArray(response.result) ? response.result : [];
    } catch {
      // Keep the widget usable even if the initial list fails to load.
    }
  }

  async uploadFiles(files) {
    if (this.props.readonly || files.length === 0) return;
    const selected = this.props.multi ? files : files.slice(0, 1);
    this.state.uploading = true;
    this.state.error = "";
    try {
      const uploaded = [];
      for (const file of selected) {
        const form = new FormData();
        form.append("file", file);
        if (this.props.modelName && this.props.recordId) {
          form.append("sys_model", this.props.modelName);
          form.append("model_id", String(this.props.recordId));
        }
        if (this.props.companyId !== undefined) form.append("company_id", String(this.props.companyId));
        if (this.props.userId !== undefined) form.append("user_id", String(this.props.userId));
        if (this.props.groupId !== undefined) form.append("group_id", String(this.props.groupId));
        if (this.props.isPublic) form.append("is_public", "true");

        // authenticatedHeaders() sets Content-Type: application/json, which
        // must not ride along on a multipart body — the browser derives the
        // correct boundary-bearing Content-Type itself.
        const headers = authenticatedHeaders();
        delete headers["Content-Type"];

        const response = await fetch(buildBackendURL("/web/content"), { method: "POST", headers, body: form });
        if (!response.ok) throw new Error(t('Could not upload "{name}".', { name: file.name }));
        const payload = await response.json();
        uploaded.push(payload.result);
      }

      const previous = this.state.attachments;
      const next = this.props.multi ? [...previous, ...uploaded] : uploaded;
      this.state.attachments = next;
      this.props.onChange?.(next);

      if (!this.props.multi && previous.length > 0) {
        for (const stale of previous) {
          void this.deleteAttachmentByID(stale.id).catch(() => {});
        }
      }
    } catch (error) {
      this.state.error = error instanceof Error ? error.message : t("Could not upload file.");
    } finally {
      this.state.uploading = false;
    }
  }

  async deleteAttachmentByID(id) {
    const response = await createModelRpc(SYS_ATTACHMENT_MODEL).delete([id]);
    if (!response.result) throw new Error(t("Could not delete attachment {id}.", { id }));
  }

  async onDeleteClick(attachment) {
    try {
      await this.deleteAttachmentByID(attachment.id);
      const next = this.state.attachments.filter((item) => item.id !== attachment.id);
      this.state.attachments = next;
      this.props.onChange?.(next);
    } catch {
      this.state.error = t('Could not delete "{name}".', { name: attachment.name });
    }
  }

  openPicker() {
    if (this.props.readonly || this.state.uploading) return;
    this.fileInput.el?.click();
  }

  onInputChange(ev) {
    const files = Array.from(ev.target.files ?? []);
    ev.target.value = "";
    void this.uploadFiles(files);
  }

  onDragOver(ev) {
    ev.preventDefault();
    this.state.dragging = true;
  }

  onDragLeave() {
    this.state.dragging = false;
  }

  onDrop(ev) {
    ev.preventDefault();
    this.state.dragging = false;
    if (this.props.readonly) return;
    void this.uploadFiles(Array.from(ev.dataTransfer?.files ?? []));
  }

  dismissError() {
    this.state.error = "";
  }
}