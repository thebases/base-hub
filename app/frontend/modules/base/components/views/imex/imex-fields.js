// Pure field/column logic shared by ImportDialog and ExportDialog. Input is
// the normalized fields_get map from services/api/jsonv2.js#fetchModelFields
// ({type, string, required, readonly, relation, selection: [[value, label]]}).
//
// Column contract (keeps an exported file re-importable):
// - header text is the field label; when two chosen fields share a label the
//   technical name is used instead so headers stay unique;
// - the import side matches a header against the technical name first, then
//   the label (case-insensitive);
// - an `ID` column on import means "update this record", blank means create.


const NON_IMPORTABLE_TYPES = new Set(["one2many", "many2many"]);
// Audit columns the server always manages; useless as default/template picks.
const SYSTEM_FIELDS = new Set(["created_at", "updated_at", "create_date", "write_date", "create_uid", "write_uid"]);

// fieldLabel returns the display label for one field name.
export function fieldLabel(fields, name) {
  if (name === "id") return "ID";
  return fields[name]?.string || name;
}

// exportableFieldNames lists every field that can be exported, ID first,
// then by label, so the picker reads like the form.
export function exportableFieldNames(fields) {
  const names = Object.keys(fields).filter((name) => name !== "id");
  names.sort((a, b) => fieldLabel(fields, a).localeCompare(fieldLabel(fields, b)));
  return ["id", ...names];
}

// isImportable is true for the ID key column and every writable scalar or
// many2one field; computed/readonly/x2many columns cannot be written.
export function isImportable(fields, name) {
  if (name === "id") return true;
  const field = fields[name];
  return Boolean(field) && !field.readonly && !NON_IMPORTABLE_TYPES.has(field.type) && !SYSTEM_FIELDS.has(name);
}

// isRequiredOnCreate mirrors the server: a required writable field must be
// present when a row creates a record (rows with an ID only update).
export function isRequiredOnCreate(fields, name) {
  return name !== "id" && isImportable(fields, name) && fields[name]?.required === true;
}

// defaultFieldNames is the template used when no saved template is loaded:
// the model's required, user-writable fields. ORM marks every notnull column
// required, so readonly server-managed ones are skipped (same rule as
// FormView.validate()). Falls back to `name` for models with none.
export function defaultFieldNames(fields) {
  const names = Object.keys(fields).filter((name) => isRequiredOnCreate(fields, name));
  if (names.length) return names;
  return fields.name ? ["name"] : [];
}

// columnHeaders maps field names to unique header strings (see contract).
export function columnHeaders(fields, names) {
  const labels = names.map((name) => fieldLabel(fields, name));
  return names.map((name, index) => (labels.indexOf(labels[index]) !== labels.lastIndexOf(labels[index]) ? name : labels[index]));
}

// formatExportValue renders one search_read value as spreadsheet text:
// many2one -> display name, selection -> label, boolean -> TRUE/FALSE,
// x2many id lists -> comma-joined ids, objects -> JSON.
export function formatExportValue(field, value) {
  if (value === null || value === undefined || value === false) {
    return field?.type === "boolean" ? "FALSE" : "";
  }
  if (field?.type === "boolean") return value ? "TRUE" : "FALSE";
  if (Array.isArray(value)) {
    if ((field?.type === "many2one" || value.length === 2) && typeof value[1] === "string" && !Array.isArray(value[0])) {
      return value[1];
    }
    return value.map((item) => (Array.isArray(item) ? item[0] : item)).join(",");
  }
  if (field?.type === "selection" && Array.isArray(field.selection)) {
    const option = field.selection.find(([optionValue]) => optionValue === String(value));
    if (option) return option[1];
  }
  if (typeof value === "object") return JSON.stringify(value);
  return String(value);
}

// matchHeader resolves one header to a field name: technical name first,
// then a case-insensitive label match; "" when nothing matches.
function matchHeader(fields, header) {
  const text = header.trim();
  if (!text) return "";
  if (text === "id" || text.toLowerCase() === "id") return "id";
  if (fields[text]) return text;
  const lower = text.toLowerCase();
  return Object.keys(fields).find((name) => fieldLabel(fields, name).toLowerCase() === lower) ?? "";
}

// parseImportRows validates header + data rows from readSheetRows and returns
// {rows: [{field: cell}], previewRows, columns, errors}. Only light checks run
// here (headers, required-on-create, duplicate IDs); type coercion and
// relation lookups are authoritative on the server, which reports row numbers.
export function parseImportRows(fields, sheetRows) {
  const headerCells = sheetRows[0]?.cells ?? [];
  const errors = [];
  const columns = [];
  headerCells.forEach((header, position) => {
    if (!header.trim()) return;
    const name = matchHeader(fields, header);
    if (!name) errors.push(t('Unknown column "{header}".', { header }));
    else if (!isImportable(fields, name)) errors.push(t('Column "{header}" is read-only and cannot be imported.', { header }));
    else if (columns.some((column) => column.name === name)) errors.push(t('Column "{header}" appears more than once.', { header }));
    else columns.push({ name, header: header.trim(), position });
  });
  if (!columns.length && !errors.length) errors.push(t("The file has no header row."));
  if (errors.length) return { rows: [], previewRows: [], columns, errors };

  const hasID = columns.some((column) => column.name === "id");
  const missingRequired = defaultFieldNames(fields).filter((name) => isRequiredOnCreate(fields, name) && !columns.some((column) => column.name === name));
  const rows = [];
  const seenIDs = new Set();
  for (const { rowNumber, cells } of sheetRows.slice(1)) {
    const row = Object.fromEntries(columns.map((column) => [column.name, (cells[column.position] ?? "").trim()]));
    if (!Object.values(row).some((value) => value.length)) continue;
    rows.push(row);
    const id = hasID ? row.id : "";
    if (id) {
      if (seenIDs.has(id)) errors.push(t("Row {row}: ID {id} is duplicated in this file.", { row: rowNumber, id }));
      seenIDs.add(id);
      continue;
    }
    // A create row must carry every required field, either as a missing column or a blank cell.
    for (const name of missingRequired) errors.push(t("Row {row}: {field} is required (column missing).", { row: rowNumber, field: fieldLabel(fields, name) }));
    for (const column of columns) {
      if (isRequiredOnCreate(fields, column.name) && !row[column.name]) errors.push(t("Row {row}: {field} is required.", { row: rowNumber, field: fieldLabel(fields, column.name) }));
    }
  }
  if (!rows.length) errors.push(t("The uploaded file does not contain any data rows."));
  return { rows, previewRows: rows, columns, errors };
}
