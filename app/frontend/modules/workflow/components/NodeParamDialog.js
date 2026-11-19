// Owl port of frontend-admin's NodeParamPanel.tsx — the per-node parameter
// editor opened from a canvas node's "Click to edit parameters" row (and
// "Click to edit trigger settings" for the trigger node).
//
// The React source renders this as a right-hand Sheet; there is no ported Owl
// Sheet, so it uses the app's established pure-Owl Dialog compound API (same
// controlled open/onOpenChange pattern as EdgeTypeDialog.js).
//
// Widget selection follows the param spec's type and secret kind (see
// node-params.js paramWidgetKind); no per-node or per-param-name switch.
import { Component, loadTemplate, onWillUpdateProps, useState, xml } from "nodexui/owl";
import {
  Alert,
  Button,
  Checkbox,
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  Field,
  Input,
  FieldLabel,
  NativeSelect,
  Textarea,
} from "nodexui/component";
import { resolveRelationValue } from "@/modules/base/components/views/form/form-field-normalizer.js";
import { fetchRelationChoices, searchRelationChoices } from "@/services/api/jsonv2.js";

import { fetchWorkflowCredentials, fetchWorkflowModelOptions, fetchWorkflowSysCredentials } from "@/modules/workflow/api.js";
import { applyTriggerDefaults } from "@/modules/workflow/canvas-graph.js";
import {
  SYS_CREDENTIAL_REF_PREFIX,
  applyDraftValuesToAction,
  applyDraftValuesToTrigger,
  buildActionDraftValues,
  buildTriggerDraftValues,
  credentialsForParam,
  isCredentialParam,
  normalizeParamSpecs,
  normalizedParamValue,
  paramSelectionOptions,
  paramWidgetKind,
  relationModelForParam,
  validateRequiredParams,
  validateRequiredTriggerParams,
} from "@/modules/workflow/node-params.js";

const template = await loadTemplate(new URL("./NodeParamDialog.xml", import.meta.url));

export class NodeParamDialog extends Component {
  static template = xml({ raw: [template] });
  static components = {
    Alert,
    Button,
    Checkbox,
    Dialog,
    DialogContent,
    DialogDescription,
    DialogFooter,
    DialogHeader,
    DialogTitle,
    Field,
    Input,
    FieldLabel,
    NativeSelect,
    Textarea,
  };

  setup() {
    this.state = useState({
      name: "",
      description: "",
      catalogId: "",
      triggerType: "",
      onErrorKey: "",
      values: {},
      error: "",
      submitting: false,
      credentials: [],
      sysCredentials: [],
      modelOptions: [],
      relationOptions: {},
    });
    if (this.props.state.open) {
      this.seedFromNode();
      void this.loadPickerData();
    }
    onWillUpdateProps((nextProps) => {
      const wasOpen = Boolean(this.props.state.open);
      const isOpen = Boolean(nextProps.state.open);
      if (isOpen && !wasOpen) {
        this.seedFromNode(nextProps);
        void this.loadPickerData();
      }
    });
  }

  get action() {
    const node = this.props.state.node;
    return node && node.kind === "action" ? node.action : null;
  }

  get trigger() {
    const node = this.props.state.node;
    return node && node.kind === "trigger" ? node.trigger : null;
  }

  get actionSpecs() {
    return this.props.specs?.actions ?? [];
  }

  get triggerSpecs() {
    return this.props.specs?.triggers ?? [];
  }

  // triggerTypeSpecs feeds the Trigger Type select. Once the workflow has an explicit webhook row,
  // the webhook is switched on/off and configured in the trigger list, so the canvas node no longer
  // offers "Webhook" (which would unlink every other transport row on save) unless the node
  // already is the webhook. The rule that a row never changes transport is unchanged.
  get triggerTypeSpecs() {
    if (!this.props.hasWebhookRow || this.trigger?.type === "webhook") return this.triggerSpecs;
    return this.triggerSpecs.filter((spec) => spec.type !== "webhook");
  }

  // resolvedActionSpec follows the live Action Catalog selection so the field
  // list updates immediately after picking a catalog row, instead of only
  // after a save + reload resolves action_code from catalog_id server-side.
  get resolvedActionSpec() {
    const action = this.action;
    if (!action) return undefined;
    const selectedID = Number(this.state.catalogId);
    if (Number.isFinite(selectedID) && selectedID > 0) {
      const matched = this.actionSpecs.find((spec) => Number(spec.catalog_id) === selectedID);
      if (matched) return matched;
    }
    return this.actionSpecs.find((spec) => spec.code === action.action_code);
  }

  // laterStepKeys lists the step keys after this step: the only valid "On error, continue at"
  // targets. Linear workflows only (the select is hidden in DAG mode).
  get laterStepKeys() {
    if (!this.action || this.props.isDag) return [];
    const keys = Array.isArray(this.props.stepKeys) ? this.props.stepKeys : [];
    const index = keys.indexOf(this.action.step_key);
    return index < 0 ? [] : keys.slice(index + 1);
  }

  get activeTriggerSpec() {
    return this.triggerSpecs.find((spec) => spec.type === this.state.triggerType);
  }

  get params() {
    if (this.action) return normalizeParamSpecs(this.resolvedActionSpec?.params);
    if (this.trigger) return normalizeParamSpecs(this.activeTriggerSpec?.params);
    return [];
  }

  get title() {
    if (this.action) return this.resolvedActionSpec?.label ?? this.action.name ?? t("Unrecognized Action");
    return this.activeTriggerSpec?.label ?? t("Node Details");
  }

  // payloadKeys turns the trigger's sample payload into {{field}} chips that
  // append into string params — same affordance as the React panel.
  get payloadKeys() {
    const sampleJSON = this.props.sampleJSON ?? "";
    if (!sampleJSON.trim()) return [];
    try {
      const parsed = JSON.parse(sampleJSON);
      if (parsed && typeof parsed === "object" && !Array.isArray(parsed)) return Object.keys(parsed);
    } catch {
      // invalid JSON — no suggestions
    }
    return [];
  }

  // Accepts an explicit props argument so it can be called from
  // onWillUpdateProps (where `this.props` is still the pre-update value)
  // with the incoming props' node/specs instead.
  seedFromNode(props = this.props) {
    this.state.error = "";
    this.state.submitting = false;
    this.state.modelOptions = [];
    const node = props.state.node;
    const action = node && node.kind === "action" ? node.action : null;
    const trigger = node && node.kind === "trigger" ? node.trigger : null;
    if (action) {
      // Name/description/catalog are populated even when no spec resolves (a
      // broken action_code/catalog binding) so the Action Catalog field stays
      // usable to repair the node.
      this.state.name = action.name;
      this.state.description = action.description;
      const catalogValue = resolveRelationValue(action.catalog_id);
      this.state.catalogId = catalogValue === "" ? "" : String(catalogValue);
      this.state.triggerType = "";
      const actionSpecs = props.specs?.actions ?? [];
      const selectedID = Number(this.state.catalogId);
      let resolvedSpec;
      if (Number.isFinite(selectedID) && selectedID > 0) {
        resolvedSpec = actionSpecs.find((spec) => Number(spec.catalog_id) === selectedID);
      }
      if (!resolvedSpec) resolvedSpec = actionSpecs.find((spec) => spec.code === action.action_code);
      this.state.values = buildActionDraftValues(action, normalizeParamSpecs(resolvedSpec?.params));
      this.state.onErrorKey = String(action.extra_params?.on_error_step_key ?? "");
      return;
    }
    if (trigger) {
      const triggerSpecs = props.specs?.triggers ?? [];
      const spec = triggerSpecs.find((item) => item.type === trigger.type);
      this.state.name = spec?.label ?? "Trigger";
      this.state.description = "";
      this.state.catalogId = "";
      this.state.triggerType = trigger.type;
      this.state.values = buildTriggerDraftValues(
        applyTriggerDefaults(trigger, spec?.params),
        normalizeParamSpecs(spec?.params),
      );
      return;
    }
    this.state.name = "";
    this.state.description = "";
    this.state.catalogId = "";
    this.state.triggerType = "";
    this.state.values = {};
  }

  // Credential/model pickers are best-effort: a failed fetch leaves the field
  // as a plain text input rather than blocking the dialog.
  async loadPickerData() {
    try {
      this.state.credentials = await fetchWorkflowCredentials();
    } catch {
      this.state.credentials = [];
    }
    try {
      this.state.sysCredentials = await fetchWorkflowSysCredentials();
    } catch {
      this.state.sysCredentials = [];
    }
    if (this.params.some((param) => paramWidgetKind(param) === "model")) {
      await this.searchModelOptions("");
    }
    for (const param of this.params) {
      if (paramWidgetKind(param) !== "many2one") continue;
      const relation = relationModelForParam(param.name);
      if (relation) await this.searchRelationOptions(param, "");
    }
  }

  async searchModelOptions(query) {
    try {
      this.state.modelOptions = await fetchWorkflowModelOptions(query);
    } catch {
      this.state.modelOptions = [];
    }
  }

  // searchRelationOptions loads combobox choices for a many2one param (e.g.
  // the webhook trigger's group_id -> tbl.groups). Owl has no ported
  // Combobox, so this backs an <input list>+<datalist> pair like model_name.
  async searchRelationOptions(param, query) {
    const relation = relationModelForParam(param.name);
    if (!relation) return;
    try {
      const options = query.trim() ? await searchRelationChoices(relation, query) : await fetchRelationChoices(relation);
      this.state.relationOptions = { ...this.state.relationOptions, [param.name]: options };
    } catch {
      this.state.relationOptions = { ...this.state.relationOptions, [param.name]: [] };
    }
  }

  relationOptionsFor(param) {
    return this.state.relationOptions[param.name] ?? [];
  }

  relationParamLabel(param) {
    const value = this.paramValue(param);
    const id = resolveRelationValue(value);
    if (id === "" || id === null || id === undefined) return "";
    const match = this.relationOptionsFor(param).find((option) => String(option.id) === String(id));
    return match ? match.name : String(id);
  }

  onRelationParamInput(param, value) {
    const label = value;
    void this.searchRelationOptions(param, label);
    const options = this.relationOptionsFor(param);
    const matched = options.find((option) => option.name === label);
    this.setParamValue(param, matched ? matched.id : label);
  }

  isCatalogSelected(entry) {
    return String(entry.id) === this.state.catalogId;
  }

  paramValue(param) {
    return normalizedParamValue(param, this.state.values[param.name]);
  }

  widgetKind(param) {
    return paramWidgetKind(param);
  }

  selectionOptions(param) {
    return paramSelectionOptions(param);
  }

  showCredentialPicker(param) {
    return isCredentialParam(param) && this.state.credentials.length > 0;
  }

  credentialOptions(param) {
    return credentialsForParam(this.state.credentials, param.name);
  }

  // Stored @syscred:<name> ref -> the bare credential name the <select> binds
  // to. The referenced token itself is never sent to the browser.
  selectedCredentialName(param) {
    const value = String(this.paramValue(param) ?? "").trim();
    return value.startsWith(SYS_CREDENTIAL_REF_PREFIX) ? value.slice(SYS_CREDENTIAL_REF_PREFIX.length) : "";
  }

  setParamValue(param, value) {
    this.state.values = { ...this.state.values, [param.name]: value };
  }

  onParamInput(param, value) {
    this.setParamValue(param, value);
  }

  onParamCheckbox(param, checked) {
    this.setParamValue(param, checked);
  }

  onModelParamInput(param, value) {
    this.setParamValue(param, value);
    void this.searchModelOptions(value);
  }

  onAuthorizationChange(param, value) {
    const name = value;
    this.setParamValue(param, name ? `${SYS_CREDENTIAL_REF_PREFIX}${name}` : "");
  }

  appendPayloadKey(param, key) {
    this.setParamValue(param, `${String(this.paramValue(param) ?? "")}{{${key}}}`);
  }

  pickCredential(param, credentialName) {
    this.setParamValue(param, `@cred:${credentialName}`);
  }

  clearCredential(param) {
    this.setParamValue(param, "");
  }

  onTriggerTypeChange(value) {
    const type = value ?? "";
    const spec = this.triggerSpecs.find((item) => item.type === type);
    const nextTrigger = applyTriggerDefaults({ ...this.trigger, type }, spec?.params);
    this.state.triggerType = type;
    this.state.name = spec?.label ?? "Trigger";
    this.state.values = buildTriggerDraftValues(nextTrigger, normalizeParamSpecs(spec?.params));
    this.state.error = "";
  }

  cancel() {
    this.props.onOpenChange(false);
  }

  deleteNode() {
    const action = this.action;
    if (!action) return;
    this.props.onOpenChange(false);
    void this.props.onDeleteAction(action);
  }

  async submit() {
    this.state.submitting = true;
    this.state.error = "";
    try {
      const action = this.action;
      if (action) {
        const catalogValue = this.state.catalogId === "" ? "" : Number(this.state.catalogId);
        if (catalogValue === "" || !Number.isFinite(catalogValue)) throw new Error(t("Action Catalog is required."));
        const params = this.params;
        const nextAction = applyDraftValuesToAction(
          {
            ...action,
            name: this.state.name.trim() || action.name,
            description: this.state.description.trim(),
            catalog_id: catalogValue,
            // Keep action_code in sync with the resolved catalog spec locally
            // so the node card and spec lookup are right immediately, without
            // waiting for the backend's catalog_id -> action_code related field.
            action_code: this.resolvedActionSpec?.code ?? action.action_code,
          },
          params,
          this.state.values,
        );
        // "On error, continue at" lives in extra_params (empty or DAG mode: none).
        if (this.state.onErrorKey && !this.props.isDag) nextAction.extra_params.on_error_step_key = this.state.onErrorKey;
        else delete nextAction.extra_params.on_error_step_key;
        validateRequiredParams(nextAction, params);
        await this.props.onSaveAction(nextAction);
      } else if (this.trigger && this.activeTriggerSpec) {
        const spec = this.activeTriggerSpec;
        const nextTrigger = applyDraftValuesToTrigger(
          applyTriggerDefaults({ ...this.trigger, type: this.state.triggerType || this.trigger.type }, spec.params),
          spec.params,
          this.state.values,
        );
        validateRequiredTriggerParams(nextTrigger, spec.params);
        await this.props.onSaveTrigger(nextTrigger);
      }
      this.props.onOpenChange(false);
    } catch (error) {
      this.state.error = error instanceof Error ? error.message : t("Failed to save node parameters.");
    } finally {
      this.state.submitting = false;
    }
  }
}
