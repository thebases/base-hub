// Form widget `<widget name="address_view" label="Address" street="street" street2="street2"
// city="city" ward="ward_id" state="state_id" country="country_id"/>`: the one-line, read-only address shown in
// view mode (pair it with the editable fields via `context.get('readonly')` invisibles).
// Renders `<street>, <street2>, <ward>, <city>, <state>, <country>`, skipping the empty parts.
import { Component, xml } from "nodexui/owl";

// relationName reads the display name of a many2one value ([id, name] pair or {name} object).
function relationName(raw) {
  if (Array.isArray(raw)) return raw[1] ? String(raw[1]) : "";
  if (raw && typeof raw === "object") return raw.name ? String(raw.name) : "";
  return "";
}

function scalarText(raw) {
  return raw === null || raw === undefined || raw === false ? "" : String(raw).trim();
}

export class AddressView extends Component {
  static template = xml`
    <div class="basehub-address-view d-flex flex-column">
      <small t-if="label" class="text-body-secondary" t-esc="label"/>
      <span class="basehub-address-text" t-esc="text || '—'"/>
    </div>`;
  static props = {
    ctx: { type: Object, optional: true },
    node: { type: Object, optional: true },
    record: { type: Object, optional: true },
    readonly: { type: Boolean, optional: true },
  };

  get attrs() {
    return this.props.node?.attrs ?? {};
  }

  get label() {
    return this.attrs.label ?? "";
  }

  get text() {
    const record = this.props.record ?? {};
    const { street = "street", street2 = "street2", city = "city", ward = "ward_id", state = "state_id", country = "country_id" } = this.attrs;
    return [
      scalarText(record[street]),
      scalarText(record[street2]),
      relationName(record[ward]),
      scalarText(record[city]),
      relationName(record[state]),
      relationName(record[country]),
    ].filter(Boolean).join(", ");
  }
}
