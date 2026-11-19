import { executeModelMethod, createModelRpc } from "@/services/api/jsonv2.js";

const SNIPPET_MODEL = "tbl.website_snippet";

function rows(result) {
  return Array.isArray(result?.result) ? result.result : Array.isArray(result) ? result : [];
}

// Fetches the block catalog: blocks of installed modules plus the blocks saved for `siteId`.
export async function fetchSnippetCatalog(siteId) {
  const kwargs = siteId ? { site_id: siteId } : {};
  return rows(await executeModelMethod(SNIPPET_MODEL, "list_catalog", kwargs)).filter((row) => row && row.key && row.html);
}

// Fetches the option schemas that drive the Customize tab.
export async function fetchOptionSchemas() {
  return rows(await executeModelMethod(SNIPPET_MODEL, "list_options")).filter((row) => row && row.selector && Array.isArray(row.groups));
}

// Saves markup as a reusable block of one site (sanitized again server-side on write).
export async function saveCustomBlock(siteId, name, html) {
  const response = await createModelRpc(SNIPPET_MODEL).create({ name, category: "custom", site_id: siteId, definition: { html } });
  if (response.error) throw new Error(response.error.message || "Could not save the block.");
}

// Renames or removes a block the user saved (key "custom/<id>"). Bundled blocks cannot be changed.
function customId(key) {
  const m = /^custom\/(\d+)$/.exec(String(key));
  if (!m) throw new Error("Only blocks you saved can be changed.");
  return Number(m[1]);
}

export async function renameCustomBlock(key, name) {
  const response = await createModelRpc(SNIPPET_MODEL).write([customId(key)], { name });
  if (response.error) throw new Error(response.error.message || "Could not rename the block.");
}

export async function deleteCustomBlock(key) {
  const response = await createModelRpc(SNIPPET_MODEL).delete([customId(key)]);
  if (response.error) throw new Error(response.error.message || "Could not delete the block.");
}
