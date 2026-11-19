// Generic list-view Export dialog (ControlPanel Actions > Export). Lets the
// user pick and order the model's fields, save/load/delete the selection as a
// named sys.model.imex template (kind=false, export), and download every record
// matching the list's current search domain as .xlsx or .csv. With no
// template loaded the selection defaults to the model's required writable
// fields. Headers follow imex-fields.js's column contract so the file can be
// re-imported through ImportDialog.
import { Component, loadTemplate, onWillUpdateProps, useState, xml } from "nodexui/owl";
import { Alert, Button, Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle, Icon, Input, NativeSelect } from "nodexui/component";

import { columnHeaders, defaultFieldNames, exportableFieldNames, fieldLabel, formatExportValue } from "./imex-fields.js";
import { EXPORT_MAX_ROWS, IMEX_KIND_EXPORT, deleteTemplate, fetchExportRecords, listTemplates, saveTemplate } from "./imex-api.js";
import { downloadCsv, downloadXlsx } from "./imex-sheet.js";

const template = await loadTemplate(new URL("./ExportDialog.xml", import.meta.url));

export class ExportDialog extends Component {
  static template = xml({ raw: [template] });
  static components = { Alert, Button, Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle, Icon, Input, NativeSelect };
  static props = {
    state: Object,
    model: String,
    title: { type: String, optional: true },
    fields: Object,
    domain: { type: Array, optional: true },
    total: { type: Number, optional: true },
    onOpenChange: Function,
    onExported: { type: Function, optional: true },
  };

  setup() {
    this.state = useState(this.emptyState(this.props.fields));
    if (this.props.state.open) void this.loadTemplates(this.props.model);
    // Reset per open (false -> true) so a reopened dialog starts from defaults.
    onWillUpdateProps((nextProps) => {
      if (nextProps.state.open && !this.props.state.open) {
        Object.assign(this.state, this.emptyState(nextProps.fields));
        void this.loadTemplates(nextProps.model);
      }
    });
  }

  emptyState(fields) {
    return {
      templates: [],
      templateId: "",
      templateName: "",
      selected: defaultFieldNames(fields),
      search: "",
      format: "xlsx",
      error: "",
      exporting: false,
      savingTemplate: false,
    };
  }

  async loadTemplates(model) {
    try {
      this.state.templates = await listTemplates(model, IMEX_KIND_EXPORT);
    } catch (error) {
      this.state.templates = [];
      this.state.error = error instanceof Error ? error.message : t("Failed to load templates");
    }
  }

  get scopeSummary() {
    const total = this.props.total ?? 0;
    const one = total === 1;
    const filtered = Boolean(this.props.domain?.length);
    const capped = total > EXPORT_MAX_ROWS;
    const params = { count: total, max: EXPORT_MAX_ROWS };
    // One whole sentence per combination: sentences are never assembled from fragments, so each can be translated.
    if (filtered && capped) {
      return one
        ? t("Export {count} record matching the current search (first {max}) to a spreadsheet.", params)
        : t("Export {count} records matching the current search (first {max}) to a spreadsheet.", params);
    }
    if (filtered) {
      return one
        ? t("Export {count} record matching the current search to a spreadsheet.", params)
        : t("Export {count} records matching the current search to a spreadsheet.", params);
    }
    if (capped) {
      return one
        ? t("Export {count} record (first {max}) to a spreadsheet.", params)
        : t("Export {count} records (first {max}) to a spreadsheet.", params);
    }
    return one ? t("Export {count} record to a spreadsheet.", params) : t("Export {count} records to a spreadsheet.", params);
  }

  // availableFields filters by label or technical name; checked state mirrors
  // the ordered selection.
  get availableFields() {
    const query = this.state.search.trim().toLowerCase();
    const fields = this.props.fields;
    return exportableFieldNames(fields)
      .map((name) => ({ name, label: fieldLabel(fields, name), selected: this.state.selected.includes(name) }))
      .filter((field) => !query || field.label.toLowerCase().includes(query) || field.name.toLowerCase().includes(query));
  }

  get selectedFields() {
    return this.state.selected.map((name) => ({ name, label: fieldLabel(this.props.fields, name) }));
  }

  get loadedTemplate() {
    return this.state.templates.find((tpl) => String(tpl.id) === this.state.templateId);
  }

  // isOverwrite: saving keeps the loaded template when its name is unchanged;
  // a new name always creates a new template.
  get isOverwrite() {
    const loaded = this.loadedTemplate;
    return Boolean(loaded) && loaded.name === this.state.templateName.trim();
  }

  get canSaveTemplate() {
    return Boolean(this.state.templateName.trim()) && this.state.selected.length > 0 && !this.state.savingTemplate;
  }

  get canExport() {
    return this.state.selected.length > 0 && !this.state.exporting;
  }

  setSearch(value) {
    this.state.search = value;
  }

  setTemplateName(value) {
    this.state.templateName = value;
  }

  setFormat(value) {
    this.state.format = value === "csv" ? "csv" : "xlsx";
  }

  toggleField(name) {
    const selected = this.state.selected;
    this.state.selected = selected.includes(name) ? selected.filter((item) => item !== name) : [...selected, name];
  }

  moveField(index, delta) {
    const next = [...this.state.selected];
    const target = index + delta;
    if (target < 0 || target >= next.length) return;
    [next[index], next[target]] = [next[target], next[index]];
    this.state.selected = next;
  }

  resetToDefault() {
    Object.assign(this.state, { templateId: "", templateName: "", selected: defaultFieldNames(this.props.fields) });
  }

  // loadTemplateById restores a saved selection; field names that no longer
  // exist on the model (renamed/removed since saving) are dropped.
  loadTemplateById(value) {
    if (!value) {
      this.resetToDefault();
      return;
    }
    const tpl = this.state.templates.find((item) => String(item.id) === value);
    if (!tpl) return;
    const known = tpl.field_names.filter((name) => name === "id" || this.props.fields[name]);
    Object.assign(this.state, { templateId: value, templateName: tpl.name, selected: known, error: "" });
  }

  async saveCurrentTemplate() {
    if (!this.canSaveTemplate) return;
    this.state.savingTemplate = true;
    this.state.error = "";
    try {
      const id = await saveTemplate({
        id: this.isOverwrite ? this.loadedTemplate.id : undefined,
        name: this.state.templateName.trim(),
        model: this.props.model,
        kind: IMEX_KIND_EXPORT,
        fieldNames: [...this.state.selected],
      });
      await this.loadTemplates(this.props.model);
      this.state.templateId = id ? String(id) : "";
    } catch (error) {
      this.state.error = error instanceof Error ? error.message : t("Failed to save the template");
    } finally {
      this.state.savingTemplate = false;
    }
  }

  async deleteCurrentTemplate() {
    const loaded = this.loadedTemplate;
    if (!loaded || !window.confirm(t('Delete template "{name}"?', { name: loaded.name }))) return;
    this.state.savingTemplate = true;
    try {
      await deleteTemplate(loaded.id);
      this.resetToDefault();
      await this.loadTemplates(this.props.model);
    } catch (error) {
      this.state.error = error instanceof Error ? error.message : t("Failed to delete the template");
    } finally {
      this.state.savingTemplate = false;
    }
  }

  // exportFile reads all matching records with only the chosen fields and
  // writes them in the chosen order.
  async exportFile() {
    if (!this.canExport) return;
    this.state.exporting = true;
    this.state.error = "";
    try {
      const fields = this.props.fields;
      const names = [...this.state.selected];
      const records = await fetchExportRecords(this.props.model, this.props.domain ?? [], names);
      const headers = columnHeaders(fields, names);
      const rows = records.map((record) => names.map((name) => formatExportValue(fields[name], record[name])));
      const stamp = new Date().toISOString().slice(0, 10);
      const fileName = `${this.props.model.replace(/\./g, "_")}_${stamp}.${this.state.format}`;
      if (this.state.format === "csv") downloadCsv({ headers, rows, fileName });
      else await downloadXlsx({ sheetName: this.props.title || this.props.model, headers, rows, fileName });
      this.props.onExported?.(records.length);
      this.props.onOpenChange(false);
    } catch (error) {
      this.state.error = error instanceof Error ? error.message : t("Export failed");
    } finally {
      this.state.exporting = false;
    }
  }

  close() {
    this.props.onOpenChange(false);
  }

  onOpenChange(open) {
    if (!open && this.state.exporting) return;
    this.props.onOpenChange(open);
  }
}
