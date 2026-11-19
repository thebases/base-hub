import { Plugin } from "../core/plugin_manager.js";
import { uniquifyIds } from "../core/ids.js";

// Padding scale of the selection handles, the standard website-builder steps (0, 4, then 8..256 by 8).
const PADDING_STEPS = [0, 4, ...Array.from({ length: 32 }, (_, i) => (i + 1) * 8)];
const snapPadding = (px) => PADDING_STEPS.reduce((best, v) => (Math.abs(v - px) < Math.abs(best - px) ? v : best), 0);

// Block actions on the selection: move up/down among siblings, clone, and drag the top/bottom padding
// handles (snapped to PADDING_STEPS; Shift = free 1px). Each is one undoable operation.
export class OverlayPlugin extends Plugin {
  static id = "overlay";
  static dependencies = ["canvas", "selection", "remove"];

  start() {
    this.bar = null;
    this.ctx.on("canvas-ready", () => this.mountBar());
    this.ctx.on("selection-changed", () => this.placeBar());
    this.ctx.on("content-restored", () => this.placeBar());
  }

  // Floating toolbar (move up / move down / delete) centered on top of the selected block. It lives
  // in the canvas body outside the editable root, so it is never serialized into the page.
  mountBar() {
    const { doc, history } = this.plugins.canvas;
    const icon = (d) => `<svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">${d}</svg>`;
    const style = doc.createElement("style");
    style.textContent = `
.bhe-bar { position: fixed; z-index: 2147483000; display: none; gap: 2px; padding: 3px; background: #fff; border: 1px solid #0d6efd; border-radius: 6px; box-shadow: 0 2px 8px rgba(0,0,0,.2); transform: translateX(-50%); }
.bhe-bar button { all: unset; box-sizing: border-box; width: 26px; height: 26px; display: inline-flex; align-items: center; justify-content: center; border-radius: 4px; cursor: pointer; color: #212529; }
.bhe-bar button:hover:not(:disabled) { background: #e9ecef; }
.bhe-bar button:disabled { opacity: .35; cursor: default; }
.bhe-bar button[data-act="delete"] { color: #dc3545; }
.bhe-pad { position: fixed; z-index: 2147482999; display: none; left: 0; width: 0; box-sizing: border-box; background: repeating-linear-gradient(45deg, rgba(13,110,253,.08), rgba(13,110,253,.08) 5px, rgba(13,110,253,.2) 5px, rgba(13,110,253,.2) 10px); pointer-events: none; }
.bhe-pad-grip { position: absolute; left: 50%; width: 36px; height: 12px; margin: -6px 0 0 -18px; border-radius: 6px; background: #fff; border: 2px solid #0d6efd; cursor: ns-resize; pointer-events: auto; touch-action: none; box-sizing: border-box; }
.bhe-pad[data-edge="top"] .bhe-pad-grip { top: 100%; }
.bhe-pad[data-edge="bottom"] .bhe-pad-grip { top: 0; }
.bhe-pad-grip:hover, .bhe-pad.bhe-dragging .bhe-pad-grip { background: #0d6efd; }
.bhe-pad-tip { position: absolute; left: 50%; transform: translateX(-50%); font: 600 11px/1 system-ui, sans-serif; color: #fff; background: #0d6efd; padding: 2px 5px; border-radius: 3px; display: none; white-space: nowrap; }
.bhe-pad[data-edge="top"] .bhe-pad-tip { top: 100%; margin-top: 10px; }
.bhe-pad[data-edge="bottom"] .bhe-pad-tip { bottom: 100%; margin-bottom: 10px; }
.bhe-pad.bhe-dragging .bhe-pad-tip { display: block; }`;
    doc.head.append(style);
    const bar = doc.createElement("div");
    bar.className = "bhe-bar";
    bar.setAttribute("data-bhe-ui", "");
    bar.innerHTML =
      `<button type="button" data-act="up" title="Move up">${icon('<path d="M12 19V5M5 12l7-7 7 7"/>')}</button>` +
      `<button type="button" data-act="down" title="Move down">${icon('<path d="M12 5v14M19 12l-7 7-7-7"/>')}</button>` +
      `<button type="button" data-act="delete" title="Delete">${icon('<path d="M3 6h18M8 6V4h8v2M19 6l-1 14H6L5 6M10 11v6M14 11v6"/>')}</button>`;
    bar.addEventListener("mousedown", (ev) => ev.preventDefault());
    bar.addEventListener("click", async (ev) => {
      const act = ev.target.closest("button")?.dataset.act;
      if (!act) return;
      ev.stopPropagation();
      if (act === "delete") await this.plugins.remove.removeSelected();
      else await this.move(act === "up" ? -1 : 1);
      this.placeBar();
    });
    doc.body.append(bar);
    this.bar = bar;
    this.pads = ["top", "bottom"].map((edge) => this.mountPad(doc, edge));
    const win = doc.defaultView;
    win.addEventListener("scroll", () => this.placeBar(), { passive: true });
    win.addEventListener("resize", () => this.placeBar());
    history.on(() => this.placeBar());
  }

  // One padding band (edge = "top" | "bottom") whose grip is dragged to change that side's padding.
  // The band height equals the block's current padding, so it shows what is being edited.
  mountPad(doc, edge) {
    const el = doc.createElement("div");
    el.className = "bhe-pad";
    el.dataset.edge = edge;
    el.setAttribute("data-bhe-ui", "");
    el.innerHTML = '<div class="bhe-pad-grip" title="Drag to change padding (Shift: free)"></div><div class="bhe-pad-tip"></div>';
    const grip = el.firstChild;
    const prop = edge === "top" ? "paddingTop" : "paddingBottom";
    grip.addEventListener("pointerdown", (ev) => {
      const block = this.block;
      if (!block || ev.button !== 0) return;
      ev.preventDefault();
      ev.stopPropagation();
      const { history } = this.plugins.canvas;
      history.prepare();
      const before = history.root.get();
      const startY = ev.clientY;
      const startPad = parseFloat(doc.defaultView.getComputedStyle(block)[prop]) || 0;
      grip.setPointerCapture(ev.pointerId);
      el.classList.add("bhe-dragging");
      this.dragging = true;
      const onMove = (e) => {
        // Top grip moves down to add padding; bottom grip moves up.
        const raw = Math.max(0, Math.min(PADDING_STEPS.at(-1), startPad + (edge === "top" ? e.clientY - startY : startY - e.clientY)));
        const value = e.shiftKey ? Math.round(raw) : snapPadding(raw);
        block.style[prop] = `${value}px`;
        el.querySelector(".bhe-pad-tip").textContent = `${value}px`;
        this.placeBar();
      };
      const onEnd = () => {
        grip.removeEventListener("pointermove", onMove);
        grip.removeEventListener("pointerup", onEnd);
        grip.removeEventListener("pointercancel", onEnd);
        el.classList.remove("bhe-dragging");
        this.dragging = false;
        // One undo step for the whole drag.
        history.commit(`padding ${edge}`, before);
        this.placeBar();
      };
      grip.addEventListener("pointermove", onMove);
      grip.addEventListener("pointerup", onEnd);
      grip.addEventListener("pointercancel", onEnd);
    });
    doc.body.append(el);
    return el;
  }

  placePads(block) {
    const win = block?.ownerDocument.defaultView;
    for (const el of this.pads || []) {
      if (!block) {
        el.style.display = "none";
        continue;
      }
      const edge = el.dataset.edge;
      const rect = block.getBoundingClientRect();
      const pad = parseFloat(win.getComputedStyle(block)[edge === "top" ? "paddingTop" : "paddingBottom"]) || 0;
      el.style.display = "block";
      el.style.left = `${rect.left}px`;
      el.style.width = `${rect.width}px`;
      el.style.height = `${pad}px`;
      el.style.top = `${edge === "top" ? rect.top : rect.bottom - pad}px`;
    }
  }

  placeBar() {
    const bar = this.bar;
    const block = this.block;
    if (!bar) return;
    // After the canvas is remounted (scope switch) the old block belongs to a detached document without a window.
    if (!block || !block.isConnected || !block.ownerDocument.defaultView) {
      bar.style.display = "none";
      this.placePads(null);
      return;
    }
    this.placePads(block);
    bar.style.display = "flex";
    bar.querySelector('[data-act="up"]').disabled = !this.canMove(-1);
    bar.querySelector('[data-act="down"]').disabled = !this.canMove(1);
    const rect = block.getBoundingClientRect();
    const h = bar.offsetHeight || 32;
    const vh = bar.ownerDocument.defaultView.innerHeight;
    // Sits just above the block; when the block's top is off-screen it sticks to the viewport top.
    const top = Math.min(Math.max(rect.top - h - 4, 4), Math.max(vh - h - 4, 4));
    const left = Math.min(Math.max(rect.left + rect.width / 2, 60), bar.ownerDocument.defaultView.innerWidth - 60);
    bar.style.top = `${top}px`;
    bar.style.left = `${left}px`;
  }

  get block() {
    return this.plugins.selection.selected;
  }

  canMove(direction) {
    const block = this.block;
    if (!block) return false;
    const sibling = direction < 0 ? block.previousElementSibling : block.nextElementSibling;
    return !!sibling;
  }

  move(direction) {
    const block = this.block;
    if (!this.canMove(direction)) return Promise.resolve();
    return this.plugins.canvas.history.run(direction < 0 ? "move up" : "move down", () => {
      if (direction < 0) block.previousElementSibling.before(block);
      else block.nextElementSibling.after(block);
    });
  }

  clone() {
    const block = this.block;
    if (!block) return Promise.resolve();
    return this.plugins.canvas.history.run("clone block", () => {
      const copy = block.cloneNode(true);
      // Editor state must not be copied; ids are renamed (with their references) so the copy works on its own.
      uniquifyIds(copy, this.plugins.canvas.doc);
      [copy, ...copy.querySelectorAll("*")].forEach((el) => {
        el.removeAttribute("contenteditable");
        el.classList.remove("bhe-selected", "bhe-hover");
      });
      block.after(copy);
      this.plugins.selection.select(copy);
    });
  }
}
