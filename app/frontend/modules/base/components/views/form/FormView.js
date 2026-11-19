import { Component, loadTemplate, onMounted, onPatched, onWillUpdateProps, useRef, useState, xml } from "nodexui/owl";

import { parseFormArch } from "./form-arch-parser.js";
import { collectArchFieldDomains, evaluateArchDomain } from "./form-domain.js";
import { isArchNodeVisible } from "./form-arch-eval.js";
import { resolveArchFieldState } from "./form-arch-field-state.js";
import {
  isJSONField,
  normalizeEmptyFieldValue,
  normalizeSubmitFieldValue,
  shouldRenderField,
} from "./form-field-normalizer.js";
import { ArchNode } from "@/modules/base/components/views/ArchNode.js";
import { ChatterPanel, readChatterCollapsed, writeChatterCollapsed } from "@/modules/base/components/chatter/ChatterPanel.js";
import { chatterFitsAsSidebar, layoutStore } from "@/stores/layout-store.js";
import { FormField } from "./FormField.js";
import {
  Alert,
  Badge,
  Button,
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
  Icon,
  Sheet,
  SheetContent,
  Tooltip,
} from "nodexui/component";

const template = await loadTemplate(new URL("./FormView.xml", import.meta.url));

// collectVisibleArchFields walks the arch for the fields the user can actually
// see — required/JSON validation must not fire for fields an invisible
// expression is hiding.
function collectVisibleArchFields(arch, fieldMap, evalContext, isCreateMode) {
  const visibleFields = new Map();
  const visit = (node) => {
    if (!isArchNodeVisible(node.invisible, evalContext)) return;
    if (node.type === "field") {
      const { field } = resolveArchFieldState({ node, evalContext, baseField: fieldMap[node.name] });
      if (shouldRenderField(field, isCreateMode)) visibleFields.set(field.name, field);
      return;
    }
    if ("children" in node) node.children.forEach(visit);
    if (node.type === "notebook") node.pages.forEach(visit);
  };
  arch.children.forEach(visit);
  return Array.from(visibleFields.values());
}

function collectArchFieldInputIDs(arch) {
  const fieldInputIDs = {};
  const visit = (node) => {
    if (node.type === "field") {
      fieldInputIDs[node.name] = node.id ?? `field-${node.name}`;
      node.list?.fields.forEach((child) => {
        fieldInputIDs[child.name] = child.id ?? `field-${child.name}`;
      });
      return;
    }
    if ("children" in node) node.children.forEach(visit);
    if (node.type === "notebook") node.pages.forEach(visit);
  };
  arch.children.forEach(visit);
  return fieldInputIDs;
}

// rootColClassFor gives top-level form children the same stretch wrapper used
// inside sheet/page nodes. Otherwise root groups can size to their content
// instead of occupying their flex-basis span.
function rootColClassFor(node) {
  const raw = node.colspan ?? (node.attrs?.colspan ? Number.parseInt(node.attrs.colspan, 10) : undefined);
  if (raw !== undefined && Number.isFinite(raw)) {
    return `basehub-arch-col basehub-arch-span-${Math.min(Math.max(raw, 1), 8)}`;
  }
  if (node.type === "group" && node.children?.some((child) => child.type === "group")) return "basehub-arch-col basehub-arch-span-8";
  return `basehub-arch-col ${node.type === "group" ? "basehub-arch-span-4" : "basehub-arch-span-8"}`;
}

export class FormView extends Component {
  static template = xml({ raw: [template] });

  setup() {
    this.root = useRef("root");
    this.state = useState({
      values: {},
      status: "saved",
      editing: true,
      saveError: "",
      isSaving: false,
      deleteDialogOpen: false,
      chatterCollapsed: readChatterCollapsed(this.props.model),
      chatterFits: chatterFitsAsSidebar(),
      chatterSheetOpen: false,
    });
    this.resetFromProps(this.props);
    onWillUpdateProps((nextProps) => this.resetFromProps(nextProps));
    // BaseUI's autoMount() only scans once at page load, so every render pass
    // that emits new b-ui markup (fields, tabs, the delete dialog)
    // needs an explicit mountAll — mount() is idempotent per element.
    onMounted(() => window.thebase?.mountAll(this.root.el));
    onPatched(() => window.thebase?.mountAll(this.root.el));
    // The docked chatter panel only fits beside the form when the left
    // sidebar is collapsed or the viewport is wide (see layout-store.js);
    // when it doesn't fit, chatter instead opens in a right-hand Sheet (see
    // showChatterSheet/toggleChatter) so it's never hidden entirely.
    this.unsubscribeLayout = layoutStore.subscribe((next) => {
      const fits = chatterFitsAsSidebar(next);
      this.state.chatterFits = fits;
      // Regaining room for the docked sidebar (sidebar collapsed or viewport
      // widened) should fold the sheet back into the sidebar, not leave both.
      if (fits) this.state.chatterSheetOpen = false;
    });
  }

  willUnmount() {
    this.unsubscribeLayout?.();
  }

  // resetFromProps rebuilds the editable snapshot whenever the loaded record
  // changes; defaults run through the same normalizer as the submit pipeline
  // so the save diff compares like with like.
  resetFromProps(props) {
    const defaults = {};
    for (const field of props.fields) {
      defaults[field.name] = normalizeEmptyFieldValue(field, props.record?.[field.name] ?? "");
    }
    this.defaultValues = defaults;
    this.parsedArch = props.viewData.arch?.trim() ? parseFormArch(props.viewData.arch) : undefined;
    this.archFieldDomains = this.parsedArch ? collectArchFieldDomains(this.parsedArch) : {};
    this.fieldInputIDs = this.parsedArch ? collectArchFieldInputIDs(this.parsedArch) : {};
    this.state.values = { ...defaults };
    this.state.status = props.isNew ? "new" : "saved";
    this.state.saveError = "";
    this.state.isSaving = false;
  }

  get fieldMap() {
    return Object.fromEntries(this.props.fields.map((field) => [field.name, field]));
  }

  // Existing records open straight in editor mode (state.editing defaults to true); the
  // read-only layout only applies when something sets editing to false.
  get isReadonly() {
    return this.state.status === "saved" && !this.state.editing;
  }

  // showSave: in edit mode the Save button only appears once the form is dirty (or the record is
  // new); a pristine saved record has nothing to persist.
  get showSave() {
    return this.state.status !== "saved";
  }

  // showDiscard: Discard only matters once there is something to throw away (unsaved edits) or a
  // new record to abandon.
  get showDiscard() {
    return this.state.status !== "saved";
  }

  // isPristine: a loaded record with no unsaved edits, whether or not it is shown editable.
  get isPristine() {
    return this.state.status === "saved";
  }

  // Actions-menu visibility: Duplicate only on a saved record, Delete on any
  // existing record, bound actions in read mode only (never on unsaved edits).
  get showDuplicate() {
    return this.isPristine && !this.isCreateMode;
  }

  get showDelete() {
    return Boolean(this.props.canDelete) && !this.isCreateMode;
  }

  get showMenuActions() {
    return this.isPristine && !this.isCreateMode && Boolean(this.props.menuActions?.length);
  }

  // showToggleActive: Activate/Deactivate for models with a boolean `active`
  // field, on a saved record with no unsaved edits (the toggle writes directly).
  get showToggleActive() {
    return this.isPristine && !this.isCreateMode && Boolean(this.props.onToggleActive)
      && this.props.fields.some((field) => field.name === "active" && field.type === "boolean");
  }

  // isRecordActive treats a missing value as active, matching the server's default.
  get isRecordActive() {
    return this.props.record?.active !== false;
  }

  get showActionsMenu() {
    return this.showDuplicate || this.showToggleActive || this.showDelete || this.showMenuActions;
  }

  get isCreateMode() {
    return this.state.status === "new";
  }

  get fallbackFields() {
    return this.props.fields.filter((field) => shouldRenderField(field, this.isCreateMode));
  }

  // hasChatterNode walks the parsed arch once for a <chatter/> marker node
  // (ArchNode.xml renders it as nothing — see its "chatter" branch) so the
  // template can dock ChatterPanel as a sidebar instead of inline.
  get hasChatterNode() {
    if (!this.parsedArch) return false;
    const visit = (node) => {
      if (node.type === "chatter") return true;
      if (Array.isArray(node.children) && node.children.some(visit)) return true;
      if (Array.isArray(node.pages) && node.pages.some((page) => Array.isArray(page.children) && page.children.some(visit))) return true;
      return false;
    };
    return this.parsedArch.children.some(visit);
  }

  // showChatterPanel gates the docked chatter sidebar (rail or expanded) on
  // there actually being room for it — see layout-store.js's
  // chatterFitsAsSidebar for the left-sidebar/viewport-width rule.
  get showChatterPanel() {
    return this.hasChatterNode && this.state.chatterFits;
  }

  // showChatterSheet is the inverse of showChatterPanel: when there's no
  // room to dock chatter beside the form, it opens in a right-hand Sheet
  // instead of being hidden.
  get showChatterSheet() {
    return this.hasChatterNode && !this.state.chatterFits;
  }

  get chatterToggleIcon() {
    if (!this.state.chatterFits) return this.state.chatterSheetOpen ? "panel-right-close" : "panel-right-open";
    return this.state.chatterCollapsed ? "panel-right-open" : "panel-right-close";
  }

  get chatterToggleLabel() {
    if (!this.state.chatterFits) return this.state.chatterSheetOpen ? t("Close chatter") : t("Open chatter");
    return this.state.chatterCollapsed ? t("Expand chatter") : t("Collapse chatter");
  }

  // toggleChatter drives whichever presentation is currently active: the
  // docked sidebar's expanded/collapsed state (persisted per model) when
  // there's room for it, or the right-hand Sheet's open state otherwise.
  toggleChatter() {
    if (this.state.chatterFits) {
      this.state.chatterCollapsed = !this.state.chatterCollapsed;
      writeChatterCollapsed(this.props.model, this.state.chatterCollapsed);
      return;
    }
    this.state.chatterSheetOpen = !this.state.chatterSheetOpen;
  }

  setChatterSheetOpen(open) {
    this.state.chatterSheetOpen = open;
  }

  rootColClass(node) {
    return rootColClassFor(node);
  }

  get archCtx() {
    const relationDomains = Object.fromEntries(
      Object.entries(this.archFieldDomains).flatMap(([fieldName, source]) => {
        const evaluated = evaluateArchDomain(source, this.state.values);
        return evaluated ? [[fieldName, evaluated.domain]] : [];
      })
    );
    return {
      values: this.state.values,
      fieldMap: this.fieldMap,
      fieldInputIDs: this.fieldInputIDs,
      relationDomains,
      isReadonly: this.isReadonly,
      isCreateMode: this.isCreateMode,
      model: this.props.model,
      onFieldChange: (name, value) => this.onFieldChange(name, value),
      // A translation saved through the field's translate editor is already persisted: refresh the
      // shown value without marking the form modified.
      onFieldTranslated: (name, value) => { this.state.values[name] = value; },
      onArchButton: (node) => this.props.onArchButton?.(node),
    };
  }

  // onFieldChange applies the edit, clears dependent relation values whose
  // arch domain referenced the changed field, then lets the backend onchange
  // handler (if any) push its own updates back in.
  onFieldChange(name, value) {
    this.state.values[name] = value;
    if (this.state.status === "saved") this.state.status = "modified";
    this.state.saveError = "";

    for (const [fieldName, source] of Object.entries(this.archFieldDomains)) {
      if (fieldName === name) continue;
      const dependencies = evaluateArchDomain(source, this.state.values)?.dependencies ?? [];
      if (!dependencies.includes(name)) continue;
      const current = this.state.values[fieldName];
      if (current === undefined || current === null || current === "" || current === false) continue;
      this.state.values[fieldName] = false;
    }

    void this.runRemoteChange(name, value);
  }

  async runRemoteChange(name, value) {
    if (!this.props.onRemoteValueChange) return;
    try {
      const updates = await this.props.onRemoteValueChange(name, value, { ...this.state.values });
      if (!updates || typeof updates !== "object") return;
      for (const [fieldName, updatedValue] of Object.entries(updates)) {
        const field = this.fieldMap[fieldName];
        if (!field) continue;
        this.state.values[fieldName] = normalizeEmptyFieldValue(field, updatedValue);
      }
    } catch (error) {
      console.error("Form onchange failed", error);
    }
  }

  validate() {
    const evalContext = { record: this.state.values };
    const visibleFields = this.parsedArch
      ? collectVisibleArchFields(this.parsedArch, this.fieldMap, evalContext, this.isCreateMode)
      : this.fallbackFields;

    for (const field of visibleFields) {
      if (!field.required) continue;
      // Readonly fields are server-managed (e.g. `notnull,default:0` counters the ORM marks
      // required): the user cannot fill them, so the backend default/hook owns the value.
      if (field.readonly) continue;
      // A checkbox has no "empty" state: untouched means false, which the backend stores.
      if (field.type === "boolean" || field.kind === "boolean") continue;
      const value = this.state.values[field.name];
      if (value === null || value === undefined || value === "") return t("{label} is required.", { label: field.label });
    }
    for (const field of visibleFields) {
      if (!isJSONField(field)) continue;
      const value = this.state.values[field.name];
      if (value === null || value === undefined || value === "" || typeof value !== "string") continue;
      try {
        JSON.parse(value);
      } catch {
        return t("{label} must contain valid JSON.", { label: field.label });
      }
    }
    return "";
  }

  // save ships only the fields the user actually changed: several backend
  // models reject writes touching readonly/runtime-managed fields even when
  // the value is unchanged. Create mode sends every field except server-computed
  // ones, which the kernel rejects on create (rejectComputedFieldValues).
  async save() {
    const error = this.validate();
    if (error) {
      this.state.saveError = error;
      return;
    }
    const isCreateMode = this.isCreateMode;
    const entries = this.props.fields
      .map((field) => [field.name, normalizeSubmitFieldValue(field, this.state.values[field.name])])
      .filter(([name, normalized]) => {
        if (isCreateMode) return !this.fieldMap[name]?.serverComputed;
        const original = normalizeSubmitFieldValue(this.fieldMap[name], this.defaultValues[name]);
        return JSON.stringify(normalized) !== JSON.stringify(original);
      });

    this.state.isSaving = true;
    this.state.saveError = "";
    try {
      await this.props.onSave(Object.fromEntries(entries), isCreateMode);
      this.state.status = "saved";
    } catch (err) {
      this.state.saveError = err instanceof Error ? err.message : t("Failed to save record");
    } finally {
      this.state.isSaving = false;
    }
  }

  edit() {
    this.state.editing = true;
    this.state.saveError = "";
    this.state.status = "modified";
  }

  discard() {
    if (this.isCreateMode) {
      this.props.onDiscardNew?.();
      return;
    }
    this.state.values = { ...this.defaultValues };
    this.state.status = "saved";
    this.state.saveError = "";
  }

  async duplicate() {
    this.state.isSaving = true;
    this.state.saveError = "";
    try {
      await this.props.onDuplicate?.();
    } catch (error) {
      this.state.saveError = error instanceof Error ? error.message : t("Failed to duplicate record");
    } finally {
      this.state.isSaving = false;
    }
  }

  // toggleActive flips the record's `active` flag through ModelView, which
  // writes it and reloads the record.
  async toggleActive() {
    this.state.isSaving = true;
    this.state.saveError = "";
    try {
      await this.props.onToggleActive(!this.isRecordActive);
    } catch (error) {
      this.state.saveError = error instanceof Error ? error.message : t("Failed to update record");
    } finally {
      this.state.isSaving = false;
    }
  }

  create() {
    this.props.onCreate?.();
  }

  refresh() {
    this.props.onRefresh?.();
  }

  // runMenuAction forwards a sys.model.action entry to ModelView, which owns
  // confirm/dispatch. ModelView hides its ControlPanel in form mode, so this
  // control bar is the only place a form-bound action can render.
  runMenuAction(menuAction) {
    this.props.onMenuAction?.(menuAction);
  }

  askDelete() {
    this.state.deleteDialogOpen = true;
  }

  async confirmDelete() {
    this.state.deleteDialogOpen = false;
    // onDelete navigates back to the list, which unmounts this form. BaseUI's
    // Modal removes its element at the end of the hide transition and its
    // _hideModal then reads `_element.style` of a null node ("Cannot read
    // properties of null (reading 'style')"), so let the dialog finish
    // closing before the form can be destroyed.
    await new Promise((resolve) => setTimeout(resolve, 400));
    try {
      await this.props.onDelete?.();
    } catch (error) {
      this.state.saveError = error instanceof Error ? error.message : t("Failed to delete record");
    }
  }
}

FormView.components = {
  ArchNode,
  ChatterPanel,
  FormField,
  Alert,
  Badge,
  Button,
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
  Icon,
  Sheet,
  SheetContent,
  Tooltip,
};