// Simplified substitute for frontend-admin's MDXEditor-based markdown-edit.tsx
// (see .agents/planning/2026-08-14-frontend-knowledge-module-phase4-plan.md).
// Upgraded from a plain <textarea> to BaseUI's TextEditor (ProseMirror/Milkdown
// WYSIWYG) per 2026-09-02 request. TextEditor speaks HTML, but this component's
// contract with MarkdownPage.js (`props.value`/`props.onChange`) must stay
// markdown so MarkdownViewer/extractMarkdownHeadings and the .md file storage
// keep working unchanged — so every edit here is converted back to markdown
// before reaching the parent.
//
// Upgraded to @thebase/ui 0.0.10's TextEditor with collaborative editing
// (Yjs + y-webrtc) per 2026-09-02 request: passing `path` derives a stable
// per-document room so any two sessions with the same markdown file open
// share one live document; `signaling` is left unset so y-webrtc falls back
// to its own public default servers (no self-hosted signaling server exists
// in this repo yet).
//
// `onChange`/`user` are bound/computed once in setup() instead of as inline
// arrows or per-render getters (fixed 2026-09-02): TextEditor's own
// onMounted/onPatched both call its async mountEditor() with no re-entrancy
// guard, so any prop that changes *reference* every render (a fresh arrow
// function, a fresh `{ name, color }` object) causes Owl to re-render and
// patch TextEditor on every keystroke — each patch re-enters mountEditor()
// before the previous async mount (dynamic-imports yjs/y-webrtc/
// @milkdown/plugin-collab over the network) has finished, starting a second
// concurrent Crepe/Yjs instance against the same room and crashing with
// "Context 'editorState' not found" once the first instance's ctx is torn
// down mid-flight. Non-collaborative mode never hit this because its
// mountTextEditorRuntime() resolves fast enough to always beat the next
// patch; collaborative mode's extra network round trips widen the race
// window enough to hit it reliably. Keeping every TextEditor prop
// referentially stable across renders (content/placeholder/collaborative/
// room already are — string/boolean primitives compare by value) makes Owl
// skip re-rendering TextEditor entirely while typing, closing the race.

import { Component, loadTemplate, onWillStart, onWillUpdateProps, useState, xml } from "nodexui/owl";
import { TextEditor } from "nodexui/component";
import { loadMarked, loadTurndown } from "@/services/script-loader.js";
import { authStore } from "@/stores/auth-service.js";

const template = await loadTemplate(new URL("./MarkdownEdit.xml", import.meta.url));

let turndownService;

// Renders markdown source to the HTML TextEditor expects to load.
function markdownToHtml(markdown) {
  return window.marked ? window.marked.parse(markdown ?? "", { gfm: true, breaks: false }) : (markdown ?? "");
}

// vendor/turndown.js is bare Turndown core: TABLE/TR/TD/TH have no dedicated
// rule, so they fall through to the generic block-element default (each cell
// just gets wrapped in blank lines with no `|` delimiters), silently
// dropping table structure on every HTML->markdown conversion. There is no
// turndown-plugin-gfm vendored in this repo, so these GFM table rules are
// hand-registered here (same algorithm as that plugin) instead of pulling in
// a new vendor file.
function isHeadingRow(tr) {
  const parent = tr.parentNode;
  return (
    parent.nodeName === "THEAD" ||
    (parent.firstChild === tr &&
      (parent.nodeName === "TABLE" || isFirstTbody(parent)) &&
      Array.prototype.every.call(tr.childNodes, (n) => n.nodeName === "TH"))
  );
}

function isFirstTbody(node) {
  const previousSibling = node.previousSibling;
  return (
    node.nodeName === "TBODY" &&
    (!previousSibling || (previousSibling.nodeName === "THEAD" && /^\s*$/i.test(previousSibling.textContent)))
  );
}

function tableShouldBeSkipped(table) {
  const rows = table.rows;
  if (!rows || rows.length === 0) return true;
  return Array.prototype.every.call(rows, (row) => !row.childNodes || row.childNodes.length === 1);
}

function configureTurndownService(service) {
  service.addRule("tableCell", {
    filter: ["th", "td"],
    replacement: (content, node) => {
      const index = Array.prototype.indexOf.call(node.parentNode.childNodes, node);
      const prefix = index === 0 ? "| " : " ";
      const text = content.trim().replace(/\n+/g, " ").replace(/\|/g, "\\|");
      return `${prefix}${text} |`;
    },
  });

  service.addRule("tableRow", {
    filter: "tr",
    replacement: (content, node) => {
      let border = "";
      if (isHeadingRow(node)) {
        const cellCount = node.childNodes.length;
        border = "\n|" + Array(cellCount).fill(" --- |").join("");
      }
      return "\n" + content + border;
    },
  });

  service.addRule("table", {
    filter: (n) => n.nodeName === "TABLE" && !tableShouldBeSkipped(n),
    replacement: (content) => "\n\n" + content.replace(/\n+/g, "\n") + "\n\n",
  });

  service.addRule("tableSection", {
    filter: ["thead", "tbody", "tfoot"],
    replacement: (content) => content,
  });
}

// Converts TextEditor's HTML output back to the markdown source this app saves.
function htmlToMarkdown(html) {
  if (!turndownService) {
    turndownService = new window.TurndownService({ headingStyle: "atx", codeBlockStyle: "fenced" });
    configureTurndownService(turndownService);
  }
  return turndownService.turndown(html ?? "");
}

// Picks a stable, readable cursor color for a collaborator from their
// username, so the same person always shows up in the same color across
// sessions without needing a server-assigned palette.
function colorForUsername(username) {
  let hash = 0;
  for (const char of username || "anonymous") {
    hash = (hash * 31 + char.charCodeAt(0)) >>> 0;
  }
  return `hsl(${hash % 360}, 70%, 45%)`;
}

export class MarkdownEdit extends Component {
  static template = xml({ raw: [template] });
  static components = { TextEditor };

  setup() {
    // Remembers the markdown we last emitted so TextEditor only reloads
    // (and loses cursor position/undo history) on a genuinely external
    // change (e.g. switching documents), not on the echo of its own edit.
    this.emittedMarkdown = this.props.value ?? "";
    this.state = useState({ html: "" });
    // marked/turndown are loaded on demand (not by index.html); both must exist before the editor mounts.
    onWillStart(async () => {
      await Promise.all([loadMarked(), loadTurndown()]).catch(() => {});
      this.state.html = markdownToHtml(this.props.value);
    });
    onWillUpdateProps((nextProps) => {
      const nextValue = nextProps.value ?? "";
      if (nextValue !== this.emittedMarkdown) {
        this.emittedMarkdown = nextValue;
        this.state.html = markdownToHtml(nextValue);
      }
    });

    // Both bound once and reused for the component's lifetime, not
    // recreated per render — see the header comment on why a fresh
    // reference on every render is what triggers TextEditor's mount race.
    this.handleTextEditorChange = (html) => this.onChange(html);
    const username = authStore.state.username || t("Anonymous");
    this.collaborationUser = { name: username, color: colorForUsername(username) };
  }

  onChange(html) {
    const markdown = htmlToMarkdown(html);
    this.emittedMarkdown = markdown;
    this.props.onChange(markdown);
  }

  // Collaboration is only meaningful once a document is actually identified
  // by its path; without one there's no stable room to share with other
  // sessions, so TextEditor falls back to single-user mode.
  get collaborative() {
    return Boolean(this.props.path);
  }

  get room() {
    return this.props.path ? `knowledge/markdown/${this.props.path}` : undefined;
  }
}
