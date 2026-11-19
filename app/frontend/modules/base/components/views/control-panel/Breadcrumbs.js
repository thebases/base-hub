// Breadcrumbs — port of frontend-admin's control-panel/breadcrumbs.tsx onto
// Bootstrap 5's native .breadcrumb markup (no bespoke CSS needed). The last
// item is always the current page and is never clickable.
import { Component, loadTemplate, xml } from "nodexui/owl";


const template = await loadTemplate(new URL("./Breadcrumbs.xml", import.meta.url));

export class Breadcrumbs extends Component {
  static template = xml({ raw: [template] });
  static props = { items: { type: Array, optional: true } };
  static defaultProps = { items: [] };

  isLast(index) {
    return index === this.props.items.length - 1;
  }

  onClickCrumb(item) {
    item.onClick?.();
  }
}
