import { Component, loadTemplate, onMounted, onPatched, useRef, xml } from "nodexui/owl";

import { HierarchyCard } from "./HierarchyCard.js";
import { NoContent } from "@/modules/base/components/NoContent.js";


const template = await loadTemplate(new URL("./HierarchyView.xml", import.meta.url));

// HierarchyView renders the pre-built forest (see ModelView.js's loadHierarchy,
// which does the parent_field grouping via hierarchy-utils.js) as one column
// of root cards, each expandable into its own subtree.
export class HierarchyView extends Component {
  static template = xml({ raw: [template] });
  static components = { HierarchyCard, NoContent };

  setup() {
    this.root = useRef("root");
    onMounted(() => window.thebase?.mountAll(this.root.el));
    onPatched(() => window.thebase?.mountAll(this.root.el));
  }
}
