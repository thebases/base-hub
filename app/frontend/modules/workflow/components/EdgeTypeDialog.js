// Owl port of WorkflowCanvas.tsx's EdgeTypeDialog: sets one DAG edge's
// edge_type and, for edge_type=="condition", its condition_expr — the same
// JSON condition-object shape step.Params["condition"] already uses on the
// backend, not a separate expression syntax.
import { Component, loadTemplate, onWillUpdateProps, useState, xml } from "nodexui/owl";
import { Button, Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle, Field, NativeSelect, Textarea } from "nodexui/component";


const template = await loadTemplate(new URL("./EdgeTypeDialog.xml", import.meta.url));

export class EdgeTypeDialog extends Component {
  static template = xml({ raw: [template] });
  static components = { Button, Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle, Field, NativeSelect, Textarea };

  setup() {
    this.state = useState({ edgeType: "success", conditionExpr: "" });
    onWillUpdateProps((nextProps) => {
      const wasOpen = Boolean(this.props.state.edge);
      const willOpen = Boolean(nextProps.state.edge);
      if (willOpen && !wasOpen) {
        this.state.edgeType = nextProps.state.edge.edge_type || "success";
        this.state.conditionExpr = nextProps.state.edge.condition_expr || "";
      }
    });
  }

  get isDialogOpen() {
    return Boolean(this.props.state.edge);
  }

  get isConditionType() {
    return this.state.edgeType === "condition";
  }

  get conditionParseError() {
    if (!this.isConditionType || !this.state.conditionExpr.trim()) return "";
    try {
      JSON.parse(this.state.conditionExpr);
      return "";
    } catch {
      return t("condition_expr must be valid JSON.");
    }
  }

  cancel() {
    this.props.onOpenChange(false);
  }

  async save() {
    const edge = this.props.state.edge;
    if (!edge || this.conditionParseError) return;
    await this.props.onSave(edge.id, this.state.edgeType, this.isConditionType ? this.state.conditionExpr : "");
  }
}
