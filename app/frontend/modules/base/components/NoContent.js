// Universal empty-state placeholder — port of frontend-admin's no-content.tsx.
// Any widget with zero records (list, kanban column/board, attachments, x2many
// summaries) renders this instead of a blank area.
import { Component, loadTemplate, xml } from "nodexui/owl";
import { Icon } from "nodexui/component";

const template = await loadTemplate(new URL("./NoContent.xml", import.meta.url));

export class NoContent extends Component {
  static template = xml({ raw: [template] });
  static components = { Icon };
  static props = { label: { type: String, optional: true }, className: { type: String, optional: true } };
  static defaultProps = { label: t("No content"), className: "" };
}
