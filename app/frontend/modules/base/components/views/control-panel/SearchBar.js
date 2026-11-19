// SearchBar — port of frontend-admin's control-panel/search-bar.tsx. Free
// text is committed as a facet chip against the active field; Enter commits,
// Backspace on an empty input drops the last facet.
import { Component, loadTemplate, useState, xml } from "nodexui/owl";
import { NativeSelect } from "nodexui/component";


const template = await loadTemplate(new URL("./SearchBar.xml", import.meta.url));

export class SearchBar extends Component {
  static template = xml({ raw: [template] });
  static components = { NativeSelect };
  static props = {
    placeholder: { type: String, optional: true },
    fields: { type: Array, optional: true },
    activeField: { type: String, optional: true },
    facets: { type: Array, optional: true },
    onActiveFieldChange: { type: Function, optional: true },
    onSearch: { type: Function, optional: true },
    onRemoveFacet: { type: Function, optional: true },
    onClearAll: { type: Function, optional: true },
  };
  static defaultProps = { placeholder: t("Search…"), fields: [], facets: [] };

  setup() {
    this.state = useState({ inputValue: "" });
  }

  get resolvedActiveField() {
    if (this.props.activeField) return this.props.activeField;
    // Default to the record's natural name, not whatever column comes first (often an image or flag).
    const fields = this.props.fields;
    const preferred = ["name", "complete_name", "display_name", "title", "login", "key"];
    const match = preferred.map((name) => fields.find((field) => field.name === name)).find(Boolean);
    return match?.name || fields[0]?.name || "name";
  }

  onFieldChange(value) {
    this.props.onActiveFieldChange?.(value);
  }

  onInput(event) {
    this.state.inputValue = event.target.value;
  }

  commitSearch() {
    const trimmed = this.state.inputValue.trim();
    if (!trimmed) return;
    this.props.onSearch?.(trimmed, this.resolvedActiveField);
    this.state.inputValue = "";
  }

  onKeyDown(event) {
    if (event.key === "Enter") {
      this.commitSearch();
      return;
    }
    if (event.key === "Backspace" && this.state.inputValue === "" && this.props.facets.length > 0) {
      this.props.onRemoveFacet?.(this.props.facets[this.props.facets.length - 1].id);
    }
  }

  onRemoveFacet(id) {
    this.props.onRemoveFacet?.(id);
  }

  onClearAll() {
    this.props.onClearAll?.();
  }
}
