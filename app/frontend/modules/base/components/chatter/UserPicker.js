import { Component, loadTemplate, onMounted, useState, xml } from "nodexui/owl";

import { RemoteCombobox } from "../remote-combobox/RemoteCombobox.js";
import { searchUsers } from "./chatter-rpc.js";

const template = await loadTemplate(new URL("./UserPicker.xml", import.meta.url));

// UserPicker is a chips + "search to add" RemoteCombobox over base.users, the same
// shape FormField.js's x2many widget uses for many2many fields (every keystroke
// runs a backend name_search; remount-via-key resets the typed query after every pick).
// Used both by ChatterComposer (required message receivers) and
// FollowerButton (adding a follower who isn't the current user).
export class UserPicker extends Component {
  static template = xml({ raw: [template] });
  static components = { RemoteCombobox };
  static props = {
    selected: { type: Array }, // [{id, name}]
    onAdd: { type: Function }, // (user: {id, name}) => void
    placeholder: { type: String, optional: true },
  };

  setup() {
    this.state = useState({ options: [], loading: false, comboKey: 0 });
    onMounted(() => void this.loadOptions());
  }

  async loadOptions(query = "") {
    const seq = (this.searchSeq = (this.searchSeq ?? 0) + 1);
    this.state.loading = true;
    try {
      const excludeIds = this.props.selected.map((user) => user.id);
      const options = await searchUsers(query, excludeIds);
      // A slower, older keystroke must not overwrite the newer result set.
      if (seq === this.searchSeq) this.state.options = options;
    } finally {
      if (seq === this.searchSeq) this.state.loading = false;
    }
  }

  get comboOptions() {
    const selectedIds = new Set(this.props.selected.map((user) => String(user.id)));
    return this.state.options.filter((option) => !selectedIds.has(String(option.id))).map((option) => ({ value: String(option.id), label: option.name }));
  }

  onChange(value) {
    if (value === undefined || value === null) return;
    const option = this.state.options.find((item) => String(item.id) === String(value));
    if (option) this.props.onAdd(option);
    // Bump the remount key so the search box reads as "add another" instead
    // of leaving the just-picked user's name behind (Combobox keeps its
    // typed query in local state with no reset API — see FormField.js).
    this.state.comboKey += 1;
  }
}
