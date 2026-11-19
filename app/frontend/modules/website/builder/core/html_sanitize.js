// Client-side paste sanitizer. The server sanitizer is the security boundary; this one keeps pasted
// markup (Word, web pages) clean so the stored HTML stays small and predictable.

const PASTE_TAGS = new Set(["p", "br", "b", "strong", "i", "em", "u", "a", "ul", "ol", "li", "h1", "h2", "h3", "h4", "h5", "h6", "blockquote", "sub", "sup"]);
const DROP_WITH_CONTENT = new Set(["script", "style", "iframe", "object", "embed", "template", "noscript", "svg", "math", "head", "title", "meta", "link"]);

export function isSafeUrl(raw) {
  const v = String(raw ?? "").replace(/[\u0000-\u001f\u007f]/g, "").trim().toLowerCase();
  if (v === "") return true;
  const i = v.search(/[:/?#]/);
  if (i < 0 || v[i] !== ":") return true;
  return ["http", "https", "mailto", "tel"].includes(v.slice(0, i));
}

// Returns clean HTML for pasted `html`. `parser` is a DOMParser (injectable for tests).
export function sanitizePasteHtml(html, parser = new DOMParser()) {
  const doc = parser.parseFromString(html, "text/html");
  const out = [];
  const walk = (node) => {
    if (node.nodeType === 3) {
      out.push(escapeText(node.nodeValue));
      return;
    }
    if (node.nodeType !== 1) return;
    const tag = node.nodeName.toLowerCase();
    if (DROP_WITH_CONTENT.has(tag)) return;
    if (!PASTE_TAGS.has(tag)) {
      node.childNodes.forEach(walk);
      return;
    }
    if (tag === "br") {
      out.push("<br>");
      return;
    }
    let attrs = "";
    if (tag === "a") {
      const href = node.getAttribute("href");
      if (href && isSafeUrl(href)) attrs = ` href="${escapeAttr(href)}"`;
    }
    out.push(`<${tag}${attrs}>`);
    node.childNodes.forEach(walk);
    out.push(`</${tag}>`);
  };
  doc.body.childNodes.forEach(walk);
  return out.join("");
}

function escapeText(s) {
  return s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
}
function escapeAttr(s) {
  return escapeText(s).replace(/"/g, "&quot;");
}
