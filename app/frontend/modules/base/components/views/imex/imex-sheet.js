// Spreadsheet I/O for the generic list-view Import/Export dialogs: .xlsx via
// ExcelJS (lazy-loaded from jsDelivr on first use, same pinned build and
// inject-once pattern as modules/edc/widgets/device-cdn.js — base must not
// import from a feature module) and dependency-free RFC 4180 .csv.

const EXCELJS_SRC = "https://cdn.jsdelivr.net/npm/exceljs@4.4.0/dist/exceljs.min.js";
const XLSX_MIME = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet";

let excelPromise = null;

// loadExcelJS resolves window.ExcelJS, injecting the script once; a failed
// load is forgotten so a later click can retry.
function loadExcelJS() {
  if (window.ExcelJS) return Promise.resolve(window.ExcelJS);
  if (!excelPromise) {
    excelPromise = new Promise((resolve, reject) => {
      const script = document.createElement("script");
      script.src = EXCELJS_SRC;
      script.async = true;
      script.dataset.basehubLib = "ExcelJS";
      script.onload = () => (window.ExcelJS ? resolve(window.ExcelJS) : reject(new Error(t("ExcelJS did not load"))));
      script.onerror = () => {
        excelPromise = null;
        script.remove();
        reject(new Error(t("Failed to load the spreadsheet library")));
      };
      document.head.appendChild(script);
    });
  }
  return excelPromise;
}

// saveBlob triggers a browser download for an in-memory file.
function saveBlob(blob, fileName) {
  const url = URL.createObjectURL(blob);
  const link = document.createElement("a");
  link.href = url;
  link.download = fileName;
  document.body.appendChild(link);
  link.click();
  link.remove();
  URL.revokeObjectURL(url);
}

// downloadXlsx writes one sheet (header + rows of strings). Every column is
// text-formatted so Excel keeps leading zeroes in codes/serials/phones.
export async function downloadXlsx({ sheetName, headers, rows, fileName }) {
  const ExcelJS = await loadExcelJS();
  const workbook = new ExcelJS.Workbook();
  const sheet = workbook.addWorksheet(sheetName.slice(0, 31) || "Sheet1");
  sheet.addRow(headers);
  for (const row of rows) sheet.addRow(row);
  sheet.getRow(1).font = { bold: true };
  headers.forEach((header, index) => {
    const column = sheet.getColumn(index + 1);
    column.numFmt = "@";
    column.width = Math.min(Math.max(String(header).length + 4, 14), 40);
  });
  const buffer = await workbook.xlsx.writeBuffer();
  saveBlob(new Blob([buffer], { type: XLSX_MIME }), fileName);
}

// csvCell quotes a cell only when it contains a delimiter, quote or newline.
function csvCell(value) {
  const text = value === null || value === undefined ? "" : String(value);
  return /[",\r\n]/.test(text) ? `"${text.replace(/"/g, '""')}"` : text;
}

// downloadCsv writes header + rows; the BOM makes Excel open UTF-8 correctly.
export function downloadCsv({ headers, rows, fileName }) {
  const lines = [headers, ...rows].map((line) => line.map(csvCell).join(","));
  saveBlob(new Blob(["﻿" + lines.join("\r\n") + "\r\n"], { type: "text/csv;charset=utf-8" }), fileName);
}

// cellText trims an ExcelJS cell value (plain, rich-text/hyperlink {text},
// formula {result} or Date) into a display string.
function cellText(value) {
  if (value === null || value === undefined) return "";
  if (value instanceof Date) return value.toISOString();
  if (typeof value === "object") {
    if (typeof value.text === "string") return value.text.trim();
    if (Array.isArray(value.richText)) return value.richText.map((part) => part.text).join("").trim();
    if (value.result !== undefined) return String(value.result).trim();
  }
  return String(value).trim();
}

// parseCsv splits CSV text into rows of trimmed cells, honouring quoted
// fields with embedded commas, quotes ("") and newlines (RFC 4180).
function parseCsv(text) {
  const rows = [];
  let row = [];
  let cell = "";
  let quoted = false;
  for (let index = 0; index < text.length; index += 1) {
    const char = text[index];
    if (quoted) {
      if (char === '"' && text[index + 1] === '"') {
        cell += '"';
        index += 1;
      } else if (char === '"') {
        quoted = false;
      } else {
        cell += char;
      }
    } else if (char === '"') {
      quoted = true;
    } else if (char === ",") {
      row.push(cell.trim());
      cell = "";
    } else if (char === "\n" || char === "\r") {
      if (char === "\r" && text[index + 1] === "\n") index += 1;
      row.push(cell.trim());
      rows.push(row);
      row = [];
      cell = "";
    } else {
      cell += char;
    }
  }
  if (cell || row.length) {
    row.push(cell.trim());
    rows.push(row);
  }
  return rows;
}

// readSheetRows returns the first sheet as [{rowNumber, cells[]}] (1-based,
// header included), dispatching on the file extension; null = no worksheet.
export async function readSheetRows(file) {
  if (/\.csv$/i.test(file.name)) {
    const text = (await file.text()).replace(/^﻿/, "");
    return parseCsv(text).map((cells, index) => ({ rowNumber: index + 1, cells }));
  }
  const ExcelJS = await loadExcelJS();
  const workbook = new ExcelJS.Workbook();
  await workbook.xlsx.load(await file.arrayBuffer());
  const sheet = workbook.worksheets[0];
  if (!sheet) return null;
  const rows = [];
  sheet.eachRow((row, rowNumber) => {
    // Collect every populated column (1-based) so header positions may vary.
    const cells = [];
    row.eachCell({ includeEmpty: true }, (cell, column) => {
      cells[column - 1] = cellText(cell.value);
    });
    rows.push({ rowNumber, cells: Array.from(cells, (cell) => cell ?? "") });
  });
  return rows;
}
