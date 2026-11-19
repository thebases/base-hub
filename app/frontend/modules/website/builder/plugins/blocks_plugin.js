import { Plugin } from "../core/plugin_manager.js";
import { CORE_SNIPPETS } from "../snippets/catalog.js";
import { uniquifyIds } from "../core/ids.js";
import { canInsertInto, pageRegions } from "../core/drop_zones.js";

// Page-level regions that hold blocks when a page has them.
// Contributes the block catalog and inserts blocks through an operation (undoable). Insertion goes
// after the selected block, else at the end of the root. Drag from the Blocks tab onto the canvas
// drops before the block under the pointer (drop zones filtered by data-snippet-allowed-in in B1).
export class BlocksPlugin extends Plugin {
  static id = "blocks";
  static dependencies = ["canvas", "selection"];

  start() {
    this.snippets = CORE_SNIPPETS;
  }

  catalog() {
    return this.snippets;
  }

  // Replaces the built-in fallback with the server catalog. An empty or failed load keeps the fallback.
  async loadCatalog(fetcher) {
    const rows = await fetcher();
    if (rows.length) this.snippets = rows;
    return this.snippets;
  }

  insert(key, { before = null } = {}) {
    const snippet = this.catalog().find((s) => s.key === key);
    if (!snippet) throw new Error(`unknown snippet ${key}`);
    if (!canInsertInto(snippet, this.plugins.canvas.root)) throw new Error(`${snippet.name} cannot be placed here`);
    return this.insertHtml(snippet.html, { before, label: `insert ${key}` });
  }

  // Inserts markup as a block through one operation: before `before`, else after the selection, else last.
  insertHtml(html, { before = null, label = "insert block" } = {}) {
    const { doc, root, history } = this.plugins.canvas;
    // The host keeps the Blocks tab open while a block is being added (selecting the new block is not a request to customize it).
    this.ctx.emit("insert-start");
    return history.run(label, () => {
      const tpl = doc.createElement("template");
      tpl.innerHTML = html;
      const node = tpl.content.firstElementChild;
      uniquifyIds(node, doc);
      const anchor = before || this.plugins.selection.selected;
      const target = before || (anchor && root.contains(anchor) ? anchor : null);
      // A page that carries header/main/footer only accepts blocks inside them, never before the
      // header or after the footer, so a position that lands directly in the root is redirected.
      const regions = pageRegions(root);
      const inRoot = !target || target.parentElement === root || target.parentElement === regions[0]?.parentElement;
      if (regions.length && inRoot) {
        const host = target && regions.includes(target) ? target : regions.find((el) => el.tagName === "MAIN") || regions.find((el) => el.tagName !== "FOOTER") || regions[0];
        host.append(node);
      } else if (before) before.before(node);
      else if (target) target.after(node);
      else root.append(node);
      this.plugins.selection.select(node);
    }).finally(() => this.ctx.emit("insert-end"));
  }

  // Wires the canvas as a drop target for drags that carry application/x-bh-snippet.
  attachDrop() {
    const { doc, root } = this.plugins.canvas;
    doc.addEventListener("dragover", (ev) => {
      if ([...(ev.dataTransfer?.types || [])].includes("application/x-bh-snippet")) ev.preventDefault();
    });
    doc.addEventListener("drop", (ev) => {
      const key = ev.dataTransfer?.getData("application/x-bh-snippet");
      if (!key) return;
      ev.preventDefault();
      const target = ev.target.closest && root.contains(ev.target) ? (ev.target.closest("[data-snippet]") || pageRegions(root).find((el) => el.contains(ev.target)) || null) : null;
      this.insert(key, { before: target });
    });
  }
}
