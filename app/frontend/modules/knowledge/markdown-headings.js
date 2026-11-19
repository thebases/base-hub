// Verbatim port of frontend-admin's markdown-headings.ts — pure logic, no
// framework dependency.

export function extractMarkdownHeadings(markdown) {
  const counts = new Map();

  return markdown
    .split(/\r?\n/)
    .map((line) => {
      const match = /^(#{1,3})\s+(.+?)\s*$/.exec(line);
      if (!match) return null;

      const title = match[2].replace(/[`*_~]/g, "").trim();
      const baseID = slugify(title);
      const count = counts.get(baseID) ?? 0;
      counts.set(baseID, count + 1);

      return {
        id: count === 0 ? baseID : `${baseID}-${count + 1}`,
        level: match[1].length,
        title,
      };
    })
    .filter((heading) => heading !== null);
}

export function slugify(value) {
  return value
    .toLowerCase()
    .replace(/[^a-z0-9\s-]/g, "")
    .trim()
    .replace(/\s+/g, "-")
    .replace(/-+/g, "-");
}
