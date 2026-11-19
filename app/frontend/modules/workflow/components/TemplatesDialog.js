// Templates gallery (T101): lists the template workflows (`list_templates`) and copies one into a
// new workflow (`instantiate_template`, optional name), then hands the new id to the page, which
// opens its canvas once this dialog has finished closing.
import { Component, loadTemplate, onWillUpdateProps, useState, xml } from "nodexui/owl";
import {
  Alert,
  Badge,
  Button,
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  Field,
  Input,
  Spinner,
} from "nodexui/component";

import { fetchWorkflowTemplates, instantiateWorkflowTemplate } from "@/modules/workflow/api.js";
import { reportWorkflowError } from "@/modules/workflow/components/notify.js";

const template = await loadTemplate(new URL("./TemplatesDialog.xml", import.meta.url));

export class TemplatesDialog extends Component {
  static template = xml({ raw: [template] });
  static components = {
    Alert,
    Badge,
    Button,
    Dialog,
    DialogContent,
    DialogDescription,
    DialogFooter,
    DialogHeader,
    DialogTitle,
    Field,
    Input,
    Spinner,
  };

  setup() {
    this.state = useState({ loading: false, error: "", templates: [], name: "", usingID: 0 });
    if (this.props.open) void this.load();
    onWillUpdateProps((nextProps) => {
      if (nextProps.open && !this.props.open) void this.load();
    });
  }

  // load fetches the template list each time the dialog opens.
  async load() {
    this.state.loading = true;
    this.state.error = "";
    this.state.name = "";
    try {
      this.state.templates = await fetchWorkflowTemplates();
    } catch (error) {
      this.state.error = reportWorkflowError(error, t("Failed to load workflow templates."));
    } finally {
      this.state.loading = false;
    }
  }

  triggerTypesText(item) {
    return Array.isArray(item.trigger_types) && item.trigger_types.length ? item.trigger_types.join(", ") : t("webhook");
  }

  // use copies one template into a new workflow and reports its id to the page.
  async use(item) {
    if (this.state.usingID) return;
    this.state.usingID = item.id;
    this.state.error = "";
    try {
      const id = await instantiateWorkflowTemplate(item.id, this.state.name.trim());
      if (!id) throw new Error(t("The template did not return a new workflow."));
      this.props.onCreated(id);
    } catch (error) {
      this.state.error = reportWorkflowError(error, t("Failed to create a workflow from the template."));
    } finally {
      this.state.usingID = 0;
    }
  }
}
