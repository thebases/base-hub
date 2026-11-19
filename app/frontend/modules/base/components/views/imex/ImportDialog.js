// Generic list-view Import dialog (ControlPanel Actions > Import). Same flow
// and layout as the EDC DevicesImportPage, but driven by the model's
// fields_get metadata: 1) download a template whose columns come from a saved
// sys.model.imex template or, by default, the model's required writable
// fields; 2) pick a .xlsx/.csv that is header-checked client-side with a
// 5-row preview; 3) submit to sys.model.imex.import_records, which upserts by
// ID in one transaction. Controlled via `props.state.open` +
// `props.onOpenChange`, like the other ModelView dialogs.
import { Component, loadTemplate, onWillUpdateProps, useRef, useState, xml } from "nodexui/owl";
import { Alert, Button, Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle, Icon, NativeSelect } from "nodexui/component";

import { columnHeaders, defaultFieldNames, isImportable, isRequiredOnCreate, parseImportRows } from "./imex-fields.js";
import { importRecords, listTemplates } from "./imex-api.js";
import { downloadCsv, downloadXlsx, readSheetRows } from "./imex-sheet.js";

const template = await loadTemplate(new URL("./ImportDialog.xml", import.meta.url));

const PREVIEW_LIMIT = 5;
const MAX_VISIBLE_ERRORS = 20;

export class ImportDialog extends Component {
  static template = xml({ raw: [template] });
  static components = { Alert, Button, Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle, Icon, NativeSelect };
  static props = {
    state: Object,
    model: String,
    title: { type: String, optional: true },
    fields: Object,
    onOpenChange: Function,
    onImported: { type: Function, optional: true },
  };

  setup() {
    this.fileInput = useRef("fileInput");
    this.state = useState(this.emptyState());
    if (this.props.state.open) void this.loadTemplates(this.props.model);
    // Reset per open (false -> true), mirroring DatabaseDuplicateDialog.
    onWillUpdateProps((nextProps) => {
      if (nextProps.state.open && !this.props.state.open) {
        Object.assign(this.state, this.emptyState());
        void this.loadTemplates(nextProps.model);
      }
    });
  }

  emptyState() {
    return { templates: [], templateId: "", fileName: "", rows: [], columns: [], errors: [], processing: false, submitting: false, downloading: false };
  }

  // loadTemplates is best-effort: without templates the default columns apply.
  async loadTemplates(model) {
    try {
      this.state.templates = await listTemplates(model);
    } catch {
      this.state.templates = [];
    }
  }

  // templateFieldNames: the selected template's importable fields, else the
  // default required set. Required fields are always appended so a
  // downloaded template can create records.
  get templateFieldNames() {
    const fields = this.props.fields;
    const selected = this.state.templates.find((tpl) => String(tpl.id) === this.state.templateId);
    const base = selected ? selected.field_names.filter((name) => isImportable(fields, name)) : defaultFieldNames(fields);
    const required = defaultFieldNames(fields).filter((name) => isRequiredOnCreate(fields, name) && !base.includes(name));
    return [...base, ...required];
  }

  get templateColumns() {
    const names = this.templateFieldNames;
    const headers = columnHeaders(this.props.fields, names);
    return names.map((name, index) => ({ name, header: headers[index], required: isRequiredOnCreate(this.props.fields, name) }));
  }

  get previewSlice() {
    return this.state.rows.slice(0, PREVIEW_LIMIT);
  }

  get visibleErrors() {
    const errors = this.state.errors;
    if (errors.length <= MAX_VISIBLE_ERRORS) return errors;
    return [...errors.slice(0, MAX_VISIBLE_ERRORS), t("…and {count} more.", { count: errors.length - MAX_VISIBLE_ERRORS })];
  }

  get isValid() {
    return this.state.rows.length > 0 && this.state.errors.length === 0;
  }

  get validSummary() {
    return t("Parsed {count} valid rows", { count: this.state.rows.length });
  }

  get canSubmit() {
    return this.isValid && !this.state.processing && !this.state.submitting;
  }

  get fileBaseName() {
    return `${this.props.model.replace(/\./g, "_")}_import_template`;
  }

  selectTemplate(value) {
    this.state.templateId = value;
  }

  // downloadTemplate writes a header-only file; xlsx lazy-loads ExcelJS, so
  // load failures surface in the error panel instead of failing silently.
  async downloadTemplate(format) {
    const headers = this.templateColumns.map((column) => column.header);
    if (format === "csv") {
      downloadCsv({ headers, rows: [], fileName: `${this.fileBaseName}.csv` });
      return;
    }
    this.state.downloading = true;
    try {
      await downloadXlsx({ sheetName: this.props.title || this.props.model, headers, rows: [], fileName: `${this.fileBaseName}.xlsx` });
    } catch (error) {
      this.state.errors = [error instanceof Error ? error.message : t("Failed to build the template")];
    } finally {
      this.state.downloading = false;
    }
  }

  pickFile() {
    this.fileInput.el?.click();
  }

  // onFileChange parses the chosen file, then clears the native input so
  // re-picking the same file after fixing it fires `change` again.
  async onFileChange(ev) {
    const file = ev.target.files?.[0];
    ev.target.value = "";
    if (file) await this.parseFile(file);
  }

  async onDrop(ev) {
    const file = ev.dataTransfer?.files?.[0];
    if (file) await this.parseFile(file);
  }

  async parseFile(file) {
    Object.assign(this.state, { processing: true, errors: [], rows: [], columns: [], fileName: file.name });
    try {
      const sheetRows = await readSheetRows(file);
      const result = sheetRows ? parseImportRows(this.props.fields, sheetRows) : { rows: [], columns: [], errors: [t("The uploaded file has no worksheet.")] };
      Object.assign(this.state, { rows: result.rows, columns: result.columns, errors: result.errors });
    } catch {
      this.state.errors = [t("The selected file could not be read. Please use the provided template.")];
    } finally {
      this.state.processing = false;
    }
  }

  // submit sends the validated batch; the server is all-or-nothing, so a
  // failure leaves the dialog open with the row-level reason.
  async submit() {
    if (!this.canSubmit) return;
    this.state.submitting = true;
    try {
      const result = await importRecords(this.props.model, this.state.rows);
      this.props.onImported?.(result);
      this.props.onOpenChange(false);
    } catch (error) {
      this.state.errors = [error instanceof Error ? error.message : t("Import failed")];
    } finally {
      this.state.submitting = false;
    }
  }

  close() {
    this.props.onOpenChange(false);
  }

  // onOpenChange blocks backdrop/Escape dismissal mid-import.
  onOpenChange(open) {
    if (!open && this.state.submitting) return;
    this.props.onOpenChange(open);
  }
}
