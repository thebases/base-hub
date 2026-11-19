import { executeModelMethod } from "@/services/api/jsonv2.js";

// Renders the stylesheet a draft theme would publish (server-side, same generator as publish).
export async function previewThemeCss(theme) {
  const response = await executeModelMethod("tbl.website_site", "preview_theme_css", { theme });
  if (response?.error) throw new Error(response.error.message || "Theme preview failed");
  const result = Array.isArray(response?.result) ? response.result[0] : response?.result ?? response;
  return String(result?.css || "");
}

// Starter header/footer markup for a site that has none.
export async function fetchDefaultChrome() {
  const response = await executeModelMethod("tbl.website_site", "default_chrome");
  if (response?.error) throw new Error(response.error.message || "Could not load the default header");
  return (Array.isArray(response?.result) ? response.result[0] : response?.result ?? response) || {};
}

// Header or footer templates shipped by installed modules (`kind` = "header" | "footer").
export async function fetchChromeTemplates(kind) {
  const response = await executeModelMethod("tbl.website_site", "list_chrome_templates", { kind });
  if (response?.error) throw new Error(response.error.message || "Could not load the templates");
  // The method returns the list directly (or wrapped in `result`, depending on the transport).
  const rows = Array.isArray(response?.result) ? response.result : Array.isArray(response) ? response : [];
  return rows.filter((row) => row && row.key && row.html);
}
