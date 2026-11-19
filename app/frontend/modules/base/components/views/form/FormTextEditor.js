import { Component, markup, onWillUpdateProps, useState, xml } from "nodexui/owl";
import { TextEditor } from "nodexui/component";
import { sanitizeHtml } from "@/services/html-sanitize.js";

// textValue turns a record value into editor text: empty values arrive as null/false, never "false".
function textValue(value) {
  return typeof value === "string" ? value : "";
}

// FormTextEditor is the form's `text_editor` widget: a text field edited as HTML in BaseUI's
// TextEditor (ProseMirror/Milkdown), shown sanitized when the form or the field is read-only.
// TextEditor reloads whenever its `content` prop changes, so content only follows the record when
// the value changed from outside (reset, reload, onchange), never after the editor's own emission;
// props handed to TextEditor stay stable for the same reason (see knowledge MarkdownEdit.js).
export class FormTextEditor extends Component {
  static template = xml`
    <div class="basehub-form-text-editor" t-att-id="props.id">
      <div t-if="props.readonly" class="basehub-form-readonly-value basehub-form-rich-text" t-out="readonlyHtml"/>
      <TextEditor t-else="" content="state.html" placeholder="t('Write something…')" onChange="handleChange"/>
    </div>`;
  static components = { TextEditor };
  static props = { id: { type: String, optional: true }, value: { optional: true }, readonly: { type: Boolean, optional: true }, onChange: Function };

  setup() {
    this.emitted = textValue(this.props.value);
    this.state = useState({ html: this.emitted });
    onWillUpdateProps((next) => {
      const value = textValue(next.value);
      if (value !== this.emitted) {
        this.emitted = value;
        this.state.html = value;
      }
    });
    this.handleChange = (html) => {
      this.emitted = html;
      this.props.onChange(html);
    };
  }

  get readonlyHtml() {
    // Stored rich text is shown read-only through the shared allowlist sanitizer.
    const html = sanitizeHtml(textValue(this.props.value));
    return markup(html || "<span>—</span>");
  }
}
