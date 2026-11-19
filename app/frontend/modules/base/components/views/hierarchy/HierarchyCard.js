import { Component, loadTemplate, useState, xml } from "nodexui/owl";

import { Badge, Button, Card, CardBody } from "nodexui/component";


const template = await loadTemplate(new URL("./HierarchyCard.xml", import.meta.url));

// displayValue mirrors KanbanRecord.js's helper: many2one tuples show their
// label, everything else is stringified as-is.
function displayValue(value) {
  if (value === null || value === undefined || value === false) return "";
  if (Array.isArray(value) && value.length === 2) return String(value[1]);
  return String(value);
}

// HierarchyCard renders one tree node and, recursively, its expanded
// children — the same "one card, self-referencing" shape a KanbanRecord
// would need if kanban columns nested, but flattened into a single component
// since hierarchy nodes (unlike kanban cards) can have their own children.
export class HierarchyCard extends Component {
  static template = xml({ raw: [template] });
  static components = { Badge, Button, Card, CardBody };

  setup() {
    this.state = useState({ expanded: true });
  }

  get title() {
    return displayValue(this.props.node.record[this.props.layout.titleField]) || t("Untitled");
  }

  get subFields() {
    return (this.props.layout.subFields ?? []).flatMap((name) => {
      const value = displayValue(this.props.node.record[name]);
      return value ? [{ name, value }] : [];
    });
  }

  get hasChildren() {
    return this.props.node.children.length > 0;
  }

  toggleExpand() {
    this.state.expanded = !this.state.expanded;
  }
}

// Self-reference is assigned after the class exists, matching MenuTree.js's
// same recursive-component pattern (a class can't name itself in its own
// static components object literal).
HierarchyCard.components = { ...HierarchyCard.components, HierarchyCard };
