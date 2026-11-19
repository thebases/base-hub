// TranslateButton is the per-field translation editor (the "EN" badge): it sits beside a
// translate:true field of a saved record and opens a dialog listing one input per active language.
// Values come from a plain `read` with context.lang "*" (the backend then returns the translated
// field as its full {lang: value} map) and the language list from tbl.lang `get_installed`; Save
// sends only changed languages through `update_field_translations`, so untouched languages are
// never rewritten.
import { Component, loadTemplate, useState, xml } from "nodexui/owl";
import { Button, Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle, Input } from "nodexui/component";

import { ResLangRpc, createModelRpc } from "@/services/api/jsonv2.js";
import { normalizeLang } from "@/services/i18n/lang.js";

const template = await loadTemplate(new URL("./TranslateButton.xml", import.meta.url));

export class TranslateButton extends Component {
  static template = xml({ raw: [template] });
  static components = { Button, Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle, Input };
  // props: model, recordId, fieldName, label, uiLang, onSaved(value)

  setup() {
    this.state = useState({ open: false, loading: false, saving: false, error: "", rows: [] });
    // Stored values as loaded, to diff against on save.
    this.initial = {};
  }

  get dialogTitle() {
    return `Translate: ${this.props.label || this.props.fieldName}`;
  }

  // badgeText shows the active UI language (the one the field input edits), e.g. "VI".
  get badgeText() {
    return (this.props.uiLang || "en").split("_")[0].toUpperCase();
  }

  async openDialog() {
    this.state.open = true;
    this.state.loading = true;
    this.state.error = "";
    try {
      const [recordResponse, langResponse] = await Promise.all([
        createModelRpc(this.props.model).callById("read", this.props.recordId, [[this.props.fieldName]], { context: { lang: "*" } }),
        ResLangRpc.call("get_installed"),
      ]);
      const record = [].concat(recordResponse?.result ?? [])[0] ?? {};
      const stored = record[this.props.fieldName];
      // A map is the per-language envelope; anything else (legacy/plain) is the en base value.
      const values = stored && typeof stored === "object" ? stored : { en: stored ?? "" };
      const rows = [{ code: "en", name: "English", value: values.en ?? "" }];
      const seen = new Set(["en"]);
      for (const [code, name] of langResponse?.result ?? []) {
        const key = normalizeLang(code) === "en_us" ? "en" : normalizeLang(code);
        if (seen.has(key)) continue;
        seen.add(key);
        rows.push({ code: key, name, value: values[key] ?? "" });
      }
      // Stored keys of a since-deactivated language stay editable rather than hidden.
      for (const key of Object.keys(values)) {
        if (seen.has(key)) continue;
        seen.add(key);
        rows.push({ code: key, name: key, value: values[key] ?? "" });
      }
      this.initial = Object.fromEntries(rows.map((row) => [row.code, row.value]));
      this.state.rows = rows;
    } catch (error) {
      this.state.error = error?.message || "Could not load translations.";
      this.state.rows = [];
    } finally {
      this.state.loading = false;
    }
  }

  onOpenChange(open) {
    if (!open) this.closeDialog();
  }

  closeDialog() {
    if (this.state.saving) return;
    this.state.open = false;
  }

  onRowInput(row, value) {
    row.value = value;
  }

  // changes builds the {lang: value|null} payload: edited values, and null for a cleared secondary
  // language (en is the required base and cannot be removed, so a cleared en is left out).
  get changes() {
    const out = {};
    for (const row of this.state.rows) {
      const before = this.initial[row.code] ?? "";
      if (row.value === before) continue;
      if (row.value === "") {
        if (row.code !== "en") out[row.code] = null;
        continue;
      }
      out[row.code] = row.value;
    }
    return out;
  }

  get hasChanges() {
    return Object.keys(this.changes).length > 0;
  }

  async save() {
    const changes = this.changes;
    if (this.state.saving || Object.keys(changes).length === 0) return;
    this.state.saving = true;
    this.state.error = "";
    try {
      await createModelRpc(this.props.model).callById("update_field_translations", this.props.recordId, [this.props.fieldName, changes]);
      // The form field shows the active UI language's value: hand it back so the input refreshes.
      const activeKey = normalizeLang(this.props.uiLang) === "en_us" ? "en" : normalizeLang(this.props.uiLang);
      const active = this.state.rows.find((row) => row.code === activeKey) ?? this.state.rows.find((row) => row.code === "en");
      this.props.onSaved?.(active?.value ?? "");
      this.state.open = false;
    } catch (error) {
      this.state.error = error?.message || "Could not save translations.";
    } finally {
      this.state.saving = false;
    }
  }
}
