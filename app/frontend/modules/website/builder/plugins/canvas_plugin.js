import { Plugin } from "../core/plugin_manager.js";
import { History } from "../core/history.js";
import { serializeBody } from "../core/serialize.js";
import { sanitizeHtml } from "@/services/html-sanitize.js";

// Owns the same-origin iframe: renders the real site chrome (header/footer) around an editable page region
// root holding body_html, loaded with the site's published CSS so editing is WYSIWYG. Exposes the
// history and `getBodyHtml()`. Header/footer are display-only unless `scope` selects them (site-level editing, B3).
const EDITOR_CSS = `
.bhe-selected { outline: 2px solid #0d6efd; outline-offset: -2px; }
.bhe-hover { outline: 1px dashed #0d6efd; outline-offset: -1px; }
/* The region being edited keeps full colour and an outline; the other two are dimmed and inert. */
[data-bhe-chrome] { pointer-events: none !important; user-select: none; opacity: .35 !important; filter: grayscale(.6) !important; transition: opacity .15s; }
#bhe-root { outline: 2px solid #0d6efd; outline-offset: -2px; }
/* The page region fills the space between header and footer, so the footer sits at the bottom of a short page like on the published site. */
body { display: flex; flex-direction: column; height: 100svh; margin: 0; }
[data-region="page"] { flex: 1 1 100%; }
#bhe-root[data-region="page"] { min-height: 40vh; }
.bhe-grid .row > [class*="col"] { outline: 1px dashed #6c757d; outline-offset: -1px; min-height: 2rem; }
.bhe-grid .row { outline: 1px solid #adb5bd; outline-offset: 2px; }
[data-bh-popup] { display: block !important; position: static !important; }
[data-bh-animate] { opacity: 1 !important; transform: none !important; }
/* The canvas runs no behaviors: header effects and the sidebar layout would only get in the way of editing. */
#site-header { position: static !important; transform: none !important; opacity: 1 !important; }
#site-header.bh-header-sidebar { width: auto !important; }
body:has(.bh-header-sidebar) { padding: 0 !important; }
#site-footer.bh-footer-slideout { position: static !important; }
/* The published site's runtime (website-chrome.js) injects these button icons; the canvas does not run it, so they are drawn in CSS only (never written into the saved HTML). */
[data-site-theme-toggle]:empty::before, #site-lang-current:empty::before { content: ""; width: 18px; height: 18px; background: currentColor; -webkit-mask: var(--bhe-icon) center / contain no-repeat; mask: var(--bhe-icon) center / contain no-repeat; }
[data-site-theme-toggle]:empty { --bhe-icon: url("data:image/svg+xml,%3Csvg xmlns=%22http://www.w3.org/2000/svg%22 viewBox=%220 0 24 24%22 fill=%22none%22 stroke=%22black%22 stroke-width=%222%22 stroke-linecap=%22round%22 stroke-linejoin=%22round%22%3E%3Cpath d=%22M21 12.8A9 9 0 1 1 11.2 3a7 7 0 0 0 9.8 9.8z%22/%3E%3C/svg%3E"); }
#site-lang-current:empty { --bhe-icon: url("data:image/svg+xml,%3Csvg xmlns=%22http://www.w3.org/2000/svg%22 viewBox=%220 0 24 24%22 fill=%22none%22 stroke=%22black%22 stroke-width=%222%22 stroke-linecap=%22round%22 stroke-linejoin=%22round%22%3E%3Ccircle cx=%2212%22 cy=%2212%22 r=%2210%22/%3E%3Cpath d=%22M2 12h20M12 2a15 15 0 0 1 0 20M12 2a15 15 0 0 0 0 20%22/%3E%3C/svg%3E"); }
`;

export class CanvasPlugin extends Plugin {
  static id = "canvas";
  static dependencies = [];

  // Mounts into `iframe` and resolves when the document is ready.
  // `scope` picks the editable region ("page" | "header" | "footer"); the others are display-only chrome.
  async mount(iframe, { headerHtml = "", footerHtml = "", bodyHtml = "", cssUrls = [], jsUrls = [], scope = "page", themeCss = "", pageCss = "" } = {}) {
    this.iframe = iframe;
    this.scope = scope;
    // The canvas is a same-origin srcdoc frame: stored HTML is cleaned with the server's rules
    // before it is written into it, in case a row bypassed the server sanitizer (import, SQL).
    [headerHtml, footerHtml, bodyHtml] = [headerHtml, footerHtml, bodyHtml].map((html) => sanitizeHtml(html, { profile: "website" }));
    const region = (name, tag, html) =>
      `<${tag} data-region="${name}" ${name === scope ? 'id="bhe-root"' : "data-bhe-chrome"}>${name === scope ? "" : html}</${tag}>`;
    const links = cssUrls.map((u) => `<link rel="stylesheet" href="${u}">`).join("");
    // BaseUI's runtime (b-icon / b-ui custom markup) must run in the canvas like on the published site;
    // it watches the DOM, so icons inserted later by the editor render too.
    const scripts = jsUrls.map((u) => `<script src="${u}"></script>`).join("");
    const doc = `<!doctype html><html><head><meta charset="utf-8">${links}<style id="bhe-theme">${themeCss}</style><style id="bhe-page-css">${String(pageCss || "").replace(/<\/style/gi, "")}</style><style>${EDITOR_CSS}</style></head><body>` +
      region("header", "div", headerHtml) + region("page", "div", bodyHtml) + region("footer", "div", footerHtml) + `${scripts}</body></html>`;
    await new Promise((resolve) => {
      iframe.addEventListener("load", resolve, { once: true });
      iframe.srcdoc = doc;
    });
    this.doc = iframe.contentDocument;
    this.root = this.doc.getElementById("bhe-root");
    this.root.innerHTML = { page: bodyHtml, header: headerHtml, footer: footerHtml }[scope];
    this.history = new History({
      get: () => serializeBody(this.root),
      set: (html) => {
        this.root.innerHTML = html;
        this.ctx.emit("content-restored");
      },
    });
    this.ctx.emit("canvas-ready");
  }

  // Shows the page's own stylesheet (custom CSS) in the canvas.
  setPageCss(css) {
    const style = this.doc?.getElementById("bhe-page-css");
    if (style) style.textContent = String(css || "").replace(/<\/style/gi, "");
  }

  // Swaps the draft theme stylesheet in place, so theme edits show without a reload.
  setThemeCss(css) {
    const style = this.doc?.getElementById("bhe-theme");
    if (style) style.textContent = css;
  }

  getBodyHtml() {
    return serializeBody(this.root);
  }
}
