import { Plugin } from "../core/plugin_manager.js";
import { isSafeUrl, sanitizePasteHtml } from "../core/html_sanitize.js";

// Inline text editing. Text elements of the selected block become contenteditable (editor-only
// attribute, stripped on save). Typing is committed to history as one step per pause or blur; toolbar
// commands and paste run as operations. IME safe: nothing is committed while a composition is active.
const EDITABLE = "h1,h2,h3,h4,h5,h6,p,li,blockquote,figcaption,td,th,span,a,small,strong,em";
const COMMIT_DELAY = 700;

export class TextPlugin extends Plugin {
  static id = "text";
  static dependencies = ["canvas", "selection"];

  start() {
    this.before = null;
    this.timer = null;
    this.composing = false;
    this.ctx.on("canvas-ready", () => this.attach());
    this.ctx.on("selection-changed", (block) => this.enable(block));
  }

  attach() {
    const { doc, root, history } = this.plugins.canvas;
    history.onBeforeChange(() => this.flush(true));
    // Native undo inside contenteditable bypasses History; route it through the history instead.
    doc.addEventListener("keydown", (ev) => {
      if (!(ev.ctrlKey || ev.metaKey) || ev.altKey || ev.key.toLowerCase() !== "z") return;
      ev.preventDefault();
      if (ev.shiftKey) history.redo();
      else history.undo();
    });
    const inEditable = (el) => el?.closest?.("[contenteditable=true]") && root.contains(el);
    doc.addEventListener("focusin", (ev) => {
      if (inEditable(ev.target) && this.before === null) this.before = this.plugins.canvas.history.root.get();
    });
    doc.addEventListener("input", (ev) => {
      if (!inEditable(ev.target)) return;
      if (this.before === null) this.before = this.plugins.canvas.history.root.get();
      this.schedule();
    });
    doc.addEventListener("compositionstart", () => { this.composing = true; });
    doc.addEventListener("compositionend", () => { this.composing = false; this.schedule(); });
    doc.addEventListener("focusout", (ev) => { if (inEditable(ev.target)) this.flush(); });
    doc.addEventListener("paste", (ev) => {
      if (!inEditable(ev.target)) return;
      ev.preventDefault();
      const html = ev.clipboardData.getData("text/html");
      const text = ev.clipboardData.getData("text/plain");
      this.run("paste", () => {
        if (html) doc.execCommand("insertHTML", false, sanitizePasteHtml(html));
        else doc.execCommand("insertText", false, text);
      });
    });
  }

  enable(block) {
    this.flush();
    const { root } = this.plugins.canvas;
    root.querySelectorAll("[contenteditable]").forEach((el) => el.removeAttribute("contenteditable"));
    // A selected text element (header/footer scope selects the clicked element itself) is editable too.
    if (block?.matches(EDITABLE)) block.setAttribute("contenteditable", "true");
    block?.querySelectorAll(EDITABLE).forEach((el) => el.setAttribute("contenteditable", "true"));
  }

  schedule() {
    clearTimeout(this.timer);
    this.timer = setTimeout(() => this.flush(), COMMIT_DELAY);
  }

  // Commits pending typing as one undo step; deferred while an IME composition is open unless forced
  // (an operation or undo is about to run and must see the typed text).
  flush(force = false) {
    clearTimeout(this.timer);
    if (this.before === null || (this.composing && !force)) return;
    if (this.plugins.canvas.history.busy) return;
    this.plugins.canvas.history.commit("edit text", this.before);
    this.before = null;
  }

  run(label, fn) {
    this.flush();
    return this.plugins.canvas.history.run(label, fn);
  }

  format(command) {
    const { doc } = this.plugins.canvas;
    return this.run(command, () => doc.execCommand(command, false, null));
  }

  // Saves the current selection so a toolbar input can take focus and the link still lands on it.
  saveRange() {
    const sel = this.plugins.canvas.doc.getSelection();
    this.range = sel && sel.rangeCount ? sel.getRangeAt(0).cloneRange() : null;
    return !!this.range && !this.range.collapsed;
  }

  applyLink(url) {
    const { doc } = this.plugins.canvas;
    const value = String(url || "").trim();
    if (value && !isSafeUrl(value)) throw new Error("Unsupported link address");
    return this.run("link", () => {
      const sel = doc.getSelection();
      if (this.range) {
        sel.removeAllRanges();
        sel.addRange(this.range);
      }
      if (value) doc.execCommand("createLink", false, value);
      else doc.execCommand("unlink", false, null);
    });
  }
}
