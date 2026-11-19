
// Faithful JS port of frontend-admin's views/form/form-arch-parser.ts —
// turns a backend <form> arch string into a small typed-ish node tree the Owl
// renderer can walk without evaluating arbitrary XML/QWeb directives.

export function parseFormArch(view) {
  const doc = new DOMParser().parseFromString(sanitizeFormArchXML(view), "text/xml");
  const parserError = doc.querySelector("parsererror");
  if (parserError) throw new Error(parserError.textContent ?? "Invalid form view XML");

  const root = doc.documentElement;
  if (!root || root.tagName.toLowerCase() !== "form") {
    throw new Error("Form view XML must use a <form> root node");
  }

  return {
    string: readAttr(root, "string"),
    name: readAttr(root, "name"),
    className: readClassName(root),
    attributes: readAttributes(root),
    children: parseChildNodes(root, "form"),
  };
}

// Some backend arches carry raw comparison operators such as `<=` inside
// quoted attribute expressions; browsers reject that as XML, so escape only
// the `<` characters that appear inside a quoted tag attribute value.
function sanitizeFormArchXML(view) {
  let out = "";
  let inTag = false;
  let quote = "";

  for (let index = 0; index < view.length; index += 1) {
    const char = view[index];
    if (!quote && char === "<") {
      const next = view[index + 1] ?? "";
      if (/[A-Za-z!/]/.test(next) || (next === "?" && view[index + 2] === "x")) {
        inTag = true;
        out += char;
        continue;
      }
    }
    if (inTag && !quote && (char === '"' || char === "'")) {
      quote = char;
      out += char;
      continue;
    }
    if (inTag && quote && char === quote) {
      quote = "";
      out += char;
      continue;
    }
    if (inTag && !quote && char === ">") {
      inTag = false;
      out += char;
      continue;
    }
    if (inTag && quote && char === "<") {
      out += "&lt;";
      continue;
    }
    out += char;
  }
  return out;
}

function parseChildNodes(parent, path) {
  return Array.from(parent.childNodes)
    .map((child, index) => parseNode(child, `${path}.${index}`))
    .filter((node) => node !== null);
}

function parseNode(node, key) {
  if (node.nodeType === Node.TEXT_NODE) {
    const text = node.textContent?.replace(/\s+/g, " ").trim();
    return text ? { type: "text", key, text, attrs: {} } : null;
  }
  if (node.nodeType !== Node.ELEMENT_NODE) return null;
  return parseElement(node, key);
}

function parseElement(element, key) {
  const tag = element.tagName.toLowerCase();
  const base = readBaseNode(element, key);

  if (tag === "header") return { ...base, type: "header", children: parseChildNodes(element, key) };
  if (tag === "footer") return { ...base, type: "footer", children: parseChildNodes(element, key) };
  if (tag === "sheet") return { ...base, type: "sheet", children: parseChildNodes(element, key) };
  if (tag === "group") {
    return {
      ...base,
      type: "group",
      label: readAttr(element, "string"),
      columns: readNumberAttr(element, "col"),
      children: parseChildNodes(element, key),
    };
  }
  if (tag === "notebook") {
    return {
      ...base,
      type: "notebook",
      pages: Array.from(element.children)
        .filter((child) => child.tagName.toLowerCase() === "page")
        .map((page, index) => parsePage(page, `${key}.page.${index}`)),
    };
  }
  if (tag === "page") return parsePage(element, key);
  if (tag === "field") return parseField(element, key);
  if (tag === "button") {
    const nestedFieldLabel = Array.from(element.children)
      .find((child) => child.tagName.toLowerCase() === "field")
      ?.getAttribute("string");
    const nestedTextLabel = element.textContent?.replace(/\s+/g, " ").trim();
    return {
      ...base,
      type: "button",
      buttonType: readAttr(element, "type"),
      label: readAttr(element, "string") ?? nestedFieldLabel ?? nestedTextLabel ?? readAttr(element, "name") ?? t("Action"),
      special: readAttr(element, "special"),
      icon: readAttr(element, "icon"),
      title: readAttr(element, "title"),
      disabled: readAttr(element, "disabled"),
      confirm: readAttr(element, "confirm"),
      // Forces a full window.location.reload() once the button's action completes
      // (see ModelView.js runArchButton) instead of the default soft record refresh.
      reload: readBoolAttr(element, "reload"),
      children: parseChildNodes(element, key),
    };
  }
  if (tag === "label") {
    return {
      ...base,
      type: "label",
      for: readAttr(element, "for"),
      label: readAttr(element, "string") ?? element.textContent?.trim(),
    };
  }
  if (tag === "separator" || tag === "newline") {
    return { ...base, type: "separator", label: readAttr(element, "string") };
  }
  if (tag === "widget") {
    return { ...base, type: "widget", title: readAttr(element, "title"), children: parseChildNodes(element, key) };
  }
  if (tag === "chatter") return { ...base, type: "chatter" };
  if (tag === "attachment") {
    const multiAttr = readAttr(element, "multi");
    return { ...base, type: "attachment", multi: multiAttr === "1" || multiAttr === "true" };
  }
  if (tag === "model-access") return { ...base, type: "modelAccess" };

  const children = parseChildNodes(element, key);
  if (children.length > 0) return { ...base, type: "container", tagName: tag, children };
  return null;
}

function parsePage(element, key) {
  return {
    ...readBaseNode(element, key),
    type: "page",
    label: readAttr(element, "string") ?? t("Page"),
    autofocus: readAttr(element, "autofocus") === "autofocus",
    children: parseChildNodes(element, key),
  };
}

function parseField(element, key) {
  // Backend arch uses <tree> historically; newer versions use <list>. Accept both.
  const listElement = Array.from(element.children).find((child) => {
    const tag = child.tagName.toLowerCase();
    return tag === "list" || tag === "tree";
  });
  return {
    ...readBaseNode(element, key),
    type: "field",
    name: readAttr(element, "name") ?? "",
    label: readAttr(element, "string"),
    widget: readAttr(element, "widget"),
    options: readAttr(element, "options"),
    placeholder: readAttr(element, "placeholder"),
    nolabel: readBoolAttr(element, "nolabel"),
    domain: readAttr(element, "domain"),
    context: readAttr(element, "context"),
    mode: readAttr(element, "mode"),
    list: listElement ? parseList(listElement, `${key}.${listElement.tagName.toLowerCase()}`) : undefined,
  };
}

function parseList(element, key) {
  return {
    ...readBaseNode(element, key),
    type: "list",
    editable: readAttr(element, "editable"),
    fields: Array.from(element.children)
      .filter((child) => child.tagName.toLowerCase() === "field")
      .map((field, index) => parseField(field, `${key}.field.${index}`)),
  };
}

function readAttr(element, name) {
  const value = element.getAttribute(name);
  return value === null || value === "" ? undefined : value;
}

function readNumberAttr(element, name) {
  const value = readAttr(element, name);
  if (!value) return undefined;
  const parsed = Number.parseInt(value, 10);
  return Number.isFinite(parsed) ? parsed : undefined;
}

function readBoolAttr(element, name) {
  const value = readAttr(element, name);
  return value === "1" || value?.toLowerCase() === "true";
}

function readBaseNode(element, key) {
  const legacyModifiers = readLegacyModifiers(element);
  return {
    key,
    id: readAttr(element, "id"),
    name: readAttr(element, "name"),
    string: readAttr(element, "string"),
    className: readClassName(element),
    invisible: readModifierAttr(element, "invisible", legacyModifiers),
    readonly: readModifierAttr(element, "readonly", legacyModifiers),
    required: readModifierAttr(element, "required", legacyModifiers),
    colspan: readNumberAttr(element, "colspan"),
    attrs: readAttributes(element),
  };
}

// Legacy `attrs="{...}"` modifiers are parsed once so runtime re-evaluation can reuse them.
function readLegacyModifiers(element) {
  const attrs = readAttr(element, "attrs");
  if (!attrs) return {};
  const parsed = parsePythonLikeLiteral(attrs);
  return isModifierMap(parsed) ? parsed : {};
}

function readModifierAttr(element, name, legacyModifiers) {
  const direct = readAttr(element, name);
  if (direct !== undefined) return direct;
  const legacy = legacyModifiers[name];
  return isModifierValue(legacy) ? legacy : undefined;
}

function readClassName(element) {
  const tokens = [readAttr(element, "class"), readAttr(element, "className")]
    .flatMap((value) => (value ? value.split(/\s+/) : []))
    .map((token) => token.trim())
    .filter(Boolean);
  if (tokens.length === 0) return undefined;
  return Array.from(new Set(tokens)).join(" ");
}

function readAttributes(element) {
  return Object.fromEntries(Array.from(element.attributes).map((attr) => [attr.name, attr.value]));
}

function isModifierMap(value) {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function isModifierValue(value) {
  return value === null || typeof value === "string" || typeof value === "number" || typeof value === "boolean" || Array.isArray(value);
}

function parsePythonLikeLiteral(source) {
  return new PythonLikeLiteralParser(source).parse();
}

class PythonLikeLiteralParser {
  constructor(source) {
    this.source = source;
    this.index = 0;
  }

  parse() {
    const value = this.parseValue();
    this.skipWhitespace();
    return this.isAtEnd() ? value : undefined;
  }

  parseValue() {
    this.skipWhitespace();
    const char = this.peek();
    if (char === "{") return this.parseObject();
    if (char === "[") return this.parseList("]");
    if (char === "(") return this.parseList(")");
    if (char === "'" || char === '"') return this.parseString();
    if (char === "-" || isDigit(char)) return this.parseNumber();
    if (isIdentifierStart(char)) return this.parseIdentifier();
    return undefined;
  }

  parseObject() {
    const out = {};
    this.index += 1;
    while (!this.isAtEnd()) {
      this.skipWhitespace();
      if (this.peek() === "}") {
        this.index += 1;
        return out;
      }
      const key = this.parseValue();
      if (typeof key !== "string") return undefined;
      this.skipWhitespace();
      if (this.peek() !== ":") return undefined;
      this.index += 1;
      out[key] = this.parseValue();
      this.skipWhitespace();
      const next = this.peek();
      if (next === ",") {
        this.index += 1;
        continue;
      }
      if (next === "}") {
        this.index += 1;
        return out;
      }
      return undefined;
    }
    return undefined;
  }

  parseList(terminator) {
    const values = [];
    this.index += 1;
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
      return undefined;
    }
    return undefined;
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
        if (escaped === undefined) return undefined;
        this.index += 1;
        result += escaped;
        continue;
      }
      if (char === quote) return result;
      result += char;
    }
    return undefined;
  }

  parseNumber() {
    const start = this.index;
    if (this.peek() === "-") this.index += 1;
    while (isDigit(this.peek())) this.index += 1;
    if (this.peek() === ".") {
      this.index += 1;
      while (isDigit(this.peek())) this.index += 1;
    }
    const parsed = Number(this.source.slice(start, this.index));
    return Number.isFinite(parsed) ? parsed : undefined;
  }

  parseIdentifier() {
    const start = this.index;
    this.index += 1;
    while (isIdentifierPart(this.peek())) this.index += 1;
    const identifier = this.source.slice(start, this.index);
    if (identifier === "True") return true;
    if (identifier === "False") return false;
    if (identifier === "None") return null;
    return identifier;
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

function isDigit(char) {
  return !!char && char >= "0" && char <= "9";
}

function isIdentifierStart(char) {
  return !!char && /[A-Za-z_]/.test(char);
}

function isIdentifierPart(char) {
  return !!char && /[A-Za-z0-9_]/.test(char);
}
