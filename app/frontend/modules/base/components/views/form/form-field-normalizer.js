// JS port of frontend-admin's views/form/form-field-normalizer.ts plus the
// three shared helpers it leans on from components/fields/utils.ts +
// selection/selection-utils.ts (isSelectionField, fieldUsesFalseForEmptyValue,
// normalizeEmptyFieldValue, resolveRelationValue) — those live here rather
// than a separate module because this tree has no field-widget folder tree.

export function isSelectionField(field) {
  return field.type === "selection" || field.kind === "selection" || field.backendType === "selection";
}

export function getSelectionOptions(field) {
  return field.options?.length ? field.options : (field.selection ?? []);
}

// formatMonetaryValue is a trimmed port of frontend-admin's fields/utils.ts
// formatMonetaryValue — the Owl readonly path was falling back to a bare
// number string (no currency symbol/decimal formatting) because the port
// only carried the field-widget catalog map, not this formatter. Kept to the
// metadata this tree actually has on a field (digits, currencyField) plus
// whatever the linked currency record's value looks like ([id,name] tuple,
// {code|symbol|...} object, or a bare code/symbol string) — no locale/
// hideSymbol/trailingZeros widget-option surface, since no monetary widget
// options are parsed anywhere in this tree yet.
export function formatMonetaryValue(value, field, record) {
  const numberValue = Number(value);
  if (!Number.isFinite(numberValue)) {
    return value === null || value === undefined || value === "" || value === false ? "—" : String(value);
  }
  const currencyMeta = extractCurrencyMetadata(field?.currencyField ? record?.[field.currencyField] : undefined);
  const fractionDigits = typeof currencyMeta.digits === "number" ? currencyMeta.digits
    : typeof field?.digits?.[1] === "number" && Number.isFinite(field.digits[1]) ? field.digits[1] : 2;
  try {
    if (currencyMeta.code) {
      return new Intl.NumberFormat(undefined, {
        style: "currency",
        currency: currencyMeta.code,
        currencyDisplay: "narrowSymbol",
        minimumFractionDigits: fractionDigits,
        maximumFractionDigits: fractionDigits,
      }).format(numberValue);
    }
    const formatted = new Intl.NumberFormat(undefined, {
      minimumFractionDigits: fractionDigits,
      maximumFractionDigits: fractionDigits,
    }).format(numberValue);
    return currencyMeta.symbol ? `${currencyMeta.symbol} ${formatted}` : formatted;
  } catch {
    return numberValue.toFixed(fractionDigits);
  }
}

function extractCurrencyMetadata(value) {
  if (Array.isArray(value)) {
    const name = typeof value[1] === "string" ? value[1] : "";
    return { code: normalizeCurrencyCode(name), symbol: normalizeCurrencySymbol(name) };
  }
  if (value && typeof value === "object") {
    const name = String(value.code ?? value.name ?? value.display_name ?? value.symbol ?? "");
    return {
      code: normalizeCurrencyCode(name),
      symbol: typeof value.symbol === "string" ? value.symbol : normalizeCurrencySymbol(name),
      digits: typeof value.digits === "number" ? value.digits : undefined,
    };
  }
  if (typeof value === "string") return { code: normalizeCurrencyCode(value), symbol: normalizeCurrencySymbol(value) };
  return {};
}

function normalizeCurrencyCode(value) {
  const match = value.trim().match(/\b[A-Z]{3}\b/);
  return match?.[0];
}

function normalizeCurrencySymbol(value) {
  const trimmed = value.trim();
  if (!trimmed || /\b[A-Z]{3}\b/.test(trimmed)) return undefined;
  return trimmed.length <= 4 ? trimmed : undefined;
}

function isCreateAutoManagedField(name) {
  return name === "id" || name.endsWith("_at") || name.endsWith("At");
}

export function shouldRenderField(field, isCreateMode) {
  if (field.invisible) return false;
  if (isCreateMode && isCreateAutoManagedField(field.name)) return false;
  return true;
}

export function isJSONField(field) {
  return (
    field.kind === "json" ||
    field.backendType === "json" ||
    field.backendType?.includes("json.") === true ||
    field.backendType?.includes("rawmessage") === true ||
    field.backendType?.startsWith("map[") === true
  );
}

// Empty strings normalised to null; invalid JSON returned raw for the validator.
export function parseJSONFieldValue(field, value) {
  if (!isJSONField(field) || typeof value !== "string") return value;
  const trimmed = value.trim();
  if (!trimmed) return null;
  return parseStructuredJSONString(trimmed);
}

// Unwraps editor text into structured JSON, including double-encoded object
// payloads that can surface after generic form round-trips.
function parseStructuredJSONString(value) {
  let currentValue = value;
  for (let depth = 0; depth < 5; depth += 1) {
    try {
      const parsed = JSON.parse(currentValue);
      if (typeof parsed !== "string") return parsed;
      const nestedValue = parsed.trim();
      const structured = (nestedValue.startsWith("{") && nestedValue.endsWith("}")) || (nestedValue.startsWith("[") && nestedValue.endsWith("]"));
      if (!structured) return parsed;
      currentValue = nestedValue;
    } catch {
      return value;
    }
  }
  return value;
}

// Collapses [id, name] server tuples to the scalar id the backend expects.
function normalizeMany2OneFieldValue(field, value) {
  if (field.type !== "many2one" && field.kind !== "one2one") return value;
  if (value === false || value === null || value === undefined) return false;
  if (Array.isArray(value) && value.length >= 1) {
    const id = value[0];
    if (typeof id === "number" && Number.isFinite(id)) return id;
    if (typeof id === "string" && id !== "") return id;
    return false;
  }
  return value;
}

// Emits a single "replace with these ids" [6,0,ids] ORM command — confirmed
// against the Go backend (internal/orm/runtime_relations.go's x2ManyCommand /
// normalizeX2ManyCommandsForField, wired into applyRelationalValues at
// internal/orm/repository_handler.go:1521) which parses op 6 identically for
// both one2many and many2many fields (applyOneToManyCommands's `case 6`
// clears then re-links; applyManyToManyCommands's `case 6` does the same).
// This is the Phase 6 x2many inline editor's save contract: the widget always
// tracks the *full* desired related-id set (search-existing to add, chip-x to
// remove), so a full replace on every touched save is correct and much
// simpler than diffing individual link/unlink commands. Deliberately does
// NOT emit [0,0,fields] create-inline-row commands — full nested record
// creation from the x2many widget is out of this pass's scope (see the
// frontend-context.md Phase 6 dialogs/x2many/PWA log entry).
// IMPORTANT: an empty array must still normalize to `[[6,0,[]]]`, not
// `false` — `false`/`undefined` raw values are left untouched by the
// `!Array.isArray` guard below (matches un-touched defaults so the save-diff
// doesn't fire), but once the field *is* an array (the widget always commits
// arrays, even []), it must produce an explicit clear-all command: the Go
// side's normalizeX2ManyCommands(false) hits its `default` branch and
// produces zero commands, which is a silent no-op, not a clear.
export function normalizeOne2ManyFieldValue(field, value) {
  if (field.type !== "one2many") return value;
  if (!Array.isArray(value)) return value;
  const ids = value.flatMap((item) => {
    const id = resolveRelationSelectionID(item);
    return id === null ? [] : [id];
  });
  return [[6, 0, ids]];
}

// Same full-replace [6,0,ids] contract as normalizeOne2ManyFieldValue, for
// many2many fields (applyManyToManyCommands's `case 6`). See that function's
// comment for the full rationale.
export function normalizeManyToManyFieldValue(field, value) {
  if (field.type !== "many2many") return value;
  if (!Array.isArray(value)) return value;
  const ids = value.flatMap((item) => {
    const id = resolveRelationSelectionID(item);
    return id === null ? [] : [id];
  });
  return [[6, 0, ids]];
}

function resolveRelationSelectionID(value) {
  if (Array.isArray(value)) return normalizeRelationScalar(value[0]);
  if (typeof value === "object" && value !== null) {
    if ("id" in value) return normalizeRelationScalar(value.id);
    if ("value" in value) return normalizeRelationScalar(value.value);
    if ("resId" in value) return normalizeRelationScalar(value.resId);
  }
  return normalizeRelationScalar(value);
}

function normalizeRelationScalar(value) {
  if (typeof value === "number" && Number.isFinite(value)) return value;
  if (typeof value === "string" && value !== "") return value;
  return null;
}

// Resolves the stable relation id from compatible many2one values.
export function resolveRelationValue(value) {
  if (Array.isArray(value)) return normalizeRelationScalar(value[0]) ?? "";
  if (typeof value === "object" && value !== null) {
    if ("resId" in value) return normalizeRelationScalar(value.resId) ?? "";
    if ("id" in value) return normalizeRelationScalar(value.id) ?? "";
    if ("value" in value) return normalizeRelationScalar(value.value) ?? "";
  }
  return normalizeRelationScalar(value) ?? "";
}

// Select-like and relation fields must use `false` (not "") for an empty
// choice so form payloads match the backend RPC convention.
function fieldUsesFalseForEmptyValue(field, widget) {
  const normalizedWidget = widget?.replace(/-/g, "_");
  return (
    isSelectionField(field) ||
    field.type === "many2one" ||
    field.type === "many2many" ||
    field.type === "one2many" ||
    field.kind === "one2one" ||
    normalizedWidget === "float_toggle"
  );
}

export function normalizeEmptyFieldValue(field, value, widget) {
  if (!fieldUsesFalseForEmptyValue(field, widget)) return value;
  if (value === "" || value === null || value === undefined) return false;
  if (Array.isArray(value) && value.length === 0) return false;
  return value;
}

// normalizeSubmitFieldValue is the one pipeline both the save diff and the
// submitted payload run through — the diff must normalize both sides
// identically or unchanged fields ride along on every write.
export function normalizeSubmitFieldValue(field, raw) {
  return normalizeMany2OneFieldValue(
    field,
    normalizeManyToManyFieldValue(field, normalizeOne2ManyFieldValue(field, parseJSONFieldValue(field, raw)))
  );
}
