import { formatLocalDateTime } from "@/services/server-time.js";
import { Component, loadTemplate, xml } from "nodexui/owl";

import { getSelectionOptions, isSelectionField } from "@/modules/base/components/views/form/form-field-normalizer.js";
import { paletteColorForIndex } from "@/modules/base/components/views/base-view/color-palette.js";
import { Avatar, Badge, Button, Card, CardBody } from "nodexui/component";


const template = await loadTemplate(new URL("./KanbanRecord.xml", import.meta.url));

function displayValue(value) {
  if (value === null || value === undefined || value === false) return "";
  if (Array.isArray(value) && value.length === 2) return String(value[1]);
  return String(value);
}

export class KanbanRecord extends Component {
  static template = xml({ raw: [template] });
  static components = { Avatar, Badge, Button, Card, CardBody };

  get title() {
    return displayValue(this.props.record[this.props.layout.titleField]) || t("Untitled");
  }

  // cardStyle paints a left accent from the arch's color="<field>" index (e.g. the stage color).
  get cardStyle() {
    const name = this.props.layout.colorField;
    const color = name ? paletteColorForIndex(this.props.record[name]) : "";
    return color ? `border-left: 6px solid ${color};` : "";
  }

  get subFields() {
    return (this.props.layout.subFields ?? []).flatMap((name) => {
      const field = this.props.fieldMap[name];
      const raw = this.props.record[name];
      // Date/datetime arrive as UTC Unix seconds; show them in the viewer's zone, not as a raw number.
      const isTime = field && (field.type === "datetime" || field.type === "date") && typeof raw === "number";
      const value = isTime ? formatLocalDateTime(raw, false) : displayValue(raw);
      if (!field || !value) return [];
      return [{ name, label: field.label, value }];
    });
  }

  // Selection badges show their human label, not the stored technical value.
  get badgeLabel() {
    const name = this.props.layout.badgeField;
    if (!name) return "";
    const field = this.props.fieldMap[name];
    const raw = this.props.record[name];
    if (field && isSelectionField(field)) {
      const option = getSelectionOptions(field).find((item) => String(item.value) === String(raw ?? ""));
      return option?.label ?? displayValue(raw);
    }
    return displayValue(raw);
  }

  get avatarInitials() {
    const name = this.props.layout.avatarField;
    const value = name ? displayValue(this.props.record[name]) : "";
    if (!value) return "";
    return value
      .split(" ")
      .slice(0, 2)
      .map((word) => word[0]?.toUpperCase() ?? "")
      .join("");
  }
}