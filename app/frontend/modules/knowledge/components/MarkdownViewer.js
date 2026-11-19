// Port of frontend-admin's modules/base/components/markdown/markdown-viewer.tsx.
// react-markdown+remark-gfm -> vendored `marked` (native GFM support, no
// plugin needed); heading-id assignment mirrors the original's
// title+occurrence-count matching against a precomputed heading list so
// anchors stay stable. Mermaid fenced blocks render through a lazy
// dynamic-import of the vendored mermaid ESM build (only paid for when a doc
// actually contains a mermaid block). Mobile bottom-sheet bookmark drawer is
// descoped for this pass — see .agents/planning/2026-08-14-frontend-knowledge-module-phase4-plan.md.

import { Component, loadTemplate, markup, onMounted, onPatched, onWillStart, onWillUpdateProps, useRef, useState, xml } from "nodexui/owl";

import { slugify } from "@/modules/knowledge/markdown-headings.js";
import { loadMarked } from "@/services/script-loader.js";
import { sanitizeHtml } from "@/services/html-sanitize.js";

const template = await loadTemplate(new URL("./MarkdownViewer.xml", import.meta.url));

let mermaidInitialized = false;

function escapeHtml(value) {
  return String(value)
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;");
}

function normalizeHeadingText(raw) {
  return String(raw ?? "").replace(/[`*_~]/g, "").trim();
}

// buildRenderer creates a fresh marked.Renderer per render() call so the
// heading-occurrence counter always starts clean, matching the React
// version's per-render `headingIndex` Map.
function buildRenderer(headings, mermaidCharts) {
  const renderer = new window.marked.Renderer();
  const headingIndex = new Map();

  renderer.heading = function headingRenderer({ tokens, depth, text }) {
    const inner = this.parser.parseInline(tokens);
    const title = normalizeHeadingText(text) || "section";
    const count = headingIndex.get(title) ?? 0;
    headingIndex.set(title, count + 1);
    const matches = headings.filter((heading) => heading.title === title);
    const id = matches[count]?.id ?? slugify(title);
    const clampedDepth = Math.min(Math.max(depth, 1), 6);
    return `<h${clampedDepth} id="${id}" class="basehub-md-h${clampedDepth}">${inner}</h${clampedDepth}>\n`;
  };

  let mermaidSeq = 0;
  renderer.code = function codeRenderer({ text, lang }) {
    const language = (lang ?? "").trim();
    if (language === "mermaid") {
      mermaidSeq += 1;
      const chartID = `basehub-md-mermaid-${Date.now().toString(36)}-${mermaidSeq}`;
      mermaidCharts.set(chartID, text);
      // The wrapper's own id deliberately does NOT equal chartID: mermaid.render(id, …)
      // uses that id for its own internal bookkeeping (it looks up/creates an
      // element by that id while rendering), and colliding with a live DOM
      // node of the same id causes it to detach/replace our wrapper mid-render.
      return `<div class="basehub-md-mermaid" data-mermaid-pending="${chartID}">Rendering diagram…</div>\n`;
    }
    const classAttr = language ? ` class="language-${escapeHtml(language)}"` : "";
    return `<pre class="basehub-md-pre"><code${classAttr}>${escapeHtml(text)}</code></pre>\n`;
  };

  return renderer;
}

function renderMarkdown(content, headings, mermaidCharts) {
  if (!window.marked) return escapeHtml(content ?? "");
  const renderer = buildRenderer(headings, mermaidCharts);
  return window.marked.parse(content ?? "", { renderer, gfm: true, breaks: false });
}

export class MarkdownViewer extends Component {
  static template = xml({ raw: [template] });

  setup() {
    this.mermaidCharts = new Map();
    this.state = useState({ html: markup("") });
    this.article = useRef("article");

    onWillStart(() => this.renderContent());
    onWillUpdateProps((nextProps) => this.renderContent(nextProps));
    onMounted(() => this.renderMermaidBlocks());
    onPatched(() => this.renderMermaidBlocks());
  }

  get hasRail() {
    return this.props.showHeadingRail ?? true;
  }

  async renderContent(props = this.props) {
    // marked is loaded on demand (not by index.html); on failure renderMarkdown falls back to escaped text.
    await loadMarked().catch(() => {});
    this.mermaidCharts.clear();
    // Notes are user-written and marked passes raw HTML through: sanitize before it becomes markup.
    const html = sanitizeHtml(renderMarkdown(props.content, props.headings ?? [], this.mermaidCharts));
    this.state.html = markup(html);
  }

  async renderMermaidBlocks() {
    if (this.mermaidCharts.size === 0) return;
    const root = this.article.el;
    if (!root) return;
    const nodes = root.querySelectorAll("[data-mermaid-pending]");
    if (nodes.length === 0) return;

    const { default: mermaid } = await import("@/vendor/mermaid.esm.min.mjs");
    if (!mermaidInitialized) {
      mermaid.initialize({ startOnLoad: false, securityLevel: "strict", theme: "dark" });
      mermaidInitialized = true;
    }

    for (const node of nodes) {
      const chartID = node.getAttribute("data-mermaid-pending");
      const chart = this.mermaidCharts.get(chartID);
      if (chart === undefined) continue;
      try {
        const { svg } = await mermaid.render(chartID, chart);
        node.innerHTML = svg;
      } catch (error) {
        node.innerHTML = `<div class="basehub-md-mermaid-error">${escapeHtml(t("Mermaid render error: {message}", { message: error instanceof Error ? error.message : String(error) }))}</div>`;
      } finally {
        node.removeAttribute("data-mermaid-pending");
      }
    }
  }
}
