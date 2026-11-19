import { Plugin } from "../core/plugin_manager.js";

// Header/footer templates (header and footer scope). Applying one swaps the whole region as a single
// undoable operation and carries the site's own content and settings over:
// the menu slot, brand name, button, text/social blocks, which elements are shown, the scroll effect,
// link style and the other bh-* settings classes survive; the layout is the template's.
const SETTING_CLASS = /^(bh-(effect|links|accent|mobile|pos|sidebar|footer-slideout)(-|$)|bh-hover-dropdown$|bh-toggler-left$)/;
const REGION = "#site-header, #site-footer";

function copyContent(from, next, selector, how) {
  const a = from.querySelector(selector);
  const b = next.querySelector(selector);
  if (a && b) how(a, b);
}

function carryOver(old, next) {
  if (!old) return next;
  // Settings classes and the region's own inline style (custom colors / font).
  const carried = [...old.classList].filter((c) => SETTING_CLASS.test(c));
  // One value per family (bh-effect-*, bh-links-*, ...): the carried classes replace the template's own.
  const families = new Set(carried.map((c) => (/^bh-(effect|links|accent|mobile|pos|sidebar|footer-slideout)/.exec(c) || [c])[0]));
  [...next.classList].filter((n) => [...families].some((f) => n.startsWith(f))).forEach((n) => next.classList.remove(n));
  carried.forEach((c) => next.classList.add(c));
  if (old.getAttribute("style")) next.setAttribute("style", old.getAttribute("style"));
  // Shown/hidden elements.
  old.querySelectorAll("[data-chrome-el]").forEach((el) => {
    const twin = next.querySelector(`[data-chrome-el="${el.getAttribute("data-chrome-el")}"]`);
    if (twin) twin.classList.toggle("d-none", el.classList.contains("d-none"));
  });
  // Content.
  copyContent(old, next, "#site-nav-menu", (a, b) => { b.innerHTML = a.innerHTML; });
  copyContent(old, next, "#site-brand-name", (a, b) => { b.textContent = a.textContent; });
  copyContent(old, next, '[data-chrome-el="cta"]', (a, b) => { b.textContent = a.textContent; b.setAttribute("href", a.getAttribute("href") || "#"); });
  copyContent(old, next, '[data-chrome-el="text"]', (a, b) => { b.innerHTML = a.innerHTML; });
  copyContent(old, next, '[data-chrome-el="social"]', (a, b) => { b.innerHTML = a.innerHTML; });
  copyContent(old, next, ".bh-footer-copyright > div > span", (a, b) => { b.innerHTML = a.innerHTML; });
  return next;
}

export class ChromePlugin extends Plugin {
  static id = "chrome";
  static dependencies = ["canvas", "selection"];

  // Key of the template the region currently uses ("default", "boxed", ...), from its marker attribute.
  currentName() {
    const el = this.plugins.canvas.root?.querySelector(REGION);
    return el ? el.getAttribute("data-bh-header") || el.getAttribute("data-bh-footer") || "" : "";
  }

  apply(html) {
    const { canvas, selection } = this.plugins;
    const holder = canvas.doc.createElement("template");
    holder.innerHTML = html;
    const next = holder.content.firstElementChild;
    if (!next) return Promise.reject(new Error("The template is empty."));
    return canvas.history.run("change template", () => {
      const old = canvas.root.querySelector(REGION);
      carryOver(old, next);
      canvas.root.replaceChildren(next);
      // Selecting the new region refreshes the Customize controls without leaving the Template tab.
      this.ctx.emit("insert-start");
      selection.select(next);
      this.ctx.emit("insert-end");
    });
  }
}
