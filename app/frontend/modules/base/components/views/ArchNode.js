import { Component, loadTemplate, xml } from "nodexui/owl";

import { evaluateArchBoolean, isArchNodeVisible } from "./form/form-arch-eval.js";
import { resolveArchFieldState } from "./form/form-arch-field-state.js";
import { shouldRenderField } from "./form/form-field-normalizer.js";
import { FormField } from "./form/FormField.js";
import { AttachmentWidget } from "@/modules/base/components/attachment/AttachmentWidget.js";
import { ModelAccessTable } from "@/modules/base/components/permissions/ModelAccessTable.js";
import { NamedFormWidget } from "./form/NamedFormWidget.js";
import { isFormWidget } from "./form/form-widget-registry.js";
import { Button, Label, Separator, TabContent, Tabs, TabsContainer, TabsList, TabTrigger } from "nodexui/component";


const template = await loadTemplate(new URL("./ArchNode.xml", import.meta.url));

const HEADING_TAGS = ["h1", "h2", "h3", "h4", "h5", "h6"];

function classTokens(className) {
  return (className ?? "").split(/\s+/).map((token) => token.trim()).filter(Boolean);
}

function hasToken(className, token) {
  return classTokens(className).includes(token);
}

// translateArchClassName passes backend-authored view-arch class names
// through unchanged — nodexui's nodexui.min.css already ships real
// Bootstrap 5 utility rules (d-flex, gap-*, align-items-*, small,
// text-muted, ...), so Bootstrap tokens must render as themselves, never
// renamed to a local basehub-arch-* alias. Only the two legacy
// semantic markers (not real Bootstrap class names) still get a local
// class, since there is no Bootstrap equivalent to keep unconverted.
function translateArchClassName(className) {
  const mapped = [];
  for (const token of classTokens(className)) {
    if (token === "oe_title") mapped.push("basehub-arch-title");
    else if (token === "button_box") mapped.push("basehub-arch-button-box");
    else mapped.push(token);
  }
  return Array.from(new Set(mapped)).join(" ");
}

function isInlineContainerTag(tagName) {
  const tag = (tagName ?? "").toLowerCase();
  return HEADING_TAGS.includes(tag) || tag === "div" || tag === "span" || tag === "main";
}

// ArchNode renders one parsed form-arch node and recurses into its children.
// Recursion must be a real self-referencing component (ArchNode.components
// below), not a second t-name + t-call inside one xml({raw}) call — that
// combination silently fails to resolve in this tree (see Shell's MenuTree).
export class ArchNode extends Component {
  static template = xml({ raw: [template] });

  get node() {
    return this.props.node;
  }

  get ctx() {
    return this.props.ctx;
  }

  get evalContext() {
    // `context.get('readonly')` lets arch nodes swap between view and edit renderings.
    return { record: this.ctx.values, context: { readonly: Boolean(this.ctx.isReadonly) } };
  }

  get visible() {
    return isArchNodeVisible(this.node.invisible, this.evalContext);
  }

  // Column span mirrors React's 8-column form layout (flexbox here, not CSS
  // grid): groups default to half width, everything else to full width,
  // both overridable by colspan.
  get colClass() {
    if (this.props.inGroupRow) return "";
    return this.colClassFor(this.node);
  }

  colClassFor(node) {
    const raw = node.colspan ?? (node.attrs?.colspan ? Number.parseInt(node.attrs.colspan, 10) : undefined);
    if (raw !== undefined && Number.isFinite(raw)) {
      return `basehub-arch-span-${Math.min(Math.max(raw, 1), 8)}`;
    }
    if (node.type === "group" && node.children.some((child) => child.type === "group")) return "basehub-arch-span-8";
    return node.type === "group" ? "basehub-arch-span-4" : "basehub-arch-span-8";
  }

  get fieldState() {
    return resolveArchFieldState({ node: this.node, evalContext: this.evalContext, baseField: this.ctx.fieldMap[this.node.name] });
  }

  fieldStateFor(node) {
    return resolveArchFieldState({ node, evalContext: this.evalContext, baseField: this.ctx.fieldMap[node.name] });
  }

  get renderField() {
    return this.fieldState.visible && shouldRenderField(this.fieldState.field, this.ctx.isCreateMode);
  }

  shouldRenderFieldNode(node) {
    const fieldState = this.fieldStateFor(node);
    return fieldState.visible && shouldRenderField(fieldState.field, this.ctx.isCreateMode);
  }

  fieldFor(node) {
    return this.fieldStateFor(node).field;
  }

  childColClass(child) {
    return `basehub-arch-col ${this.colClassFor(child)}`;
  }

  groupChildClass() {
    return "basehub-arch-col";
  }

  groupRowClass() {
    const node = this.node;
    const hasNestedGroups = node.children.some((child) => child.type === "group");
    const hasOnlyButtons = node.children.length > 0 && node.children.every((child) => child.type === "button");
    if (hasOnlyButtons) return "basehub-arch-group-actions";
    if (node.columns && node.columns <= 1) return "basehub-arch-group-row basehub-arch-group-row-1";
    if (hasNestedGroups || (node.columns && node.columns > 1)) return "basehub-arch-group-row basehub-arch-group-row-2";
    return "basehub-arch-group-row basehub-arch-group-row-1";
  }

  nodeClassName(extra = "") {
    return [extra, translateArchClassName(this.node.className)].filter(Boolean).join(" ");
  }

  childNodeClassName(child, extra = "") {
    return [extra, translateArchClassName(child.className)].filter(Boolean).join(" ");
  }

  containerClassName() {
    const tag = (this.node.tagName ?? "").toLowerCase();
    const semantic = [];
    if (tag === "h1") semantic.push("basehub-arch-heading-xl");
    if (tag === "h5") semantic.push("basehub-arch-heading-sm");
    if (tag === "main") semantic.push("d-flex", "flex-column", "gap-2");
    if (hasToken(this.node.className, "oe_title")) semantic.push("basehub-arch-title");
    return this.nodeClassName(["basehub-arch-container", ...semantic].join(" "));
  }

  shouldInlineChild(child) {
    if (child.type !== "field") return false;
    if (child.widget === "contact_image" || child.widget === "image") return true;
    return isInlineContainerTag(this.node.tagName);
  }

  inlineFieldShowLabel(child) {
    return !child.nolabel;
  }

  // The security tab exposes masked credential values that must be changed via
  // their own buttons, so it is hidden entirely once the record enters edit
  // mode — arch `invisible` expressions have no "edit mode" variable in scope.
  get visiblePages() {
    return this.node.pages.filter((page) => {
      if (!isArchNodeVisible(page.invisible, this.evalContext)) return false;
      if (page.name === "security" && !this.ctx.isReadonly) return false;
      return true;
    });
  }

  // A `?tab=<page name>` in the URL (e.g. from a list row's type="open"
  // action) wins over autofocus; a name that matches no visible page in this
  // notebook — or a second notebook on the same form — falls through.
  get activePageKey() {
    const pages = this.visiblePages;
    const requestedTab = new URLSearchParams(window.location.search).get("tab");
    const requested = requestedTab ? pages.find((page) => page.name === requestedTab) : undefined;
    return (requested ?? pages.find((page) => page.autofocus) ?? pages[0])?.key ?? "";
  }

  get buttonDisabled() {
    return evaluateArchBoolean(this.node.disabled, this.evalContext, false);
  }

  get buttonVariant() {
    const cls = this.node.className ?? "";
    if (cls.includes("btn-link")) return "link";
    if (cls.includes("btn-secondary")) return "secondary";
    if (cls.includes("btn-destructive")) return "destructive";
    if (cls.includes("btn-primary") || cls.includes("primary")) return "default";
    return "outline";
  }

  // Only childless widgets registered in form-widget-registry.js mount a module component; any
  // other named widget (e.g. web_ribbon) keeps the generic widget rendering it had before.
  isNamedWidget(node) {
    return !node.children.length && isFormWidget(node.name);
  }

  labelFor(name) {
    return this.ctx.fieldInputIDs[name] ?? `field-${name}`;
  }

  onButtonClick() {
    this.ctx.onArchButton(this.node);
  }
}

ArchNode.components = { ArchNode, FormField, AttachmentWidget, ModelAccessTable, NamedFormWidget, Button, Label, Separator, TabContent, Tabs, TabsContainer, TabsList, TabTrigger };