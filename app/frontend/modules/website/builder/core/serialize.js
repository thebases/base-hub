// Editor-only state must never be saved: classes `bhe-*` and attributes `data-bhe-*`/`contenteditable`
// set by the builder are removed from a CLONE of the canvas root, and the result is serialized as XHTML
// (the publish step needs well-formed XML; the server sanitizes again regardless).

function cleanEditorState(rootEl) {
  const clone = rootEl.cloneNode(true);
  const nodes = [clone, ...clone.querySelectorAll("*")];
  for (const el of nodes) {
    for (const cls of [...el.classList]) if (cls.startsWith("bhe-")) el.classList.remove(cls);
    if (el.getAttribute("class") === "") el.removeAttribute("class");
    for (const attr of [...el.attributes]) {
      if (attr.name.startsWith("data-bhe-") || attr.name === "contenteditable") el.removeAttribute(attr.name);
    }
  }
  return clone;
}

export function serializeBody(rootEl) {
  const clone = cleanEditorState(rootEl);
  const serializer = new XMLSerializer();
  return [...clone.childNodes]
    .map((node) => serializer.serializeToString(node).replace(/ xmlns="http:\/\/www\.w3\.org\/1999\/xhtml"/g, ""))
    .join("");
}

// One element as clean XHTML (editor state removed), e.g. for saving a block as reusable. Ids stay: they wire
// parts of a block together, and are renamed on insert when they collide (core/ids.js).
export function serializeElement(el) {
  const clone = cleanEditorState(el);
  return new XMLSerializer().serializeToString(clone).replace(/ xmlns="http:\/\/www\.w3\.org\/1999\/xhtml"/g, "");
}
