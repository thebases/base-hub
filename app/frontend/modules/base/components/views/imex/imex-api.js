// RPC helpers for the list-view Import/Export dialogs. Templates live in the
// `sys.model.imex` kernel model ({name, model_name, kind, field_names}, where
// kind is a boolean: false = export template, true = import template);
// imports go through its transactional `import_records` method.
import { createModelRpc } from "@/services/api/jsonv2.js";

const IMEX_MODEL = "sys.model.imex";
const TEMPLATE_FIELDS = ["id", "name", "model_name", "kind", "field_names"];
// Export pages through search_read so one huge request cannot hit the 15 s
// RPC timeout; the hard cap keeps the browser-side workbook bounded.
const EXPORT_PAGE_SIZE = 500;
export const EXPORT_MAX_ROWS = 20000;
// sys.model.imex.kind for export templates (true = import).
export const IMEX_KIND_EXPORT = false;

// listTemplates returns the saved templates of one model, newest first;
// `kind` narrows to export (false) or import (true) templates; omit for both.
export async function listTemplates(model, kind) {
  const domain = [["model_name", "=", model]];
  if (typeof kind === "boolean") domain.push(["kind", "=", kind]);
  const response = await createModelRpc(IMEX_MODEL).search(domain, TEMPLATE_FIELDS, "id DESC", 0, 200);
  const records = Array.isArray(response.result) ? response.result : [];
  return records.map((record) => ({ ...record, field_names: Array.isArray(record.field_names) ? record.field_names : [] }));
}

// saveTemplate overwrites `id` when given, otherwise creates a new template;
// resolves the saved template id.
export async function saveTemplate({ id, name, model, kind, fieldNames }) {
  const values = { name, model_name: model, kind, field_names: fieldNames };
  const rpc = createModelRpc(IMEX_MODEL);
  if (id) {
    await rpc.write([id], values);
    return id;
  }
  const response = await rpc.create(values);
  const created = response.result;
  return typeof created === "object" && created !== null ? created.id : created;
}

export async function deleteTemplate(id) {
  await createModelRpc(IMEX_MODEL).unlink([id]);
}

// importRecords sends parsed rows ({field: cell text}) for one model; the
// server coerces types, resolves many2one names and rolls back on any error.
export async function importRecords(model, rows) {
  const response = await createModelRpc(IMEX_MODEL).call("import_records", [], { model, rows });
  return response.result ?? { created: 0, updated: 0, total: 0 };
}

// fetchExportRecords reads every record matching `domain` (capped at
// EXPORT_MAX_ROWS) with only the chosen fields, oldest first.
export async function fetchExportRecords(model, domain, fieldNames) {
  const rpc = createModelRpc(model);
  const fields = fieldNames.includes("id") ? fieldNames : ["id", ...fieldNames];
  const records = [];
  while (records.length < EXPORT_MAX_ROWS) {
    const limit = Math.min(EXPORT_PAGE_SIZE, EXPORT_MAX_ROWS - records.length);
    const response = await rpc.search(domain, fields, "id ASC", records.length, limit);
    const page = Array.isArray(response.result) ? response.result : [];
    records.push(...page);
    if (page.length < limit) break;
  }
  return records;
}
