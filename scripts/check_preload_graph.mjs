// Checks that the signed-in preload lists in app/frontend/index.html match the real static import graph
// (and template XMLs) of Shell.js, ModelView.js and ListView.js. A stale list only loses the speed-up,
// but a missing file silently brings back a serial round trip. Run: node scripts/check_preload_graph.mjs
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../app/frontend");
const ENTRIES = [
  "modules/base/pages/Shell/Shell.js",
  "modules/base/pages/ModelView/ModelView.js",
  "modules/base/components/views/list/ListView.js",
];

// Resolves a static import specifier ("@/x" alias or relative) to a file path, or null for bare/CDN specifiers.
function resolveSpecifier(from, spec) {
  if (spec.startsWith("@/")) return path.join(root, spec.slice(2));
  if (spec.startsWith(".")) return path.join(path.dirname(from), spec);
  return null;
}

// Collects every statically imported module and every `new URL("./X.xml", import.meta.url)` template under a file.
function crawl(file, files, xmls) {
  if (files.has(file) || !fs.existsSync(file)) return;
  files.add(file);
  const source = fs.readFileSync(file, "utf8");
  for (const match of source.matchAll(/(?:^|\n)\s*(?:import|export)\s[^"'`;]*?from\s*["']([^"']+)["']|(?:^|\n)\s*import\s*["']([^"']+)["']/g)) {
    const target = resolveSpecifier(file, match[1] ?? match[2]);
    if (target) crawl(target, files, xmls);
  }
  for (const match of source.matchAll(/new URL\(\s*["']([^"']+\.xml)["']\s*,\s*import\.meta\.url/g)) xmls.add(path.join(path.dirname(file), match[1]));
}

const files = new Set();
const xmls = new Set();
ENTRIES.forEach((entry) => crawl(path.join(root, entry), files, xmls));
const expected = new Set([...files, ...xmls].map((file) => "./" + path.relative(root, file)));

const html = fs.readFileSync(path.join(root, "index.html"), "utf8");
const block = html.slice(html.indexOf("var shell = ["), html.indexOf("var hrefs = shell;"));
const listed = new Set([...block.matchAll(/"(\.\/[^"]+)"/g)].map((match) => match[1]));

const missing = [...expected].filter((href) => !listed.has(href));
const stale = [...listed].filter((href) => !expected.has(href));
missing.forEach((href) => console.error(`missing from index.html preload lists: ${href}`));
stale.forEach((href) => console.error(`in index.html preload lists but not imported any more: ${href}`));
if (missing.length || stale.length) process.exit(1);
console.log(`preload lists OK (${expected.size} files)`);
