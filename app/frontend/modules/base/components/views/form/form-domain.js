// Faithful JS port of frontend-admin's views/form/form-domain.ts — resolves a
// field's arch `domain="..."` expression against the live record values, and
// reports which record fields it depends on so the form can clear dependent
// relation values when one of them changes.

export function evaluateArchDomain(source, record) {
  const trimmed = source?.trim();
  if (!trimmed) return null;
  const parser = new PythonLikeDomainParser(trimmed, record);
  const value = parser.parseRootValue();
  if (!Array.isArray(value)) return null;
  return { domain: normalizeDomainOperands(value), dependencies: Array.from(parser.dependencies) };
}

export function collectArchFieldDomains(arch) {
  const domains = {};
  const visit = (node) => {
    if (node.type === "field" && node.domain?.trim()) domains[node.name] = node.domain;
    if ("children" in node) node.children.forEach(visit);
    if (node.type === "notebook") node.pages.forEach(visit);
  };
  arch.children.forEach(visit);
  return domains;
}

function normalizeDomainOperands(value) {
  if (Array.isArray(value)) {
    return value.map((item, index) => {
      if (index === 2 && value.length >= 3 && typeof value[0] === "string" && typeof value[1] === "string") {
        return normalizeRelationOperand(item);
      }
      return normalizeDomainOperands(item);
    });
  }
  if (isExpressionToken(value)) return false;
  return value;
}

function normalizeRelationOperand(value) {
  if (Array.isArray(value)) {
    if (value.length === 2 && value[0] !== undefined) return value[0];
    return value.map((item) => normalizeRelationOperand(item));
  }
  if (value && typeof value === "object") {
    if ("resId" in value) return value.resId ?? false;
    if ("id" in value) return value.id ?? false;
    if ("value" in value) return value.value ?? false;
  }
  if (isExpressionToken(value)) return false;
  return value;
}

class PythonLikeDomainParser {
  constructor(source, record) {
    this.source = source;
    this.record = record;
    this.index = 0;
    this.dependencies = new Set();
  }

  parseRootValue() {
    const value = this.parseValue();
    this.skipWhitespace();
    if (!this.isAtEnd()) throw new Error("Unexpected token at the end of the domain.");
    return value;
  }

  parseValue() {
    this.skipWhitespace();
    const char = this.peek();
    if (char === "[") return this.parseList("]");
    if (char === "(") return this.parseList(")", true);
    if (char === "'" || char === '"') return this.parseString();
    if (char === "-" || isDigit(char)) return this.parseNumber();
    if (isIdentifierStart(char)) return this.parseIdentifier();
    throw new Error(`Unexpected token "${char ?? "end"}" in domain.`);
  }

  parseList(terminator, tuple = false) {
    this.index += 1;
    const values = [];
    while (!this.isAtEnd()) {
      this.skipWhitespace();
      if (this.peek() === terminator) {
        this.index += 1;
        return values;
      }
      values.push(this.parseValue());
      this.skipWhitespace();
      const next = this.peek();
      if (next === ",") {
        this.index += 1;
        continue;
      }
      if (next === terminator) {
        this.index += 1;
        return values;
      }
      throw new Error(`Expected "," or "${terminator}"${tuple ? " in tuple" : ""}.`);
    }
    throw new Error(`Missing closing "${terminator}" in domain.`);
  }

  parseString() {
    const quote = this.peek();
    this.index += 1;
    let result = "";
    while (!this.isAtEnd()) {
      const char = this.peek();
      this.index += 1;
      if (char === "\\") {
        const escaped = this.peek();
        if (escaped === undefined) throw new Error("Unterminated escape sequence in string literal.");
        this.index += 1;
        result += decodeEscapedChar(escaped);
        continue;
      }
      if (char === quote) return result;
      result += char;
    }
    throw new Error("Unterminated string literal in domain.");
  }

  parseNumber() {
    const start = this.index;
    if (this.peek() === "-") this.index += 1;
    while (isDigit(this.peek())) this.index += 1;
    if (this.peek() === ".") {
      this.index += 1;
      while (isDigit(this.peek())) this.index += 1;
    }
    const raw = this.source.slice(start, this.index);
    const parsed = Number(raw);
    if (!Number.isFinite(parsed)) throw new Error(`Invalid number literal "${raw}".`);
    return parsed;
  }

  parseIdentifier() {
    const start = this.index;
    this.index += 1;
    while (isIdentifierPart(this.peek()) || this.peek() === ".") this.index += 1;
    const identifier = this.source.slice(start, this.index);
    if (identifier === "True") return true;
    if (identifier === "False") return false;
    if (identifier === "None") return null;
    const root = identifier.split(".")[0]?.trim();
    if (root) this.dependencies.add(root);
    const resolved = resolveRecordPath(this.record, identifier);
    if (resolved.found) return resolved.value;
    return { __expression: identifier };
  }

  skipWhitespace() {
    while (/\s/.test(this.peek() ?? "")) this.index += 1;
  }

  peek() {
    return this.source[this.index];
  }

  isAtEnd() {
    return this.index >= this.source.length;
  }
}

function resolveRecordPath(record, path) {
  const parts = path.split(".").filter(Boolean);
  let current = record;
  for (const part of parts) {
    if (!current || typeof current !== "object" || Array.isArray(current)) return { found: false, value: undefined };
    if (!(part in current)) return { found: false, value: undefined };
    current = current[part];
  }
  return { found: true, value: current };
}

function decodeEscapedChar(char) {
  switch (char) {
    case "n":
      return "\n";
    case "r":
      return "\r";
    case "t":
      return "\t";
    default:
      return char;
  }
}

function isExpressionToken(value) {
  return typeof value === "object" && value !== null && !Array.isArray(value) && typeof value.__expression === "string";
}

function isDigit(value) {
  return !!value && value >= "0" && value <= "9";
}

function isIdentifierStart(value) {
  return !!value && /[A-Za-z_]/.test(value);
}

function isIdentifierPart(value) {
  return !!value && /[A-Za-z0-9_]/.test(value);
}
