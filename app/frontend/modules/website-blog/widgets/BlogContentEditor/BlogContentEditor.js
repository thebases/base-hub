// Form widget `<widget name="blog_content_editor" field="content"/>` for tbl.website_blog_post.
// A small contenteditable WYSIWYG (document.execCommand toolbar) with an HTML source toggle. It owns
// no storage: every edit is committed to the record through ctx.onFieldChange(field, html). The
// server sanitizes on save (SanitizeBlogHTML); cleanHtml below only protects this admin page from
// markup that reached the column some other way (import, SQL).
// Arch attributes: `field` (default content).
import { Component, loadTemplate, onMounted, useRef, useState, xml } from "nodexui/owl";
import { isSafeUrl, sanitizeHtml } from "@/services/html-sanitize.js";

const template = await loadTemplate(new URL("./BlogContentEditor.xml", import.meta.url));

// cleanHtml removes active content from an HTML string and returns the cleaned markup.
function cleanHtml(html) {
  return sanitizeHtml(html);
}

const BUTTONS = [
  { id: "bold", label: "B", title: "Bold", cmd: "bold" },
  { id: "italic", label: "I", title: "Italic", cmd: "italic" },
  { id: "underline", label: "U", title: "Underline", cmd: "underline" },
  { id: "h2", label: "H2", title: "Heading 2", cmd: "formatBlock", arg: "h2" },
  { id: "h3", label: "H3", title: "Heading 3", cmd: "formatBlock", arg: "h3" },
  { id: "p", label: "¶", title: "Paragraph", cmd: "formatBlock", arg: "p" },
  { id: "ul", label: "• List", title: "Bulleted list", cmd: "insertUnorderedList" },
  { id: "ol", label: "1. List", title: "Numbered list", cmd: "insertOrderedList" },
  { id: "quote", label: "Quote", title: "Block quote", cmd: "formatBlock", arg: "blockquote" },
  { id: "link", label: "Link", title: "Insert link", prompt: "Link URL (https://...)", cmd: "createLink" },
  { id: "image", label: "Image", title: "Insert image", prompt: "Image URL (https://...)", cmd: "insertImage" },
  { id: "clear", label: "Clear", title: "Remove formatting", cmd: "removeFormat" },
];

export class BlogContentEditor extends Component {
  static template = xml({ raw: [template] });
  static props = {
    ctx: { type: Object, optional: true },
    node: { type: Object, optional: true },
    record: { type: Object, optional: true },
    readonly: { type: Boolean, optional: true },
  };

  setup() {
    this.buttons = BUTTONS;
    this.editor = useRef("editor");
    this.state = useState({ source: false, html: "" });
    // The last value this widget emitted; a record value equal to it is our own echo and must not
    // reset the caret by rewriting innerHTML.
    this.lastEmitted = null;
    onMounted(() => this.syncFromRecord());
  }

  get fieldName() {
    return this.props.node?.attrs?.field || "content";
  }

  get isReadonly() {
    return Boolean(this.props.readonly);
  }

  get recordHtml() {
    const value = this.props.record?.[this.fieldName];
    return typeof value === "string" ? value : "";
  }

  syncFromRecord() {
    const el = this.editor.el;
    if (!el || this.recordHtml === this.lastEmitted) return;
    el.innerHTML = cleanHtml(this.recordHtml);
  }

  emit() {
    const el = this.editor.el;
    if (!el || this.isReadonly) return;
    const html = el.innerHTML;
    this.lastEmitted = html;
    this.props.ctx?.onFieldChange?.(this.fieldName, html);
  }

  run(btn) {
    if (this.isReadonly || this.state.source) return;
    let arg = btn.arg;
    if (btn.prompt) {
      arg = window.prompt(btn.prompt, "https://");
      if (!arg?.trim() || !isSafeUrl(arg)) return;
    }
    this.editor.el?.focus();
    document.execCommand(btn.cmd, false, arg);
    this.emit();
  }

  toggleSource() {
    if (this.state.source) {
      // Back to visual: the textarea content becomes the document (cleaned).
      this.state.source = false;
      requestAnimationFrame(() => {
        if (this.editor.el) this.editor.el.innerHTML = cleanHtml(this.state.html);
        this.emit();
      });
      return;
    }
    this.state.html = this.editor.el?.innerHTML ?? this.recordHtml;
    this.state.source = true;
  }

  onSourceInput(ev) {
    this.state.html = ev.target.value;
    this.lastEmitted = ev.target.value;
    this.props.ctx?.onFieldChange?.(this.fieldName, ev.target.value);
  }
}
