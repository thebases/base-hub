import { Plugin } from "../core/plugin_manager.js";
import { pageRegions } from "../core/drop_zones.js";

// Click selects the nearest block (an element with data-snippet inside the root); hover outlines it.
// Selection is editor state only (bhe- class), stripped on save.
export class SelectionPlugin extends Plugin {
  static id = "selection";
  static dependencies = ["canvas"];

  start() {
    this.selected = null;
    this.gridMode = false;
    this.ctx.on("canvas-ready", () => this.attach());
    this.ctx.on("content-restored", () => this.select(null));
    // A remounted canvas starts without grid mode.
    this.ctx.on("canvas-ready", () => { this.gridMode = false; });
  }

  attach() {
    const { doc, root } = this.plugins.canvas;
    // In the page, blocks are [data-snippet] elements. Header and footer have no such markers, so
    // their top-level elements act as blocks.
    const blockOf = (el) => {
      if (!el || !el.closest || !root.contains(el)) return null;
      // Grid layout mode selects columns and rows instead of whole blocks.
      if (this.gridMode) {
        const grid = el.closest('.row > [class*="col"]') || el.closest(".row");
        if (grid && root.contains(grid)) return grid;
      }
      // Header/footer editing selects the clicked element itself (any component inside the region); SVG
      // internals resolve to their <svg>.
      if (this.plugins.canvas.scope !== "page") {
        const target = el.closest("svg") || el;
        return target === root ? null : target;
      }
      const marked = el.closest("[data-snippet]");
      if (marked) return marked;
      // A page that carries <header>/<main>/<footer> lets those regions be selected, so a block can be added into them.
      if (this.plugins.canvas.scope === "page") {
        return pageRegions(root).find((r) => r.contains(el)) || null;
      }
      return null;
    };
    doc.addEventListener("click", (ev) => {
      const block = blockOf(ev.target);
      if (block || root.contains(ev.target)) {
        ev.preventDefault();
        this.select(block);
      }
    });
    doc.addEventListener("mouseover", (ev) => {
      root.querySelectorAll(".bhe-hover").forEach((e) => e.classList.remove("bhe-hover"));
      blockOf(ev.target)?.classList.add("bhe-hover");
    });
  }

  // Grid layout mode: click selects a column (or its row); the options tab then edits the grid.
  setGridMode(on) {
    this.gridMode = !!on;
    this.plugins.canvas.doc?.body.classList.toggle("bhe-grid", this.gridMode);
    this.select(null);
  }

  // Moves the selection one level up: column -> row -> block.
  selectParent() {
    const { root } = this.plugins.canvas;
    const el = this.selected;
    if (!el) return;
    const inChrome = this.plugins.canvas.scope !== "page";
    const up = el.matches('.row > [class*="col"]') ? el.closest(".row") : inChrome ? el.parentElement : el.parentElement?.closest("[data-snippet]");
    if (up && up !== el && up !== root && root.contains(up)) this.select(up);
  }

  select(block) {
    this.selected?.classList.remove("bhe-selected");
    this.selected = block;
    block?.classList.add("bhe-selected");
    this.ctx.emit("selection-changed", block);
  }
}
