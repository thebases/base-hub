// Allowlist HTML sanitizer and safe-URL check shared by every SPA surface that renders stored HTML
// (markdown viewer, read-only text editor, blog editor, website builder canvas). It mirrors the
// server's website sanitizer (modules/website/models/helpers_website_sanitize.go): anything not on
// the allowlist is dropped, never escaped into the output; unknown wrappers are unwrapped.
//
// Profiles:
//   "rich"    (default) formatting markup only: no inline style, no form controls except the
//             disabled checkbox GFM task lists emit.
//   "website" builder-authored pages: adds inline style (checked like the server) and the form
//             controls the server keeps, so the canvas never shows less than a save would store.

const DROP_WITH_CONTENT = new Set([
  "script", "style", "iframe", "frame", "frameset", "object", "embed", "applet", "template",
  "noscript", "link", "meta", "base", "svg", "math",
]);

const RICH_TAGS = new Set([
  "a", "abbr", "address", "article", "aside", "b", "blockquote", "br", "caption", "cite", "code",
  "col", "colgroup", "dd", "del", "details", "div", "dl", "dt", "em", "figcaption", "figure",
  "footer", "h1", "h2", "h3", "h4", "h5", "h6", "header", "hr", "i", "img", "ins", "kbd", "li",
  "main", "mark", "nav", "ol", "p", "picture", "pre", "q", "s", "section", "small", "source",
  "span", "strong", "sub", "summary", "sup", "table", "tbody", "td", "tfoot", "th", "thead",
  "time", "tr", "u", "ul", "input",
]);

const WEBSITE_EXTRA_TAGS = new Set(["button", "label", "video", "form", "textarea", "select", "option"]);

const GLOBAL_ATTRS = new Set(["id", "class", "title", "lang", "dir", "role", "tabindex"]);

const TAG_ATTRS = {
  a: new Set(["href", "target", "rel", "download"]),
  img: new Set(["src", "alt", "width", "height", "loading", "srcset", "sizes"]),
  source: new Set(["src", "srcset", "type", "media", "sizes"]),
  video: new Set(["src", "poster", "controls", "loop", "muted", "playsinline", "width", "height"]),
  td: new Set(["colspan", "rowspan", "headers", "align"]),
  th: new Set(["colspan", "rowspan", "scope", "headers", "align"]),
  col: new Set(["span"]),
  colgroup: new Set(["span"]),
  time: new Set(["datetime"]),
  ol: new Set(["start", "reversed", "type"]),
  li: new Set(["value"]),
  button: new Set(["type", "disabled"]),
  form: new Set(["novalidate"]),
  input: new Set(["autocomplete", "type", "name", "placeholder", "required", "maxlength", "value", "min", "max", "checked", "disabled"]),
  textarea: new Set(["autocomplete", "name", "placeholder", "required", "maxlength", "rows"]),
  select: new Set(["name", "required"]),
  option: new Set(["value", "selected"]),
  label: new Set(["for"]),
  details: new Set(["open"]),
};

const URL_ATTRS = new Set(["href", "src", "poster", "xlink:href", "action", "formaction"]);
const SAFE_SCHEMES = new Set(["http", "https", "mailto", "tel"]);
const WEBSITE_INPUT_TYPES = new Set(["text", "email", "tel", "number", "checkbox", "radio", "date", "url"]);
const AUTOCOMPLETE = new Set(["off", "on", "name", "email", "tel"]);
const BAD_STYLE = ["expression(", "javascript:", "@import", "behavior:", "-moz-binding"];

// isSafeUrl reports whether a URL may be used as a link or resource: http, https, mailto, tel, or a
// relative reference. Control characters and whitespace are removed first, as browsers ignore them
// inside a scheme ("java\tscript:").
export function isSafeUrl(raw) {
  const value = String(raw ?? "").replace(/[\u0000- \u007f]/g, "").toLowerCase();
  if (!value) return true;
  const i = value.search(/[:/?#]/);
  if (i < 0 || value[i] !== ":") return true;
  return SAFE_SCHEMES.has(value.slice(0, i));
}

// safeHref returns the URL when isSafeUrl accepts it, else "" (callers render no link).
export function safeHref(raw) {
  const value = String(raw ?? "").trim();
  return value && isSafeUrl(value) ? value : "";
}

function safeStyle(value) {
  const lower = value.toLowerCase();
  if (BAD_STYLE.some((bad) => lower.includes(bad))) return false;
  // url(...) is allowed for background images, with the same schemes as links.
  for (const match of lower.matchAll(/url\(([^)]*)(\)|$)/g)) {
    if (!match[2] || !isSafeUrl(match[1].trim().replace(/^["']|["']$/g, ""))) return false;
  }
  return true;
}

function allowedValue(tag, name, value, website) {
  if (URL_ATTRS.has(name)) return isSafeUrl(value);
  switch (name) {
    case "srcset":
      return value.split(",").every((part) => isSafeUrl(part.trim().split(/\s+/)[0]));
    case "style":
      return safeStyle(value);
    case "target":
      return value === "_blank" || value === "_self";
    case "tabindex":
      return value === "-1" || value === "0";
    case "autocomplete":
      return AUTOCOMPLETE.has(value.toLowerCase());
    case "type":
      if (tag === "input") return website ? WEBSITE_INPUT_TYPES.has(value.toLowerCase()) : value.toLowerCase() === "checkbox";
      if (tag === "button") return ["button", "submit", "reset"].includes(value.toLowerCase());
      return true;
    default:
      return true;
  }
}

function allowedName(tag, name, website) {
  if (name.startsWith("on")) return false;
  if (name.startsWith("data-") || name.startsWith("aria-")) return true;
  if (name === "style") return website;
  return GLOBAL_ATTRS.has(name) || Boolean(TAG_ATTRS[tag]?.has(name));
}

function cleanElement(el, website) {
  for (const child of Array.from(el.childNodes)) {
    if (child.nodeType === Node.COMMENT_NODE || child.nodeType === Node.PROCESSING_INSTRUCTION_NODE) {
      child.remove();
      continue;
    }
    if (child.nodeType !== Node.ELEMENT_NODE) continue;
    const tag = child.localName.toLowerCase();
    if (DROP_WITH_CONTENT.has(tag) || child.namespaceURI !== "http://www.w3.org/1999/xhtml") {
      child.remove();
      continue;
    }
    if (!RICH_TAGS.has(tag) && !(website && WEBSITE_EXTRA_TAGS.has(tag))) {
      // Unknown wrapper: clean its children, then put them where the wrapper was.
      cleanElement(child, website);
      child.replaceWith(...child.childNodes);
      continue;
    }
    for (const attr of Array.from(child.attributes)) {
      const name = attr.name.toLowerCase();
      if (!allowedName(tag, name, website) || !allowedValue(tag, name, attr.value.trim(), website)) {
        child.removeAttribute(attr.name);
      }
    }
    if (tag === "input" && !website) {
      // GFM task-list checkbox only: never an editable control in rendered rich text.
      if (child.getAttribute("type") !== "checkbox") {
        child.remove();
        continue;
      }
      child.setAttribute("disabled", "");
    }
    if (tag === "a" && child.getAttribute("target") === "_blank") child.setAttribute("rel", "noopener noreferrer");
    cleanElement(child, website);
  }
}

// sanitizeHtml returns `html` with only allowlisted tags, attributes and URL schemes kept.
// Parsing uses an inert document (DOMParser), so nothing in the input loads or runs while cleaning.
export function sanitizeHtml(html, { profile = "rich" } = {}) {
  if (html === null || html === undefined || html === false) return "";
  const doc = new DOMParser().parseFromString(`<!doctype html><body>${String(html)}</body>`, "text/html");
  cleanElement(doc.body, profile === "website");
  return doc.body.innerHTML;
}
