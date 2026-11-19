import { Component, onWillStart, onWillUpdateProps, useState, xml } from "nodexui/owl";

import { resolveFormWidget } from "./form-widget-registry.js";

// NamedFormWidget mounts the component registered for a form arch
// `<widget name="..."/>` node (see form-widget-registry.js). Resolution is
// async (lazy import), so it renders nothing until the module has loaded and
// an inline notice when the name is not registered.
export class NamedFormWidget extends Component {
  static template = xml`
    <t t-if="state.component">
      <t t-component="state.component" ctx="props.ctx" node="props.node"
         record="props.ctx.values" readonly="props.ctx.isReadonly"/>
    </t>
    <t t-elif="state.missing">
      <p class="basehub-arch-unsupported"><t t-esc="t('Unknown widget:')"/> <t t-esc="props.node.name"/></p>
    </t>`;
  static props = { node: Object, ctx: Object };

  setup() {
    this.state = useState({ component: null, missing: false });
    onWillStart(() => this.load(this.props.node.name));
    onWillUpdateProps((next) => {
      // Only re-resolve when the arch swapped to a different widget name.
      if (next.node.name !== this.props.node.name) return this.load(next.node.name);
    });
  }

  async load(name) {
    const component = await resolveFormWidget(name);
    this.state.component = component;
    this.state.missing = !component;
  }
}
