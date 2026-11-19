// Pager — extracted from ListView.xml's inline pagination block so it can be
// reused by ControlPanel. Same offset/limit/total contract ModelView already
// threads through fetchModelListRecords/fetchModelKanbanRecords.
import { Component, loadTemplate, xml } from "nodexui/owl";

import { Pagination, PaginationItem } from "nodexui/component";
import { Icon } from "nodexui/component";

const template = await loadTemplate(new URL("./Pager.xml", import.meta.url));

export class Pager extends Component {
  static template = xml({ raw: [template] });
  static components = { Pagination, PaginationItem, Icon };
  static props = { offset: Number, limit: Number, total: Number, onUpdate: Function };

  get rangeStart() {
    return this.props.total === 0 ? 0 : this.props.offset + 1;
  }

  get rangeEnd() {
    return Math.min(this.props.offset + this.props.limit, this.props.total);
  }

  get hasPrev() {
    return this.props.offset > 0;
  }

  get hasNext() {
    return this.props.offset + this.props.limit < this.props.total;
  }

  onPrev() {
    if (!this.hasPrev) return;
    this.props.onUpdate({ offset: Math.max(0, this.props.offset - this.props.limit), limit: this.props.limit });
  }

  onNext() {
    if (!this.hasNext) return;
    this.props.onUpdate({ offset: this.props.offset + this.props.limit, limit: this.props.limit });
  }
}
