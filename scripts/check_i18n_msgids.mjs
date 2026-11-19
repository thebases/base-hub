#!/usr/bin/env node
// Checks the frontend gettext catalogs used by Base UI's i18n service (app/frontend/i18n.js).
//
//   node scripts/check_i18n_msgids.mjs            report problems, exit 1 when any
//   node scripts/check_i18n_msgids.mjs --write    also create/extend each <lang>.po: missing msgids are appended, seeded
//                                                 from the backend app/modules/*/i18n/vi_vn.po when the msgid matches,
//                                                 otherwise left with an empty msgstr (which the runtime ignores)
//   node scripts/check_i18n_msgids.mjs --strict   treat empty/fuzzy msgstr as errors too (default: reported as warnings)
//
// Source of truth: every t("...") / t('...') call in app/frontend/**/*.{js,xml} (vendor/ excluded). A file under
// modules/<m>/ belongs to catalog modules/<m>/i18n/<lang>.po; every other file belongs to frontend/i18n/<lang>.po.
// t() and tHtml() are globals (app/frontend/i18n.js), so no import is checked. Errors: msgid missing from the catalog,
// a catalog entry whose placeholders differ from its msgid. Warnings: a catalog entry no call uses (stale), untranslated.

import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const frontend = path.join(root, "app", "frontend");
const backendModules = path.join(root, "app", "modules");
const args = new Set(process.argv.slice(2));
const write = args.has("--write");
const strict = args.has("--strict");
// Catalog file name == languages[].code in app/frontend/i18n.js (canonical ll_cc codes).
const LANGS = ["vi_vn"];

function walk(dir, out = []) {
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    if (entry.name === "vendor" || entry.name === "node_modules" || entry.name.startsWith(".")) continue;
    const full = path.join(dir, entry.name);
    if (entry.isDirectory()) walk(full, out);
    else if (/\.(js|xml)$/.test(entry.name)) out.push(full);
  }
  return out;
}

function catalogDirFor(file) {
  const rel = path.relative(frontend, file).split(path.sep);
  return rel[0] === "modules" && rel.length > 2 ? path.join(frontend, "modules", rel[1]) : frontend;
}

function unescapeXml(text) {
  return text.replace(/&quot;/g, '"').replace(/&apos;/g, "'").replace(/&lt;/g, "<").replace(/&gt;/g, ">").replace(/&amp;/g, "&");
}

function unescapeLiteral(text) {
  return text.replace(/\\(['"\\nt])/g, (_, c) => ({ n: "\n", t: "\t" })[c] ?? c);
}

// extract returns every msgid passed as the first argument string literal of a t(...) call.
const CALL = /(?<![\w.$])t(?:Html)?\(\s*(['"])((?:\\.|(?!\1)[^\\])*)\1/g;
function extract(file) {
  let text = fs.readFileSync(file, "utf8");
  if (file.endsWith(".xml")) text = unescapeXml(text);
  const ids = [];
  for (const match of text.matchAll(CALL)) ids.push(unescapeLiteral(match[2]));
  return ids;
}

function parsePo(text) {
  const entries = new Map();
  for (const block of text.split(/\n\s*\n/)) {
    const entry = { id: "", str: "", fuzzy: false };
    let field = "";
    for (const raw of block.split("\n")) {
      const line = raw.trim();
      if (line.startsWith("#")) {
        if (line.startsWith("#,") && line.includes("fuzzy")) entry.fuzzy = true;
        continue;
      }
      const m = /^(msgid|msgstr)\s+(".*")$/.exec(line);
      if (m) {
        field = m[1] === "msgid" ? "id" : "str";
        entry[field] = JSON.parse(m[2]);
      } else if (field && line.startsWith('"')) {
        entry[field] += JSON.parse(line);
      }
    }
    if (entry.id) entries.set(entry.id, entry);
  }
  return entries;
}

function quote(text) {
  return JSON.stringify(text);
}

function formatEntry(id, str) {
  return `msgid ${quote(id)}\nmsgstr ${quote(str)}\n`;
}

function header(lang) {
  return `msgid ""\nmsgstr ""\n"Language: ${lang}\\n"\n"Content-Type: text/plain; charset=UTF-8\\n"\n`;
}

// Backend translations seed new entries; the first non-empty, non-fuzzy translation wins.
function loadBackendSeeds(lang) {
  const seeds = new Map();
  if (!fs.existsSync(backendModules)) return seeds;
  for (const mod of fs.readdirSync(backendModules)) {
    const file = path.join(backendModules, mod, "i18n", `${lang}.po`);
    if (!fs.existsSync(file)) continue;
    for (const [id, entry] of parsePo(fs.readFileSync(file, "utf8"))) {
      if (entry.str && !entry.fuzzy && !seeds.has(id)) seeds.set(id, entry.str);
    }
  }
  return seeds;
}

const placeholders = (text) => [...text.matchAll(/\{(\w+)\}/g)].map((m) => m[1]).sort().join(",");

// Group msgids by catalog directory.
const usage = new Map(); // catalog dir -> Map(msgid -> [files])
const errors = [];
const warnings = [];
for (const file of walk(frontend)) {
  if (path.basename(file) === "i18n.js") continue;
  const ids = extract(file);
  if (ids.length === 0) continue;
  const rel = path.relative(root, file);
  const dir = catalogDirFor(file);
  if (!usage.has(dir)) usage.set(dir, new Map());
  for (const id of ids) {
    if (!usage.get(dir).has(id)) usage.get(dir).set(id, []);
    usage.get(dir).get(id).push(rel);
  }
}

let total = 0;
for (const lang of LANGS) {
  const seeds = write ? loadBackendSeeds(lang) : new Map();
  const dirs = new Set([...usage.keys()]);
  // Catalogs on disk with no remaining t() usage are still checked for staleness.
  for (const dir of [frontend, ...fs.readdirSync(path.join(frontend, "modules")).map((m) => path.join(frontend, "modules", m))]) {
    if (fs.existsSync(path.join(dir, "i18n", `${lang}.po`))) dirs.add(dir);
  }
  for (const dir of dirs) {
    const poPath = path.join(dir, "i18n", `${lang}.po`);
    const relPo = path.relative(root, poPath);
    const used = usage.get(dir) ?? new Map();
    const existing = fs.existsSync(poPath) ? parsePo(fs.readFileSync(poPath, "utf8")) : new Map();
    const missing = [...used.keys()].filter((id) => !existing.has(id));
    if (write && (missing.length > 0 || !fs.existsSync(poPath))) {
      fs.mkdirSync(path.dirname(poPath), { recursive: true });
      let text = fs.existsSync(poPath) ? fs.readFileSync(poPath, "utf8").replace(/\s*$/, "\n") : header(lang);
      for (const id of missing) {
        text += `\n${formatEntry(id, seeds.get(id) ?? "")}`;
        existing.set(id, { id, str: seeds.get(id) ?? "", fuzzy: false });
      }
      fs.writeFileSync(poPath, text);
      console.log(`wrote ${relPo}: +${missing.length} (${missing.filter((id) => seeds.has(id)).length} seeded from backend)`);
    } else {
      for (const id of missing) errors.push(`${relPo}: missing msgid ${JSON.stringify(id)} (used in ${used.get(id)[0]})`);
    }
    for (const [id, entry] of existing) {
      total += 1;
      if (!used.has(id)) warnings.push(`${relPo}: unused msgid ${JSON.stringify(id)}`);
      if (!entry.str) (strict ? errors : warnings).push(`${relPo}: untranslated ${JSON.stringify(id)}`);
      else if (entry.fuzzy) (strict ? errors : warnings).push(`${relPo}: fuzzy ${JSON.stringify(id)}`);
      else if (placeholders(id) !== placeholders(entry.str)) errors.push(`${relPo}: placeholder mismatch ${JSON.stringify(id)}`);
    }
  }
}

for (const line of warnings) console.warn(`warn  ${line}`);
for (const line of errors) console.error(`error ${line}`);
const untranslated = warnings.filter((w) => w.includes("untranslated")).length;
console.log(`${total} catalog entries, ${errors.length} errors, ${warnings.length} warnings (${untranslated} untranslated)`);
process.exit(errors.length > 0 ? 1 : 0);
