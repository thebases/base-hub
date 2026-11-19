import { Component, loadTemplate, onWillUnmount, useRef, useState, xml } from "nodexui/owl";

import { Input } from "nodexui/component";

const template = await loadTemplate(new URL("./RemoteCombobox.xml", import.meta.url));

// RemoteCombobox is a server-searched picker: BaseUI's Combobox only filters the options it was
// given by label text, so a record matched by the backend on another field (a code, an email) is
// hidden. This one never filters locally: every keystroke is handed to onSearch (debounced) and
// whatever the backend returns is shown as-is. The Input inside a BaseUI <Field> picks up the
// Field's floating label, so it looks like the other outlined controls.
export class RemoteCombobox extends Component {
  static template = xml({ raw: [template] });
  static components = { Input };
  static props = {
    options: { type: Array }, // [{value, label}] from the last search, shown unfiltered
    value: { type: String, optional: true }, // selected option value (highlight only)
    text: { type: String, optional: true }, // label of the current selection, shown when not typing
    placeholder: { type: String, optional: true },
    loading: { type: Boolean, optional: true },
    readonly: { type: Boolean, optional: true },
    clearOnSelect: { type: Boolean, optional: true }, // "add another" pickers reset after a pick
    debounce: { type: Number, optional: true },
    onSearch: { type: Function }, // (query: string) => void
    onChange: { type: Function }, // (value: string) => void
  };

  setup() {
    this.root = useRef("root");
    // query === null means "not typing": the input shows props.text instead.
    this.state = useState({ open: false, query: null, active: -1 });
    this.lastQuery = null;
    onWillUnmount(() => clearTimeout(this.timer));
  }

  get displayValue() {
    return this.state.query ?? this.props.text ?? "";
  }

  get showList() {
    return this.state.open && !this.props.readonly;
  }

  search(query) {
    this.lastQuery = query;
    this.props.onSearch(query);
  }

  onInput(value) {
    this.state.query = value;
    this.state.open = true;
    this.state.active = -1;
    clearTimeout(this.timer);
    this.timer = setTimeout(() => this.search(value), this.props.debounce ?? 250);
  }

  onFocusIn(ev) {
    if (this.props.readonly) return;
    this.state.open = true;
    // Select the shown label so typing replaces it, and drop a stale filtered list from a previous search.
    if (ev.target?.tagName === "INPUT") ev.target.select();
    if (this.lastQuery) this.search("");
  }

  onFocusOut(ev) {
    if (this.root.el?.contains(ev.relatedTarget)) return;
    clearTimeout(this.timer);
    this.state.open = false;
    this.state.query = null;
    this.state.active = -1;
  }

  onKeyDown(ev) {
    const count = this.props.options.length;
    if (ev.key === "ArrowDown" || ev.key === "ArrowUp") {
      ev.preventDefault();
      this.state.open = true;
      if (count === 0) return;
      const step = ev.key === "ArrowDown" ? 1 : -1;
      this.state.active = (this.state.active + step + count) % count;
    } else if (ev.key === "Enter" && this.showList && this.state.active >= 0) {
      ev.preventDefault();
      this.choose(this.props.options[this.state.active]);
    } else if (ev.key === "Escape") {
      this.state.open = false;
      this.state.query = null;
    }
  }

  // mousedown (not click) so the input keeps focus and onFocusOut doesn't close the list first.
  onPick(ev, option) {
    ev.preventDefault();
    this.choose(option);
  }

  choose(option) {
    if (!option) return;
    clearTimeout(this.timer);
    this.state.open = false;
    this.state.query = null;
    this.state.active = -1;
    this.props.onChange(option.value);
    if (this.props.clearOnSelect) this.lastQuery = null;
  }
}
