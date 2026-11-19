// List "New" popup for tbl.website_page: shows every page template shipped by an installed module
// (`<module>/static/templates/*.xml`, via the `list_page_templates` RuntimeMethod) and opens the
// new-page form pre-filled with the chosen one (`default_page_template`). The server applies the
// template's document when the page is created.
import { Component, loadTemplate, onMounted, useState, xml } from "nodexui/owl";
import { executeModelMethod } from "@/services/api/jsonv2.js";

const template = await loadTemplate(new URL("./PageTemplatePicker.xml", import.meta.url));

const PAGE_MODEL = "tbl.website_page";
const BLANK_KEY = "website/blank";

export class PageTemplatePicker extends Component {
  static template = xml({ raw: [template] });
  static props = {
    ctx: { type: Object, optional: true },
    node: { type: Object, optional: true },
    record: { type: Object, optional: true },
    readonly: { type: Boolean, optional: true },
  };

  setup() {
    this.state = useState({ loading: true, error: "", templates: [], selected: "" });
    onMounted(() => this.load());
  }

  async load() {
    try {
      const templates = await executeModelMethod(PAGE_MODEL, "list_page_templates");
      this.state.templates = Array.isArray(templates) ? templates : [];
      const hasBlank = this.state.templates.some((tpl) => tpl.key === BLANK_KEY);
      this.state.selected = hasBlank ? BLANK_KEY : (this.state.templates[0]?.key ?? "");
    } catch (error) {
      this.state.error = error instanceof Error ? error.message : "Could not load page templates";
    } finally {
      this.state.loading = false;
    }
  }

  select(key) {
    this.state.selected = key;
  }

  confirm(key) {
    if (!key) return;
    this.props.ctx?.closeAndNavigate?.(`/web/${PAGE_MODEL}/new?default_page_template=${encodeURIComponent(key)}`);
  }

  cancel() {
    this.props.ctx?.close?.();
  }
}
