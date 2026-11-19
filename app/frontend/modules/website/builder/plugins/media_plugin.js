import { Plugin } from "../core/plugin_manager.js";
import { uploadImage } from "../host/upload.js";

// Image insertion. With the caret in text the image goes inline; otherwise it becomes an image block.
export class MediaPlugin extends Plugin {
  static id = "media";
  static dependencies = ["canvas", "selection", "text", "blocks"];

  async insertImageFile(file) {
    const url = await uploadImage(file);
    return this.insertImage(url, file.name.replace(/\.[^.]+$/, ""));
  }

  insertImage(url, alt = "") {
    const { doc, root } = this.plugins.canvas;
    const esc = (s) => s.replace(/&/g, "&amp;").replace(/"/g, "&quot;").replace(/</g, "&lt;");
    const img = `<img class="img-fluid" src="${esc(url)}" alt="${esc(alt)}"/>`;
    const sel = doc.getSelection();
    const anchor = sel && sel.rangeCount ? sel.anchorNode : null;
    const el = anchor && (anchor.nodeType === 1 ? anchor : anchor.parentElement);
    if (el && el.closest("[contenteditable=true]") && root.contains(el)) {
      return this.plugins.text.run("insert image", () => doc.execCommand("insertHTML", false, img));
    }
    return this.plugins.blocks.insertHtml(`<section class="bh-image py-4 text-center" data-snippet="image"><div class="container">${img}</div></section>`);
  }
}
