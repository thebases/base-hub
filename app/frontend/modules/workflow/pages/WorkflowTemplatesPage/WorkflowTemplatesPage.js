// Page /web/workflow/templates — target of the "Workflow Templates" menu. The SPA opens every
// /web/<model> URL from the model alone (ModelView never applies a ui.actions.window domain or
// view_id), so a menu on /web/base.workflow listed every workflow; this page shows only the
// template gallery (`list_templates`) and opens the new workflow's canvas after "Use template".
import { Component, loadTemplate, useState, xml } from "nodexui/owl";
import { Button } from "nodexui/component";

import { navigate } from "@/router/navigation.js";
import { TemplatesDialog } from "@/modules/workflow/components/TemplatesDialog.js";

const template = await loadTemplate(new URL("./WorkflowTemplatesPage.xml", import.meta.url));

export class WorkflowTemplatesPage extends Component {
  static template = xml({ raw: [template] });
  static components = { Button, TemplatesDialog };
  static props = { params: { type: Object, optional: true } };

  setup() {
    this.state = useState({ open: true });
  }

  // openCanvas shows the workflow just copied from a template.
  openCanvas(workflowID) {
    this.state.open = false;
    navigate(`/web/base.workflow/canvas/${workflowID}`);
  }
}
