// JS port of frontend-admin's views/form/form-arch-field-state.ts — merges an
// arch <field> node's own modifiers over the fields_get FieldDef so
// readonly/required re-evaluate live against the current record values.

import { evaluateArchBoolean, isArchNodeVisible } from "./form-arch-eval.js";

export function resolveArchFieldState({ node, evalContext, baseField }) {
  const field = baseField
    ? {
        ...baseField,
        label: resolveRenderedFieldLabel(node.label, baseField.label, node.name),
        placeholder: node.placeholder ?? baseField.placeholder,
        readonly: node.readonly !== undefined ? evaluateArchBoolean(node.readonly, evalContext, false) : baseField.readonly,
        required: node.required !== undefined ? evaluateArchBoolean(node.required, evalContext, false) : baseField.required,
      }
    : {
        name: node.name,
        label: resolveRenderedFieldLabel(node.label, undefined, node.name),
        type: node.list ? "one2many" : "char",
        placeholder: node.placeholder,
        readonly: node.readonly !== undefined ? evaluateArchBoolean(node.readonly, evalContext, false) : undefined,
        required: node.required !== undefined ? evaluateArchBoolean(node.required, evalContext, false) : undefined,
      };

  return { field, visible: isArchNodeVisible(node.invisible, evalContext) };
}

function resolveRenderedFieldLabel(nodeLabel, fieldLabel, fieldName) {
  const preferred = nodeLabel?.trim() || fieldLabel?.trim();
  // A label identical to the technical name ("partner_id") means the model never set one.
  if (preferred && preferred !== fieldName) return preferred;
  return humanizeFieldName(fieldName);
}

function humanizeFieldName(fieldName) {
  const normalized = fieldName.replace(/[_\-.]+/g, " ").replace(/\s+/g, " ").trim();
  if (!normalized) return fieldName;
  return normalized.replace(/\b\w/g, (char) => char.toUpperCase());
}
