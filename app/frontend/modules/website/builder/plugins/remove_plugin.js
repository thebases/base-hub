import { Plugin } from "../core/plugin_manager.js";

export class RemovePlugin extends Plugin {
  static id = "remove";
  static dependencies = ["canvas", "selection"];

  removeSelected() {
    const block = this.plugins.selection.selected;
    if (!block) return Promise.resolve();
    return this.plugins.canvas.history.run("remove block", () => {
      this.plugins.selection.select(null);
      block.remove();
    });
  }
}
