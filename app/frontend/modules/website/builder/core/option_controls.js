// Reads and applies one declarative option control on a block. A control edits exactly one thing:
// a class set (`class` or per-value classes), an attribute (`attr`) or a style property (`style`).
// `control.target` is an optional selector, relative to the block, for the element(s) it edits.
// Values come from the schema (validated server-side), but writes are still guarded: only simple
// class tokens, plain style values and data/aria attributes can be produced.

const TOKENS = (s) => String(s || "").split(/\s+/).filter(Boolean);
const SAFE_CSS_VALUE = /^[#%.,\-\w\s()/"':]*$/;

export function targetsOf(block, control) {
  if (!control.target) return [block];
  try {
    return [...block.querySelectorAll(control.target)];
  } catch {
    return [];
  }
}

function rgbToHex(value) {
  const m = /^rgba?\((\d+),\s*(\d+),\s*(\d+)/.exec(value || "");
  if (!m) return /^#[0-9a-f]{6}$/i.test(value || "") ? value : "";
  return "#" + [m[1], m[2], m[3]].map((n) => Number(n).toString(16).padStart(2, "0")).join("");
}

function urlOf(value) {
  const m = /url\(\s*["']?([^"')]*)["']?\s*\)/.exec(value || "");
  return m ? m[1] : "";
}

// The current value of a control as the UI shows it. Select returns the index into control.values
// (as a string) so class-valued and attribute-valued selects share one rendering.
export function readControl(block, control) {
  const el = targetsOf(block, control)[0];
  if (!el) return null;
  switch (control.type) {
    case "toggle":
      return control.class ? TOKENS(control.class).every((c) => el.classList.contains(c)) : el.getAttribute(control.attr) === control.on;
    case "select": {
      const values = control.values;
      if (control.attr || control.style) {
        const raw = control.attr ? el.getAttribute(control.attr) || "" : el.style.getPropertyValue(control.style);
        const i = values.findIndex((v) => (v.value || "") === raw);
        return String(i < 0 ? 0 : i);
      }
      let best = -1;
      let bestLen = 0;
      values.forEach((v, i) => {
        const t = TOKENS(v.class);
        if (t.length && t.length >= bestLen && t.every((c) => el.classList.contains(c))) {
          best = i;
          bestLen = t.length;
        }
      });
      return String(best < 0 ? Math.max(0, values.findIndex((v) => !v.class)) : best);
    }
    case "range":
      return parseFloat(el.style.getPropertyValue(control.style)) || 0;
    case "color":
      return rgbToHex(el.style.getPropertyValue(control.style));
    case "image":
      return urlOf(el.style.getPropertyValue(control.style));
    case "text":
      return control.attr ? el.getAttribute(control.attr) || "" : el.style.getPropertyValue(control.style);
    default:
      return null;
  }
}

function setStyle(el, prop, value) {
  if (value === "" || value == null) el.style.removeProperty(prop);
  else el.style.setProperty(prop, value);
}

function setAttr(el, name, value) {
  if (value === "" || value == null) el.removeAttribute(name);
  else el.setAttribute(name, value);
}

// Applies `value` (in the shape readControl returns) to every target of the control.
export function applyControl(block, control, value) {
  for (const el of targetsOf(block, control)) {
    switch (control.type) {
      case "toggle":
        if (control.class) TOKENS(control.class).forEach((c) => el.classList.toggle(c, !!value));
        else setAttr(el, control.attr, value ? control.on : "");
        break;
      case "select": {
        const chosen = control.values[Number(value)];
        if (!chosen) break;
        if (control.attr) setAttr(el, control.attr, chosen.value || "");
        else if (control.style) setStyle(el, control.style, chosen.value || "");
        else {
          control.values.forEach((v) => TOKENS(v.class).forEach((c) => el.classList.remove(c)));
          TOKENS(chosen.class).forEach((c) => el.classList.add(c));
        }
        break;
      }
      case "range":
        setStyle(el, control.style, value === "" ? "" : `${value}${control.unit || ""}`);
        break;
      case "color":
        setStyle(el, control.style, String(value || ""));
        break;
      case "image": {
        const url = String(value || "");
        if (url && /["')\\]/.test(url)) throw new Error("Invalid image address.");
        setStyle(el, control.style, url ? `url("${url}")` : "");
        break;
      }
      case "text": {
        const text = String(value ?? "");
        if (control.attr) setAttr(el, control.attr, text);
        else {
          if (!SAFE_CSS_VALUE.test(text)) throw new Error("Invalid value.");
          setStyle(el, control.style, text);
        }
        break;
      }
    }
    if (el.getAttribute("style") === "") el.removeAttribute("style");
    if (el.getAttribute("class") === "") el.removeAttribute("class");
  }
}
