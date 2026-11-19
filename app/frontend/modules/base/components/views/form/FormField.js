import { formatLocalDateTime, localInputToServerTime, serverTimeToDate, serverTimeToInputValue, serverTimeToLocalInputValue } from "@/services/server-time.js";
import { safeHref } from "@/services/html-sanitize.js";
import { Component, loadTemplate, onMounted, onPatched, useState, xml } from "nodexui/owl";
import { Alert, Attachment, Badge, Button, Checkbox, ClipboardButton, Combobox, CodeEditor, Field, Icon, Input, Label, NativeSelect, Progress, Switch, Textarea, Tooltip } from "nodexui/component";

import { fetchKvtChoices, fetchModelNameChoices, fetchViewTypeChoices, searchRelationChoices } from "@/services/api/jsonv2.js";
import { RemoteCombobox } from "../../remote-combobox/RemoteCombobox.js";
import { TranslateButton } from "./TranslateButton.js";
import { FormTextEditor } from "./FormTextEditor.js";
import { authStore } from "@/stores/auth-service.js";
import { normalizeLang } from "@/services/i18n/lang.js";
import { parseViewTypes, toggleViewType } from "./view-type-value.js";
import { formatMonetaryValue, getSelectionOptions, isJSONField, isSelectionField, resolveRelationValue } from "./form-field-normalizer.js";
import { KANBAN_COLOR_PALETTE } from "@/modules/base/components/views/base-view/color-palette.js";

const template = await loadTemplate(new URL("./FormField.xml", import.meta.url));

// Matches frontend-admin's many2one-field.tsx MANY2ONE_SEARCH_LIMIT — keep the
// list scannable/actionable, same constant on both sides.
const MANY2ONE_SEARCH_LIMIT = 10;

// RelationRecordDialog class, cached after the first many2one "view" click.
let relationDialogClass = null;

// Arch widget name (normalized) → the kind FormField.xml renders. Widgets with no BaseUI/Bootstrap
// equivalent here (signature, pdf_viewer, iframe_wrapper, ace, domain, properties, ...) degrade to
// "text"/"json" rather than blocking — see the frontend-context.md Phase 6 log entry.
const WIDGET_KIND_MAP = {
  password: "password",
  monetary: "monetary",
  url: "url",
  email: "email",
  phone: "phone",
  copy_clipboard: "copy_clipboard",
  percentage: "percentage",
  percent_pie: "percentage",
  progress_bar: "progress_bar",
  gauge: "gauge",
  color: "color",
  color_picker: "color",
  kanban_color_picker: "kanban_color_picker",
  boolean_toggle: "boolean_toggle",
  boolean_favorite: "boolean_favorite",
  boolean_icon: "boolean_icon",
  float_toggle: "boolean_toggle",
  float_time: "float_time",
  float_factor: "float",
  priority: "priority",
  badge: "badge",
  badge_selection: "badge",
  badge_selection_with_filter: "badge",
  label_selection: "badge",
  state_selection: "state_selection",
  statusbar: "statusbar",
  json: "json",
  json_checkboxes: "json",
  // lua / lua_script: text field holding a Lua script, edited with Base UI's CodeEditor
  // (codeLang="lua": line numbers, colouring, indent keys). Arch options:
  // lua_globals="payload,base" (sandbox globals for the hint), lua_hint="…".
  lua: "lua",
  lua_script: "lua",
  domain: "json",
  properties: "json",
  html: "text",
  // text_editor: text field holding HTML, edited with BaseUI's TextEditor (FormTextEditor.js).
  text_editor: "text_editor",
  rich_text: "text_editor",
  markdown: "text",
  ace: "text",
  ir_ui_view_ace: "text",
  signature: "text",
  pdf_viewer: "text",
  google_slide_viewer: "text",
  journal_dashboard_graph: "text",
  contact_statistics: "text",
  workflow_authorization: "text",
  iframe_wrapper: "text",
  // sys_model_name: char field holding a model's technical name, picked from
  // the sys.model catalog (falls back to a text input if the read fails).
  sys_model_name: "model_name",
  workflow_model_name: "char",
  // ui_view_type / ui_view_types: char field holding one view type name, or a
  // comma-separated list, picked from the ui.view.type catalog (both fall back
  // to a text input if the read fails). Single offers every type, multi only
  // view modes; arch view_modes_only="1"/"0" overrides either default.
  ui_view_type: "view_type",
  ui_view_types: "view_types",
  timezone_mismatch: "timezone_mismatch",
  // timezone: char field holding an IANA zone name, picked from the base.kvt record named by arch
  // `kvt_key` (default "timezones"); falls back to a text input if the read fails.
  timezone: "timezone",
  remaining_days: "remaining_days",
  handle: "handle",
  reference: "reference",
  many2one_avatar: "many2one",
  many2one_barcode: "many2one",
  many2one_reference: "many2one",
  many2one_reference_integer: "many2one",
  image: "image",
  image_url: "image",
  binary: "image",
  attachment_image: "image",
  contact_image: "image",
  many2many_tags: "x2many",
  many2many_tags_avatar: "x2many",
  many2many_checkboxes: "x2many",
  many2many_binary: "x2many",
};

const CODE_LANG_ALIASES = { js: "javascript", ts: "typescript", py: "python", yml: "yaml", md: "markdown", sh: "bash", golang: "go" };

// resolveWidgetKind maps an arch widget hint + fields_get type onto a
// rendered widget kind. Anything unrecognised degrades to a plain text
// input rather than failing.
function resolveWidgetKind(field, widget) {
  const normalized = widget?.replace(/-/g, "_");
  // widget="code" / widget="code-<lang>": any string/text field in a BaseUI CodeEditor.
  if (normalized === "code" || normalized?.startsWith("code_")) return "code";
  if (normalized && WIDGET_KIND_MAP[normalized]) return WIDGET_KIND_MAP[normalized];
  if (field.type === "boolean") return "boolean";
  if (isSelectionField(field)) return "selection";
  if (field.type === "many2one" || field.kind === "one2one") return "many2one";
  if (field.type === "many2many" || field.type === "one2many") return "x2many";
  if (field.type === "date") return "date";
  if (field.type === "datetime") return "datetime";
  if (field.type === "integer") return "integer";
  if (field.type === "monetary") return "monetary";
  if (field.type === "float") return "float";
  // JSON columns (jsonb/map/RawMessage) get the json widget without an explicit
  // widget="json": the text path would render an object as "[object Object]".
  // Same detection as the save path's parseJSONFieldValue, so edit and submit agree.
  if (isJSONField(field)) return "json";
  if (field.type === "text" || field.type === "html") return "text";
  return "char";
}

const BOOLEAN_LIKE_KINDS = new Set(["boolean", "boolean_toggle", "boolean_favorite", "boolean_icon"]);
const BADGE_LIKE_KINDS = new Set(["badge", "state_selection", "statusbar", "priority"]);

// Kinds drawn by a Base UI Input/Textarea/NativeSelect/Combobox, which float the label on the border
// (and show the help as a tooltip icon) through the surrounding <Field>. Every other kind gets the
// stacked <Label> instead.
const SELECT_KINDS = new Set(["selection", "badge", "state_selection", "statusbar"]);
const COMBO_KINDS = new Set(["many2one", "model_name", "view_type", "timezone"]);
const DATE_KINDS = new Set(["date", "datetime", "remaining_days"]);
const NUMBER_KINDS = new Set(["integer", "float", "monetary", "float_time", "percentage", "progress_bar", "gauge"]);
const OUTLINED_KINDS = new Set(["char", "text", "password", "url", "email", "phone", "json", ...SELECT_KINDS, ...COMBO_KINDS, ...DATE_KINDS, ...NUMBER_KINDS]);
// <input type> per kind for the generic text Input; anything else is "text".
const TEXT_INPUT_TYPES = { url: "url", email: "email", phone: "tel", password: "password", color: "color" };
const COMBO_PLACEHOLDERS = { many2one: () => t("Search…"), model_name: () => t("Select a model…"), view_type: () => t("Select a view type…"), timezone: () => t("Select a timezone…") };

export class FormField extends Component {
  static template = xml({ raw: [template] });
  static components = { RemoteCombobox, TranslateButton, FormTextEditor, Alert, Attachment, Badge, Button, Checkbox, ClipboardButton, Combobox, CodeEditor, Field, Icon, Input, Label, NativeSelect, Progress, Switch, Textarea, Tooltip };

  setup() {
    this.state = useState({
      options: [], loading: false,
      // x2many "search existing to add" combobox state — separate from the
      // many2one options above so a form with both widget kinds doesn't share
      // one search result list. x2manyComboKey is bumped after every pick to
      // force the pure-Owl Combobox to remount (it has no exposed "clear the
      // typed query" API, see onX2ManyChange).
      x2manyOptions: [], x2manyLoading: false, x2manyComboKey: 0,
      // model_name widget: sys.model catalog choices; modelNameFailed flips
      // the widget back to a plain text input.
      modelNames: [], modelNameFailed: false,
      // view_type(s) widgets: ui.view.type catalog choices; viewTypeFailed
      // flips them back to a plain text input.
      viewTypes: [], viewTypeFailed: false,
      // timezone widget: base.kvt option list; timezoneFailed flips it back to a plain text input.
      timezones: [], timezoneFailed: false,
      // Name hydrated for a many2one that arrived as a bare id (no [id, name] tuple).
      resolvedRelation: { id: "", name: "" },
      // many2one "view" popup: { component, model, recordId } while open.
      relationDialog: null,
    });
    // The Combobox only exposes options/value/onChange (no per-keystroke search hook), so options are
    // fetched once and filtered client-side. A saved record mounts read-only and flips to edit on the
    // SAME instance, so onMounted alone never fetches — onPatched catches the first editable render.
    this.relationOptionsRequested = false;
    // relationDomainKey remembers which evaluated arch domain the current
    // many2one options were fetched for, so a domain that depends on another
    // field (e.g. operation_id filtered by method_model) refetches on change.
    this.relationDomainKey = "";
    onMounted(() => {
      this.ensureRelationOptions();
      this.hydrateRelationName();
    });
    onPatched(() => {
      this.ensureRelationOptions();
      this.hydrateRelationName();
    });
  }

  // hydrateRelationName looks the display name up when a read-only many2one only carries a bare id, so the
  // form shows "Acme" instead of "32". One request per id; failures keep the id fallback.
  hydrateRelationName() {
    if (this.kind !== "many2one" || !this.field.relation) return;
    const raw = this.value;
    if (Array.isArray(raw) || (raw && typeof raw === "object")) return;
    const id = resolveRelationValue(raw);
    if (id === "" || id === null || String(this.state.resolvedRelation.id) === String(id)) return;
    if (this.relationNameRequested === String(id)) return;
    this.relationNameRequested = String(id);
    searchRelationChoices(this.field.relation, "", 1, [["id", "=", Number(id)]])
      .then((choices) => {
        const match = choices.find((choice) => String(choice.id) === String(id));
        if (match) this.state.resolvedRelation = { id: String(id), name: match.name };
      })
      .catch(() => {});
  }

  // ensureRelationOptions fires the one-time relation option fetch the first
  // time this field renders editable; the flag keeps later re-renders (and
  // the state writes the fetch itself triggers) from re-requesting.
  ensureRelationOptions() {
    if (this.isReadonly) return;
    if (this.baseKind === "many2one" && this.relationOptionsRequested && this.relationDomainKey !== this.currentRelationDomainKey) {
      void this.loadRelationOptions("");
      return;
    }
    if (this.relationOptionsRequested) return;
    if (this.baseKind === "view_type" || this.baseKind === "view_types") {
      this.relationOptionsRequested = true;
      void this.loadViewTypeOptions();
    } else if (this.baseKind === "timezone") {
      this.relationOptionsRequested = true;
      void this.loadTimezoneOptions();
    } else if (this.baseKind === "model_name") {
      this.relationOptionsRequested = true;
      void this.loadModelNameOptions();
    } else if (this.baseKind === "many2one") {
      this.relationOptionsRequested = true;
      void this.loadRelationOptions("");
    } else if (this.baseKind === "x2many") {
      this.relationOptionsRequested = true;
      void this.loadX2ManyOptions("");
    }
  }

  get field() {
    return this.props.field;
  }

  get ctx() {
    return this.props.ctx;
  }

  // baseKind is the requested widget; kind additionally degrades the catalog widgets to a plain
  // "char" input once their catalog read has failed.
  get baseKind() {
    return resolveWidgetKind(this.props.field, this.props.node?.widget);
  }

  get kind() {
    const kind = this.baseKind;
    if (kind === "model_name" && this.state.modelNameFailed) return "char";
    if (kind === "timezone" && this.state.timezoneFailed) return "char";
    if ((kind === "view_type" || kind === "view_types") && this.state.viewTypeFailed) return "char";
    return kind;
  }

  get inputID() {
    return this.props.node?.id ?? `field-${this.props.field.name}`;
  }

  get value() {
    return this.ctx.values[this.field.name];
  }

  // luaGlobals reads the lua widget's comma-separated `lua_globals` arch
  // attribute; every Lua runner binds `payload`, so that is the default.
  get luaGlobals() {
    const raw = this.props.node?.attrs?.lua_globals;
    if (typeof raw !== "string") return ["payload"];
    return raw.split(",").map((name) => name.trim()).filter(Boolean);
  }

  // codeLang is the language of the code widget: the `<lang>` of widget="code-<lang>", else the
  // arch `code_lang` attribute; undefined lets the CodeEditor pick its default. Short aliases
  // (js, py, ...) map to the CodeEditor's language names.
  get codeLang() {
    const widget = this.props.node?.widget?.toLowerCase() ?? "";
    const raw = widget.startsWith("code-") ? widget.slice(5) : (this.props.node?.attrs?.code_lang ?? "");
    const lang = String(raw).trim().toLowerCase();
    return CODE_LANG_ALIASES[lang] ?? (lang || undefined);
  }

  // codeValue is the editor text: object/array values (json fields) are pretty-printed rather than
  // run through String(), which would show "[object Object]".
  get codeValue() {
    const raw = this.value;
    if (raw !== null && typeof raw === "object") return this.jsonEditValue;
    return this.stringValue;
  }

  get luaHint() {
    return this.props.node?.attrs?.lua_hint ?? "";
  }

  get isReadonly() {
    return this.ctx.isReadonly || this.field.readonly === true;
  }

  get readonlyDateText() {
    const text = this.stringValue;
    return text ? text.replace("T", " ") : "—";
  }

  // stringValue is the display/edit string for every scalar widget; `false`
  // (the backend's empty marker) must render as "", not the text "false".
  get stringValue() {
    const raw = this.value;
    if (raw === null || raw === undefined || raw === false) return "";
    // Datetimes are stored in UTC; edit/display them in the viewer's local zone.
    if (this.field.type === "datetime" && (typeof raw === "number" || typeof raw === "string")) {
      return serverTimeToLocalInputValue(raw);
    }
    if (typeof raw === "string") return raw;
    // Dates arrive as UTC Unix seconds; render them as input-ready strings.
    if (typeof raw === "number" && (this.field.type === "date" || this.field.type === "datetime")) {
      return serverTimeToInputValue(raw, this.field.type);
    }
    // Safety net for a JSON value forced onto a scalar widget (e.g. widget="text").
    if (typeof raw === "object" && !Array.isArray(raw)) {
      try {
        return JSON.stringify(raw, null, 2);
      } catch {
        return String(raw);
      }
    }
    return String(raw);
  }

  get selectionOptions() {
    return getSelectionOptions(this.field);
  }

  // relationLabel prefers the [id, name] tuple's hydrated name over an
  // id-only fallback, same preference order as React's many2one widget.
  get relationLabel() {
    const raw = this.value;
    if (Array.isArray(raw) && raw[1] !== undefined && raw[1] !== null && raw[1] !== "") return String(raw[1]);
    if (raw && typeof raw === "object" && "name" in raw) return String(raw.name ?? "");
    const id = resolveRelationValue(raw);
    if (id === "" || id === null) return "";
    if (String(this.state.resolvedRelation.id) === String(id) && this.state.resolvedRelation.name) return this.state.resolvedRelation.name;
    return String(id);
  }

  // displayValue is the readonly rendering path. It keeps record forms from
  // looking like disabled edit dialogs while preserving relation/selection
  // labels that would otherwise collapse to stored ids.
  get displayValue() {
    if (BOOLEAN_LIKE_KINDS.has(this.kind)) return this.value === true ? t("Yes") : t("No");
    if (this.kind === "many2one") return this.relationLabel || "—";
    if (this.kind === "selection" || BADGE_LIKE_KINDS.has(this.kind)) {
      const option = this.selectionOptions.find((item) => this.isSelected(item));
      return option?.label ?? (this.stringValue || "—");
    }
    if (this.kind === "monetary") return formatMonetaryValue(this.value, this.field, this.ctx.values);
    // Matches React's FloatField readonly path (shared by integer + float):
    // Number(...).toLocaleString() rather than the raw unformatted string.
    if (this.kind === "integer" || this.kind === "float") {
      const n = Number(this.value);
      return Number.isFinite(n) ? n.toLocaleString() : (this.stringValue || "—");
    }
    if (this.kind === "datetime") return formatLocalDateTime(this.value) || "—";
    return this.stringValue || "—";
  }

  // badgeOption resolves the matched selection entry for badge/state_selection/
  // statusbar/priority readonly rendering — statusbar is deliberately
  // simplified to a single badge rather than a full step-bar, since this tree
  // has no generic state-transition-order metadata to build steps from.
  get badgeOption() {
    return this.selectionOptions.find((item) => this.isSelected(item)) ?? null;
  }

  isPriorityActive(option) {
    const current = Number(this.stringValue || 0);
    const optionValue = Number(option.value);
    return Number.isFinite(current) && Number.isFinite(optionValue) && optionValue <= current;
  }

  onPriorityClick(option) {
    if (this.isReadonly) return;
    this.commit(option.value);
  }

  get progressPercent() {
    const n = Number(this.stringValue);
    return Number.isFinite(n) ? Math.max(0, Math.min(100, n)) : 0;
  }

  // float_time renders hours:minutes for readonly display; editing stays a
  // plain decimal-hours number input (same markup as "float").
  get floatTimeDisplay() {
    const n = Number(this.stringValue);
    if (!Number.isFinite(n)) return "—";
    const hours = Math.floor(n);
    const minutes = Math.round((n - hours) * 60);
    return `${hours}:${String(minutes).padStart(2, "0")}`;
  }

  get colorHex() {
    const v = this.stringValue;
    return /^#([0-9a-f]{3}){1,2}$/i.test(v) ? v : "#6c757d";
  }

  get kanbanColorPalette() {
    return KANBAN_COLOR_PALETTE;
  }

  get kanbanColorIndex() {
    const n = Number(this.stringValue);
    return Number.isFinite(n) ? n : 0;
  }

  kanbanSwatchStyle(index) {
    return `background-color:${KANBAN_COLOR_PALETTE[index] ?? "#6c757d"}`;
  }

  onKanbanColorClick(index) {
    if (this.isReadonly) return;
    this.commit(index);
  }

  // linkHref adds the mailto:/tel: scheme for readonly email/phone rendering;
  // url widgets keep the value only when its scheme is safe ("" renders plain text).
  get linkHref() {
    if (this.kind === "email") return `mailto:${this.stringValue}`;
    if (this.kind === "phone") return `tel:${this.stringValue}`;
    return safeHref(this.stringValue);
  }

  // jsonDisplay pretty-prints object/array values for the json widget's
  // readonly view; non-JSON-ish values fall back to their plain string form.
  get jsonDisplay() {
    const raw = this.value;
    if (raw === false || raw === null || raw === undefined) return "—";
    if (typeof raw === "string") return raw;
    try {
      return JSON.stringify(raw, null, 2);
    } catch {
      return this.stringValue;
    }
  }

  get jsonEditValue() {
    const raw = this.value;
    if (raw === false || raw === null || raw === undefined) return "";
    if (typeof raw === "string") return raw;
    try {
      return JSON.stringify(raw, null, 2);
    } catch {
      return "";
    }
  }

  // image widget accepts a file and commits it as a base64 data URL — this is
  // a simplified stand-in for the real attachment/binary RPC flow (no
  // ir.attachment record is created), documented as a known gap.
  onImageInput(files) {
    const file = files?.[0];
    if (!file?.type.startsWith("image/")) return;
    const reader = new FileReader();
    reader.onload = () => this.commit(String(reader.result ?? ""));
    reader.readAsDataURL(file);
  }

  get imageSrc() {
    const raw = this.stringValue;
    if (!raw) return "";
    if (raw.startsWith("data:") || raw.startsWith("http") || raw.startsWith("/")) return raw;
    return `data:image/png;base64,${raw}`;
  }

  // remainingDaysDisplay computes a relative-day label from a date/datetime
  // value for the readonly view; editing still uses the underlying date input.
  get remainingDaysDisplay() {
    const raw = this.value;
    if (!raw) return "—";
    const target = serverTimeToDate(raw);
    if (!target) return "—";
    const diffDays = Math.round((target.setHours(0, 0, 0, 0) - new Date().setHours(0, 0, 0, 0)) / 86400000);
    if (diffDays === 0) return t("Today");
    if (diffDays < 0) return t("{days}d overdue", { days: Math.abs(diffDays) });
    return t("in {days}d", { days: diffDays });
  }

  // Date kinds: datetime fields edit as datetime-local (local zone, converted to UTC seconds on commit),
  // date fields as a plain date. Read-only date/datetime show text; a native date input would show
  // its empty "mm/dd/yyyy" mask.
  get isDatetime() {
    return this.field.type === "datetime";
  }

  get dateInputType() {
    if (this.isReadonly && this.kind !== "remaining_days") return "text";
    return this.isDatetime ? "datetime-local" : "date";
  }

  get dateInputValue() {
    if (this.dateInputType === "text") return this.readonlyDateText;
    return this.isDatetime ? this.stringValue.replace(" ", "T").slice(0, 16) : this.stringValue.slice(0, 10);
  }

  onDateInput(value) {
    this.commit(this.isDatetime ? localInputToServerTime(value) : value);
  }

  // x2manyItems normalizes one2many/many2many values ([id,name] tuples, raw
  // ids, or {id,name} objects) into a display list, used both for the
  // readonly chip summary and as the edit-mode "currently attached" set that
  // addX2ManyItem/removeX2ManyItem mutate. Phase 6 dialogs/x2many/PWA pass:
  // real inline editing is search-existing-to-add + remove-chip only (the
  // plan's realistic MVP subset) — full nested record
  // creation from this widget is still out of scope, see FormField.xml.
  get x2manyItems() {
    const raw = this.value;
    if (!Array.isArray(raw)) return [];
    return raw.map((item) => {
      if (Array.isArray(item)) return { id: item[0], name: item[1] !== undefined && item[1] !== null ? String(item[1]) : String(item[0]) };
      if (item && typeof item === "object") {
        const id = item.id ?? item.resId ?? item.value;
        return { id, name: String(item.name ?? item.display_name ?? id ?? "") };
      }
      return { id: item, name: String(item) };
    });
  }

  get x2manyOverflowCount() {
    return Math.max(0, this.x2manyItems.length - 8);
  }

  // x2manyAddOptions filters the remote search results down to records not
  // already attached, same "don't offer what's already selected" rule the
  // search-and-attach MVP needs.
  get x2manyAddOptions() {
    const selectedIds = new Set(this.x2manyItems.map((item) => String(item.id)));
    return this.state.x2manyOptions.filter((option) => !selectedIds.has(String(option.id)));
  }

  // x2manyComboOptions reshapes x2manyAddOptions into the {value, label}
  // shape the pure-Owl Combobox's `options` prop requires.
  get x2manyComboOptions() {
    return this.x2manyAddOptions.map((item) => ({ value: String(item.id), label: item.name }));
  }

  async loadX2ManyOptions(query) {
    const relation = this.field.relation;
    if (!relation) return;
    this.state.x2manyLoading = true;
    try {
      const domain = this.ctx.relationDomains[this.field.name] ?? [];
      const seq = (this.x2manySearchSeq = (this.x2manySearchSeq ?? 0) + 1);
      const options = await searchRelationChoices(relation, query, MANY2ONE_SEARCH_LIMIT, domain);
      if (seq === this.x2manySearchSeq) this.state.x2manyOptions = options;
    } catch (error) {
      console.warn("[FormField] x2many relation search failed", { field: this.field.name, error });
      this.state.x2manyOptions = [];
    } finally {
      this.state.x2manyLoading = false;
    }
  }

  onX2ManyChange(value) {
    if (value === undefined || value === null) return;
    const option = this.state.x2manyOptions.find((item) => String(item.id) === String(value));
    if (option) this.addX2ManyItem(option);
    // Bump the remount key so the search box reads as "add another" rather
    // than leaving the just-picked record's name behind — the pure-Owl
    // Combobox keeps its typed query in local state with no reset API, so a
    // fresh instance (via t-key) is the only way to clear it.
    this.state.x2manyComboKey += 1;
  }

  // addX2ManyItem/removeX2ManyItem always commit a plain array of [id,name]
  // tuples (the same shape the backend read RPC returns) — the
  // [[6,0,ids]] write-command translation happens once, at save time, in
  // form-field-normalizer.js's normalizeOne2ManyFieldValue/
  // normalizeManyToManyFieldValue, so this widget never has to know about
  // the ORM command-tuple wire format.
  addX2ManyItem(option) {
    const current = this.x2manyItems;
    if (current.some((item) => String(item.id) === String(option.id))) return;
    const next = [...current.map((item) => [item.id, item.name]), [option.id, option.name]];
    this.commit(next);
  }

  removeX2ManyItem(id) {
    if (this.isReadonly) return;
    const next = this.x2manyItems
      .filter((item) => String(item.id) !== String(id))
      .map((item) => [item.id, item.name]);
    this.commit(next);
  }

  get fieldClassName() {
    return [
      "basehub-form-field",
      this.props.className,
      this.props.inline ? "basehub-form-field-inline" : "",
      this.isReadonly ? "basehub-form-field-readonly" : "",
    ].filter(Boolean).join(" ");
  }

  // Every field shows its label by default; only an arch `label=""` attribute (or the legacy
  // `nolabel`) hides it, and a non-empty `label="..."` replaces the model field's label text.
  get showLabel() {
    return Boolean(this.props.showLabel) && this.props.node?.attrs?.label !== "";
  }

  get labelText() {
    return this.props.node?.attrs?.label || this.field.label;
  }

  // Read-only computed/related fields are display-only: they render as a plain span under a stacked
  // label (in view and edit mode) instead of a disabled outlined control.
  get isComputedReadonly() {
    return this.field.computed === true && this.isReadonly;
  }

  get outlined() {
    return this.showLabel && OUTLINED_KINDS.has(this.kind) && !this.isComputedReadonly;
  }

  get stackedLabel() {
    return this.showLabel && !this.outlined;
  }

  // View mode: outlined kinds render the same disabled Base UI control instead
  // of the plain-text readonly spans (those stay for the other kinds).
  get readonlyPlain() {
    return this.isReadonly && !this.outlined;
  }

  // Props handed to Base UI's <Field>, which its Input/Textarea/NativeSelect/Combobox read:
  // `label` floats on the control's border, `description` + useTip becomes its tooltip icon, `error`
  // replaces the helper. Inline fields stay compact, so they carry no help.
  get outlineLabel() {
    if (!this.outlined) return undefined;
    return this.field.required ? `${this.labelText} *` : this.labelText;
  }

  get helpText() {
    return this.field.help && !this.props.inline ? this.field.help : undefined;
  }

  // Inline parse feedback while typing; the raw text is still committed so the
  // save-time validator reports the same error if the user submits anyway.
  get fieldError() {
    if (this.kind !== "json") return undefined;
    const raw = this.value;
    if (typeof raw !== "string" || !raw.trim()) return undefined;
    try {
      JSON.parse(raw);
      return undefined;
    } catch (error) {
      return t("Invalid JSON: {message}", { message: error instanceof Error ? error.message : t("parse error") });
    }
  }

  get isSelectKind() {
    return SELECT_KINDS.has(this.kind);
  }

  get isComboKind() {
    return COMBO_KINDS.has(this.kind);
  }

  get isDateKind() {
    return DATE_KINDS.has(this.kind);
  }

  get isNumberKind() {
    return NUMBER_KINDS.has(this.kind);
  }

  // Generic text Input: <input type> by kind, and the colour picker edits the sanitised hex.
  get textInputType() {
    return TEXT_INPUT_TYPES[this.kind] ?? "text";
  }

  get colorOrStringValue() {
    return this.kind === "color" ? this.colorHex : this.stringValue;
  }

  get numberStep() {
    return this.kind === "integer" ? 1 : "any";
  }

  get numberMin() {
    return this.kind === "progress_bar" || this.kind === "gauge" ? 0 : undefined;
  }

  get numberMax() {
    return this.kind === "progress_bar" || this.kind === "gauge" ? 100 : undefined;
  }

  // text and json share one Textarea; json grows with its content (4..20 rows) so payloads are
  // readable without scrolling.
  get textareaValue() {
    return this.kind === "json" ? this.jsonEditValue : this.stringValue;
  }

  get textareaRows() {
    if (this.kind !== "json") return 4;
    return Math.min(20, Math.max(4, this.jsonEditValue.split("\n").length + 1));
  }

  // Owl template scope exposes no String()/global builtins, so the coercion
  // has to live here rather than inline in the template.
  isSelected(option) {
    return String(option.value) === this.stringValue;
  }

  commit(nextValue) {
    // View mode shows normal-looking controls (not `disabled`), so writes are blocked here.
    if (this.isReadonly) return;
    this.ctx.onFieldChange(this.field.name, nextValue);
  }

  // showTranslate: translate:true text fields of a saved record get the translation editor button.
  get showTranslate() {
    const id = this.ctx.values?.id;
    return this.field.translate === true && !this.ctx.isCreateMode && !this.isReadonly && id !== undefined && id !== null && id !== false;
  }

  get translateLang() {
    return normalizeLang(authStore.state.uiLang || authStore.state.lang);
  }

  onTranslationSaved(value) {
    this.ctx.onFieldTranslated?.(this.field.name, value);
  }

  // Pure-Owl Input/Textarea/NativeSelect's onInput/onChange already extract
  // ev.target.value and pass it directly — these handlers take that value,
  // not the native event, per the pure-Owl conversion.
  onTextInput(value) {
    this.commit(value);
  }

  onNumberInput(value) {
    if (value === "") return this.commit("");
    const parsed = this.kind === "integer" ? Number.parseInt(value, 10) : Number.parseFloat(value);
    this.commit(Number.isFinite(parsed) ? parsed : value);
  }

  get booleanActive() {
    return this.value === true;
  }

  get booleanIconName() {
    if (this.kind === "boolean_favorite") return "star";
    return this.booleanActive ? "check-circle" : "circle";
  }

  onBooleanToggleClick() {
    if (this.isReadonly) return;
    this.commit(!this.booleanActive);
  }

  onSelectionInput(value) {
    this.commit(value === "" ? false : value);
  }

  // Evaluated arch domain (JSON) the current many2one options belong to; a change refetches.
  get currentRelationDomainKey() {
    return JSON.stringify(this.ctx.relationDomains[this.field.name] ?? []);
  }

  // relationSearchLimit lets an arch raise the option count for a many2one
  // whose catalog is larger than the default page (`limit="200"`), since the
  // Combobox only filters the options fetched up front.
  get relationSearchLimit() {
    const limit = Number.parseInt(this.props.node?.attrs?.limit ?? "", 10);
    return Number.isFinite(limit) && limit > 0 ? limit : MANY2ONE_SEARCH_LIMIT;
  }

  // relationOptionsBlocked: arch `options_require="field_a,field_b"` makes a
  // many2one offer no options while any listed field is empty (false/null/""),
  // e.g. sys.model.action's method picker until a model is chosen. The
  // domain key already tracks those values, so filling them in refetches.
  get relationOptionsBlocked() {
    const required = String(this.props.node?.attrs?.options_require ?? "")
      .split(",")
      .map((name) => name.trim())
      .filter(Boolean);
    return required.some((name) => {
      const value = this.ctx.values[name];
      return value === false || value === null || value === undefined || value === "";
    });
  }

  async loadRelationOptions(query) {
    const relation = this.field.relation;
    if (!relation) return;
    if (this.relationOptionsBlocked) {
      this.relationDomainKey = this.currentRelationDomainKey;
      this.state.options = [];
      return;
    }
    this.state.loading = true;
    try {
      const domain = this.ctx.relationDomains[this.field.name] ?? [];
      this.relationDomainKey = JSON.stringify(domain);
      const seq = (this.relationSearchSeq = (this.relationSearchSeq ?? 0) + 1);
      const options = await searchRelationChoices(relation, query, this.relationSearchLimit, domain);
      // A slower, older keystroke must not overwrite the newer result set.
      if (seq === this.relationSearchSeq) this.state.options = options;
    } catch (error) {
      console.warn("[FormField] relation search failed", { field: this.field.name, error });
      this.state.options = [];
    } finally {
      this.state.loading = false;
    }
  }

  // relationComboOptions reshapes state.options into the {value, label} shape RemoteCombobox
  // needs. The current selection is not prepended: its label is shown via the `text` prop, and
  // the list must be exactly what the backend search returned.
  get relationComboOptions() {
    return this.state.options.map((item) => ({ value: String(item.id), label: item.name }));
  }

  get relationComboValue() {
    const id = resolveRelationValue(this.value);
    return id === "" || id === null ? undefined : String(id);
  }

  // The single Combobox serves many2one, model_name and view_type; these getters pick the
  // options, current value, placeholder and change handler for the active kind.
  get comboOptions() {
    if (this.kind === "many2one") return this.relationComboOptions;
    if (this.kind === "timezone") return this.timezoneComboOptions;
    return this.kind === "model_name" ? this.modelNameComboOptions : this.viewTypeComboOptions;
  }

  get comboValue() {
    return this.kind === "many2one" ? this.relationComboValue : this.stringValue || undefined;
  }

  // Id of the linked record, or "" when unset; gates the many2one "view" button.
  get relationRecordId() {
    const id = resolveRelationValue(this.value);
    return id === "" || id === null || id === false ? "" : id;
  }

  // Opens the linked record's form in a popup over this form (unsaved edits here stay untouched).
  // The dialog module is imported on first use: it pulls in FormView, which imports this file.
  async openRelation() {
    const id = this.relationRecordId;
    if (!id || !this.field.relation) return;
    if (!relationDialogClass) relationDialogClass = (await import("./RelationRecordDialog.js")).RelationRecordDialog;
    this.state.relationDialog = { component: relationDialogClass, model: this.field.relation, recordId: id };
  }

  closeRelation() {
    this.state.relationDialog = null;
  }

  get comboPlaceholder() {
    return this.field.placeholder || COMBO_PLACEHOLDERS[this.kind]?.();
  }

  onComboChange(value) {
    if (value === undefined || value === null) return;
    if (this.kind !== "many2one") return this.commit(String(value));
    const option = this.state.options.find((item) => String(item.id) === String(value));
    this.commit(option ? [option.id, option.name] : value);
  }

  get timezoneKvtKey() {
    return this.props.node?.attrs?.kvt_key || "timezones";
  }

  async loadTimezoneOptions() {
    try {
      this.state.timezones = await fetchKvtChoices(this.timezoneKvtKey);
    } catch (error) {
      console.warn("[FormField] timezone kvt read failed", { field: this.field.name, error });
      this.state.timezoneFailed = true;
    }
  }

  // timezoneComboOptions lists the kvt records as value/label pairs; a saved value missing from the
  // list (legacy alias, retired zone) is still offered so it never silently drops.
  get timezoneComboOptions() {
    const options = this.state.timezones.map((zone) => ({ value: zone.value, label: zone.label }));
    const current = this.stringValue;
    if (current && !options.some((option) => option.value === current)) options.unshift({ value: current, label: current });
    return options;
  }

  async loadModelNameOptions() {
    try {
      this.state.modelNames = await fetchModelNameChoices();
    } catch (error) {
      console.warn("[FormField] model catalog read failed", { field: this.field.name, error });
      this.state.modelNameFailed = true;
    }
  }

  // modelNameComboOptions shows "technical.name — Label" so typing either the
  // technical name or the label filters the list; the stored value is the
  // technical name. A saved value missing from the catalog is still offered.
  get modelNameComboOptions() {
    const options = this.state.modelNames.map((model) => ({
      value: model.name,
      label: model.label && model.label !== model.name ? `${model.name} — ${model.label}` : model.name,
    }));
    const current = this.stringValue;
    if (current && !options.some((option) => option.value === current)) options.unshift({ value: current, label: current });
    return options;
  }

  async loadViewTypeOptions() {
    try {
      this.state.viewTypes = await fetchViewTypeChoices();
    } catch (error) {
      console.warn("[FormField] view type catalog read failed", { field: this.field.name, error });
      this.state.viewTypeFailed = true;
    }
  }

  // viewTypeChoices applies the view-modes-only filter: the multi widget
  // defaults to view modes (what menus/window actions can list), the single
  // widget to every type (ui.view.type also holds search/qweb archs).
  get viewTypeChoices() {
    const raw = this.props.node?.attrs?.view_modes_only;
    const modesOnly = raw === undefined ? this.baseKind === "view_types" : !["0", "false", "False"].includes(String(raw));
    return modesOnly ? this.state.viewTypes.filter((choice) => choice.isViewMode) : this.state.viewTypes;
  }

  // viewTypeComboOptions labels each type "Label (name)"; a saved value
  // missing from the catalog is still offered so it never silently drops.
  get viewTypeComboOptions() {
    const options = this.viewTypeChoices.map((choice) => ({ value: choice.name, label: `${choice.label} (${choice.name})` }));
    const current = this.stringValue.trim().toLowerCase();
    if (current && !options.some((option) => option.value === current)) options.unshift({ value: current, label: current });
    return options;
  }

  // selectedViewTypes is the parsed multi value, used for checkbox state.
  get selectedViewTypes() {
    return parseViewTypes(this.stringValue);
  }

  // viewTypeMultiChoices lists catalog choices plus any saved unknown names,
  // so editing never drops a value the catalog does not (yet) contain.
  get viewTypeMultiChoices() {
    const choices = [...this.viewTypeChoices];
    for (const name of this.selectedViewTypes) {
      if (!choices.some((choice) => choice.name === name)) choices.push({ name, label: name, icon: "", isViewMode: false });
    }
    return choices;
  }

  // viewTypeLabels renders the stored list as catalog labels (read-only chips).
  get viewTypeLabels() {
    return this.selectedViewTypes.map((name) => this.state.viewTypes.find((choice) => choice.name === name)?.label ?? name);
  }

  onViewTypeToggle(name, checked) {
    this.commit(toggleViewType(this.stringValue, name, checked));
  }
}
