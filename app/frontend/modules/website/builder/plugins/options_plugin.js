import { Plugin } from "../core/plugin_manager.js";
import { applyControl, readControl, targetsOf } from "../core/option_controls.js";

// Customize tab engine: option schemas (data, shipped by modules) decide which controls a selected
// block gets; every change goes through one undoable operation. No per-component code.
export class OptionsPlugin extends Plugin {
  static id = "options";
  static dependencies = ["canvas", "selection"];

  start() {
    this.schemas = [];
  }

  async load(fetcher) {
    this.schemas = await fetcher();
  }

  // Groups applicable to `block`, each control with its current value. Controls whose target is
  // absent in this block are left out, as are schemas the block is excluded from.
  groupsFor(block) {
    if (!block) return [];
    const out = [];
    for (const schema of this.schemas) {
      let matches = false;
      try {
        matches = block.matches(schema.selector) && !(schema.exclude && block.matches(schema.exclude));
      } catch {
        matches = false;
      }
      if (!matches) continue;
      for (const group of schema.groups) {
        const controls = group.controls
          .filter((control) => targetsOf(block, control).length > 0)
          .map((control) => ({ control, value: readControl(block, control) }));
        if (controls.length) out.push({ key: `${schema.key}:${group.label}`, label: group.label, controls });
      }
    }
    return out;
  }

  set(control, value) {
    const block = this.plugins.selection.selected;
    if (!block) return Promise.resolve();
    return this.plugins.canvas.history.run(`set ${control.label}`, () => applyControl(block, control, value));
  }
}
