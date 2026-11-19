// The theme controls of the Theme tab: each edits one dotted path of tbl.website_site.theme.
// Defaults mirror the server's generator, so an empty theme shows what would be published.
export const THEME_FIELDS = [
  { group: "Colors", path: "colors.main.0", label: "Primary", type: "color", fallback: "#0d1117" },
  { group: "Colors", path: "colors.main.1", label: "Secondary", type: "color", fallback: "#f0ad4e" },
  { group: "Colors", path: "colors.main.2", label: "Tertiary", type: "color", fallback: "#ffffff" },
  { group: "Colors", path: "colors.light", label: "Light", type: "color", fallback: "#ffffff" },
  { group: "Colors", path: "colors.dark", label: "Dark", type: "color", fallback: "#0d1117" },
  { group: "Colors", path: "website.mode", label: "Mode", type: "select", fallback: "light", options: ["light", "dark"] },
  { group: "Fonts", path: "paragraph.fontFamily", label: "Body font", type: "select", fallback: "Inter, system-ui, sans-serif", options: FONT_STACKS() },
  { group: "Fonts", path: "paragraph.fontSize", label: "Body size", type: "text", fallback: "16px" },
  { group: "Fonts", path: "headings.fontFamily", label: "Heading font", type: "select", fallback: "'Inter Tight', system-ui, sans-serif", options: FONT_STACKS() },
  { group: "Fonts", path: "headings.fontSize", label: "Heading size", type: "text", fallback: "48px" },
  { group: "Buttons", path: "button.roundCorners", label: "Corner radius", type: "text", fallback: "6.4px" },
  { group: "Buttons", path: "button.primaryStyle", label: "Primary style", type: "select", fallback: "fill", options: ["fill", "outline"] },
  { group: "Links", path: "link.style", label: "Link style", type: "select", fallback: "underline-hover", options: ["underline-hover", "underline", "none"] },
];

// System-safe stacks only: nothing is downloaded from a third party, so the published site stays
// self-contained. The default stacks stay selectable because they are what the generator uses.
function FONT_STACKS() {
  return [
    "Inter, system-ui, sans-serif",
    "'Inter Tight', system-ui, sans-serif",
    "system-ui, -apple-system, 'Segoe UI', sans-serif",
    "Georgia, 'Times New Roman', serif",
    "'Courier New', monospace",
  ];
}

function getPath(obj, path) {
  let cur = obj;
  for (const key of path.split(".")) {
    if (cur == null || typeof cur !== "object") return undefined;
    cur = cur[key];
  }
  return cur;
}

// Returns a copy of `obj` with `path` set; arrays are kept as arrays when the next key is an index.
function setPath(obj, path, value) {
  const keys = path.split(".");
  const root = Array.isArray(obj) ? [...obj] : { ...(obj || {}) };
  let cur = root;
  keys.forEach((key, i) => {
    if (i === keys.length - 1) {
      cur[key] = value;
      return;
    }
    const next = cur[key];
    const wantArray = /^\d+$/.test(keys[i + 1]);
    cur[key] = Array.isArray(next) ? [...next] : next && typeof next === "object" ? { ...next } : wantArray ? [] : {};
    cur = cur[key];
  });
  return root;
}

export function themeFormFrom(theme) {
  const form = {};
  for (const f of THEME_FIELDS) form[f.path] = getPath(theme, f.path) ?? f.fallback;
  return form;
}

export function themeFromForm(baseTheme, form) {
  let theme = baseTheme || {};
  for (const f of THEME_FIELDS) theme = setPath(theme, f.path, form[f.path]);
  return theme;
}
