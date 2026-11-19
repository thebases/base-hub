// Collects the declared kwargs (RuntimeMethod.Params) of a sys.model.action
// "model" entry before ModelView runs the method. Controlled via
// `props.state` ({open, title, description, params}) + `props.onOpenChange`/
// `props.onSubmit(kwargs)`, same contract as UserSecurityPasswordDialog.js.
// Cancel only closes; Submit validates required inputs, coerces each value
// to its declared type and awaits onSubmit — a rejection keeps the dialog
// open with the error so the user can fix the input and retry.
import { Component, loadTemplate, onWillUpdateProps, useState, xml } from "nodexui/owl";
import { Alert, Button, Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, Field, Input, NativeSelect, Switch, Textarea } from "nodexui/component";

const template = await loadTemplate(new URL("./ModelActionParamsDialog.xml", import.meta.url));

// HTML input type per declared param type (text/selection/boolean have their
// own controls in the template).
const INPUT_TYPES = { integer: "number", float: "number", date: "date", datetime: "datetime-local", password: "password" };

export class ModelActionParamsDialog extends Component {
  static template = xml({ raw: [template] });
  static components = { Alert, Button, Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, Field, Input, NativeSelect, Switch, Textarea };

  setup() {
    this.state = useState({ values: {}, error: "", submitting: false });
    // Seed the inputs from each param's default exactly once per open, so a
    // reopened popup never shows the previous run's values.
    onWillUpdateProps((nextProps) => {
      if (nextProps.state.open && !this.props.state.open) {
        this.state.values = initialValues(nextProps.state.params);
        this.state.error = "";
        this.state.submitting = false;
      }
    });
  }

  get params() {
    return Array.isArray(this.props.state.params) ? this.props.state.params : [];
  }

  inputID(param) {
    return `model-action-param-${param.name}`;
  }

  inputType(param) {
    return INPUT_TYPES[param.type] || "text";
  }

  stepFor(param) {
    return param.type === "integer" ? "1" : "any";
  }

  isSelected(param, option) {
    return this.state.values[param.name] === option.value;
  }

  setValue(name, value) {
    this.state.values[name] = value;
  }

  onOpenChange(open) {
    if (!open && !this.state.submitting) this.props.onOpenChange(false);
  }

  cancel() {
    this.props.onOpenChange(false);
  }

  async submit() {
    const { kwargs, error } = buildKwargs(this.params, this.state.values);
    if (error) {
      this.state.error = error;
      return;
    }
    this.state.submitting = true;
    this.state.error = "";
    try {
      await this.props.onSubmit(kwargs);
      this.props.onOpenChange(false);
    } catch (submitError) {
      this.state.error = submitError instanceof Error ? submitError.message : t("Action failed");
    } finally {
      this.state.submitting = false;
    }
  }
}

// initialValues maps each param to its editable starting value: booleans as
// real booleans (Switch), everything else as the string an input holds.
function initialValues(params) {
  const values = {};
  for (const param of Array.isArray(params) ? params : []) {
    if (param.type === "boolean") {
      values[param.name] = param.default === true;
      continue;
    }
    values[param.name] = param.default === null || param.default === undefined ? "" : String(param.default);
  }
  return values;
}

// buildKwargs coerces the raw inputs into the JSON types the backend's
// ValidateRuntimeMethodKwargs expects. Blank optional inputs are omitted so
// the method sees "not provided" rather than an empty string; the first
// missing/invalid required input is reported instead of submitting.
function buildKwargs(params, values) {
  const kwargs = {};
  for (const param of params) {
    const raw = values[param.name];
    if (param.type === "boolean") {
      kwargs[param.name] = raw === true;
      continue;
    }
    const text = typeof raw === "string" ? raw.trim() : "";
    if (!text) {
      if (param.required) return { kwargs, error: t("{label} is required.", { label: param.label }) };
      continue;
    }
    if (param.type === "integer" || param.type === "float") {
      const number = Number(text);
      if (!Number.isFinite(number) || (param.type === "integer" && !Number.isInteger(number))) {
        return { kwargs, error: param.type === "integer" ? t("{label} must be a valid whole number.", { label: param.label }) : t("{label} must be a valid number.", { label: param.label }) };
      }
      kwargs[param.name] = number;
      continue;
    }
    // text and password keep the exact input (whitespace is meaningful);
    // other string types are trimmed.
    kwargs[param.name] = param.type === "text" || param.type === "password" ? raw : text;
  }
  return { kwargs, error: "" };
}
