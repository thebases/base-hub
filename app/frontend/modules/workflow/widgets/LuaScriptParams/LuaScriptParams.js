// Form widget `<widget name="workflow_lua_script" field="extra_params"/>` for
// base.workflow.action rows running lua_script@v1. The node never carries Lua
// source: scripts are written in base-automation (Automation > Actions) and
// the node selects one as `extra_params.automation_action_id`. It also holds
// the node's custom JSON (`extra_params.data`), which the script reads as the
// global `data` next to `payload` (the previous node's output).
// Arch attributes: `field` (default extra_params), `string` (optional heading).
import { Component, loadTemplate, onWillStart, useState, xml } from "nodexui/owl";

import { Field, NativeSelect, Textarea } from "nodexui/component";
import { createModelRpc } from "@/services/api/jsonv2.js";
import { parseLuaParams } from "@/modules/workflow/node-params.js";

const template = await loadTemplate(new URL("./LuaScriptParams.xml", import.meta.url));

export class LuaScriptParams extends Component {
  static template = xml({ raw: [template] });
  static components = { Field, NativeSelect, Textarea };
  static props = {
    ctx: { type: Object, optional: true },
    node: { type: Object, optional: true },
    record: { type: Object, optional: true },
    readonly: { type: Boolean, optional: true },
  };

  setup() {
    this.onActionChange = this.onActionChange.bind(this);
    this.onDataInput = this.onDataInput.bind(this);
    // dataDraft is the textarea text; it is committed to extra_params only while it parses as an
    // object, so half-typed JSON is never written back (and never discards sibling keys).
    this.state = useState({ actions: [], dataDraft: null, dataError: "" });
    onWillStart(async () => {
      try {
        const response = await createModelRpc("automation.action").search([["action_type", "=", "lua"]], ["id", "name"], "name ASC", 0, 200, false);
        this.state.actions = Array.isArray(response.result) ? response.result : [];
      } catch {
        this.state.actions = [];
      }
    });
  }

  get attrs() {
    return this.props.node?.attrs ?? {};
  }

  get fieldName() {
    return this.attrs.field || "extra_params";
  }

  get params() {
    return parseLuaParams(this.props.record?.[this.fieldName]);
  }

  // invalidParams is true while the raw JSON editor holds unparsable text;
  // editing is blocked then, since writing back would discard that text.
  get invalidParams() {
    return this.params === null;
  }

  get isReadonly() {
    return Boolean(this.props.readonly) || this.invalidParams;
  }

  get title() {
    return this.attrs.string ?? t("Lua Script");
  }

  // selectedActionId is the automation_action_id stored in extra_params ("" = none selected).
  get selectedActionId() {
    const id = this.params?.automation_action_id;
    return id ? String(id) : "";
  }

  // actionOptions precomputes string option values (Owl templates cannot call String()).
  get actionOptions() {
    return this.state.actions.map((action) => ({ value: String(action.id), label: action.name }));
  }

  get dataText() {
    if (this.state.dataDraft !== null) return this.state.dataDraft;
    return JSON.stringify(this.params?.data ?? {}, null, 2);
  }

  commit(next) {
    this.props.ctx?.onFieldChange?.(this.fieldName, next);
  }

  // onActionChange stores the picked action id, or removes the key when cleared.
  onActionChange(value) {
    const next = { ...(this.params ?? {}) };
    const id = Number(value);
    if (Number.isInteger(id) && id > 0) next.automation_action_id = id;
    else delete next.automation_action_id;
    this.commit(next);
  }

  // onDataInput keeps the draft text and commits the parsed object once it is valid JSON.
  onDataInput(text) {
    this.state.dataDraft = text;
    try {
      const parsed = text.trim() === "" ? {} : JSON.parse(text);
      if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) throw new Error(t("Custom data must be a JSON object"));
      this.state.dataError = "";
      this.commit({ ...(this.params ?? {}), data: parsed });
    } catch (error) {
      this.state.dataError = error.message;
    }
  }
}
