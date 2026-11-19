// Faithful JS port of frontend-admin's views/form/form-arch-eval.ts — evaluates
// arch modifier expressions (invisible/readonly/required) and legacy `attrs`
// domain arrays against the live form record.

export function evaluateArchBoolean(expression, evalContext, fallback = false) {
  if (expression === undefined) return fallback;
  if (typeof expression === "boolean") return expression;
  if (typeof expression === "number") return expression !== 0;
  if (expression === null) return false;
  if (Array.isArray(expression)) return evaluateDomainBoolean(expression, evalContext.record, fallback);

  const trimmed = expression.trim();
  if (!trimmed) return fallback;
  if (isFalseLiteral(trimmed)) return false;
  if (isTrueLiteral(trimmed)) return true;

  try {
    return Boolean(evaluateExpression(trimmed, evalContext));
  } catch (error) {
    console.warn("[FormArch] Could not evaluate expression", { expression, error });
    return fallback;
  }
}

export function isArchNodeVisible(invisible, evalContext) {
  return !evaluateArchBoolean(invisible, evalContext, false);
}

function evaluateDomainBoolean(expression, record, fallback) {
  try {
    return evaluateDomainNode(expression, record);
  } catch (error) {
    console.warn("[FormArch] Could not evaluate domain modifier", { expression, error });
    return fallback;
  }
}

function evaluateDomainNode(domain, record) {
  if (!Array.isArray(domain)) return Boolean(domain);
  if (domain.length === 0) return true;
  const first = domain[0];
  if (first === "&" || first === "|" || first === "!") return evaluatePrefixedDomain(domain, record);
  return domain.every((clause) => evaluateDomainClause(clause, record));
}

function evaluatePrefixedDomain(domain, record) {
  const [operator, ...operands] = domain;
  if (operator === "!") return !evaluateDomainNode(operands[0], record);
  if (operator === "&") return evaluateDomainNode(operands[0], record) && evaluateDomainNode(operands[1], record);
  if (operator === "|") return evaluateDomainNode(operands[0], record) || evaluateDomainNode(operands[1], record);
  return false;
}

function evaluateDomainClause(clause, record) {
  if (!Array.isArray(clause)) return Boolean(clause);
  if (clause.length >= 3 && typeof clause[0] === "string" && typeof clause[1] === "string") {
    const [fieldName, operator, expected] = clause;
    return compareDomainValues(resolveRecordPath(record, fieldName), operator, expected);
  }
  return evaluateDomainNode(clause, record);
}

function compareDomainValues(actual, operator, expected) {
  switch (operator) {
    case "=":
    case "==":
      return normalizeComparableValue(actual) === normalizeComparableValue(expected);
    case "!=":
    case "<>":
      return normalizeComparableValue(actual) !== normalizeComparableValue(expected);
    case ">":
      return toNumberLike(actual) > toNumberLike(expected);
    case ">=":
      return toNumberLike(actual) >= toNumberLike(expected);
    case "<":
      return toNumberLike(actual) < toNumberLike(expected);
    case "<=":
      return toNumberLike(actual) <= toNumberLike(expected);
    case "in":
      return containsValue(expected, normalizeComparableValue(actual));
    case "not in":
      return !containsValue(expected, normalizeComparableValue(actual));
    default:
      return false;
  }
}

function resolveRecordPath(record, path) {
  return path.split(".").reduce((current, segment) => {
    if (!current || typeof current !== "object") return undefined;
    return current[segment];
  }, record);
}

function normalizeComparableValue(value) {
  if (Array.isArray(value) && value.length === 2) return value[0];
  if (value && typeof value === "object") {
    if ("resId" in value) return value.resId;
    if ("id" in value) return value.id;
    if ("value" in value) return value.value;
  }
  return value;
}

function toNumberLike(value) {
  const normalized = normalizeComparableValue(value);
  return typeof normalized === "number" ? normalized : Number(normalized ?? 0);
}

function evaluateExpression(expression, { record, context = {} }) {
  const jsExpression = toJavaScriptExpression(expression);
  const normalizedRecord = normalizeEvalRecord(record);
  const scope = {
    ...normalizedRecord,
    ...context,
    context,
    record: normalizedRecord,
    rawRecord: record,
    containsValue,
    getContext: (key, fallback) => context[key] ?? fallback,
    parent: normalizedRecord.parent,
    id: normalizedRecord.id,
    uid: context.uid,
    user_id: context.user_id,
  };
  return Function("scope", `with (scope) { return (${jsExpression}); }`)(scope);
}

function toJavaScriptExpression(expression) {
  let next = expression.trim();
  next = next.replace(/\bTrue\b/g, "true");
  next = next.replace(/\bFalse\b/g, "false");
  next = next.replace(/\bNone\b/g, "null");
  next = next.replace(/\bnot\s+in\b/g, "__NOT_IN__");
  next = next.replace(/\band\b/g, "&&");
  next = next.replace(/\bor\b/g, "||");
  next = next.replace(/\bnot\b/g, "!");
  next = next.replace(/__NOT_IN__/g, "not in");
  next = next.replace(/(['"][^'"]+['"]|\b[A-Za-z_][\w.]*\b)\s+not\s+in\s+(\b[A-Za-z_][\w.]*\b|\[[^\]]*\])/g, "!containsValue($2, $1)");
  next = next.replace(/(['"][^'"]+['"]|\b[A-Za-z_][\w.]*\b)\s+in\s+(\b[A-Za-z_][\w.]*\b|\[[^\]]*\])/g, "containsValue($2, $1)");
  next = next.replace(/\bcontext\.get\(/g, "getContext(");
  next = next.replace(/\b([A-Za-z_]\w*)\b/g, (match, name, offset, source) => {
    if (RESERVED_NAMES.has(name)) return match;
    const previous = source[offset - 1];
    if (previous === "." || previous === "'" || previous === '"') return match;
    return `scope.${name}`;
  });
  next = next.replace(/\bscope\.containsValue\b/g, "containsValue");
  next = next.replace(/\bscope\.getContext\b/g, "getContext");
  return next;
}

function containsValue(container, value) {
  if (Array.isArray(container)) return container.includes(value);
  if (container && typeof container === "object") return typeof value === "string" && value in container;
  if (typeof container === "string") return container.includes(String(value));
  return false;
}

function isTrueLiteral(value) {
  return value === "1" || value.toLowerCase() === "true";
}

function isFalseLiteral(value) {
  return value === "0" || value.toLowerCase() === "false";
}

function normalizeEvalRecord(record) {
  return Object.fromEntries(Object.entries(record ?? {}).map(([key, value]) => [key, normalizeEvalValue(value)]));
}

function normalizeEvalValue(value) {
  if (Array.isArray(value)) {
    if (isRelationTuple(value)) return createRelationEvalValue(value[0], value[1]);
    return value.map((item) => normalizeEvalValue(item));
  }
  if (!value || typeof value !== "object") return value;
  if (isRelationObject(value)) {
    return createRelationEvalValue(value.resId ?? value.id ?? value.value, firstNonEmptyString(value.name, value.displayName, value.display_name), value);
  }
  return Object.fromEntries(Object.entries(value).map(([key, nested]) => [key, normalizeEvalValue(nested)]));
}

function isRelationTuple(value) {
  return value.length === 2 && typeof value[1] === "string";
}

function isRelationObject(value) {
  return typeof value === "object" && value !== null && !Array.isArray(value) && ("id" in value || "resId" in value || "value" in value);
}

// Relation values become dual-natured objects: they compare as their id in a
// numeric context and stringify as their display name, so both
// `partner_id == 3` and `partner_id` truthiness work in arch expressions.
function createRelationEvalValue(rawId, rawLabel, extra) {
  const id = normalizeRelationPrimitive(rawId);
  const label = rawLabel?.trim() || firstNonEmptyString(extra?.name, extra?.displayName, extra?.display_name);
  const relationValue = { ...normalizeExtraRelationFields(extra), id, resId: id, value: id };
  if (label) {
    relationValue.name = label;
    relationValue.displayName = label;
    relationValue.display_name = label;
  }
  Object.defineProperties(relationValue, {
    valueOf: { value: () => (id === "" ? false : id) },
    toString: { value: () => label ?? String(id ?? "") },
    [Symbol.toPrimitive]: {
      value: (hint) => (hint === "string" ? (label ?? String(id ?? "")) : id === "" ? false : id),
    },
  });
  return relationValue;
}

function normalizeExtraRelationFields(extra) {
  if (!extra) return {};
  return Object.fromEntries(
    Object.entries(extra)
      .filter(([key]) => key !== "id" && key !== "resId" && key !== "value")
      .map(([key, value]) => [key, normalizeEvalValue(value)])
  );
}

function normalizeRelationPrimitive(value) {
  if (typeof value === "number" && Number.isFinite(value)) return value;
  if (typeof value === "string") return value;
  return "";
}

function firstNonEmptyString(...values) {
  for (const value of values) {
    if (typeof value === "string" && value.trim()) return value;
  }
  return undefined;
}

const RESERVED_NAMES = new Set(["true", "false", "null", "undefined", "scope", "record", "rawRecord", "context", "containsValue", "getContext"]);
