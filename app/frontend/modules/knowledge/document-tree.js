// Port of frontend-admin's nav-documents.tsx tree-building helpers
// (buildDocumentTree/sortDocumentNodes/humanizePathSegment) — pure logic, no
// framework dependency. Groups flat MarkdownMenuItem rows (relative_path) into
// a folder/leaf tree for the sidebar's "KnowLedge" section.

export function buildDocumentTree(items) {
  const root = { kind: "folder", key: "", label: "", children: [] };

  for (const item of items) {
    const segments = item.relative_path.split("/").filter(Boolean);
    if (segments.length === 0) continue;

    let current = root;
    for (let index = 0; index < segments.length; index += 1) {
      const segment = segments[index];
      const pathKey = segments.slice(0, index + 1).join("/");
      const isLeaf = index === segments.length - 1;

      if (isLeaf) {
        current.children.push({
          kind: "leaf",
          key: item.relative_path,
          label: item.name || segment,
          href: item.route_path,
        });
        continue;
      }

      let folder = current.children.find((child) => child.kind === "folder" && child.key === pathKey);
      if (!folder) {
        folder = { kind: "folder", key: pathKey, label: humanizePathSegment(segment), children: [] };
        current.children.push(folder);
      }
      current = folder;
    }
  }

  return sortDocumentNodes(root.children);
}

function sortDocumentNodes(nodes) {
  return [...nodes]
    .sort((left, right) => {
      if (left.kind !== right.kind) return left.kind === "folder" ? -1 : 1;
      return left.label.localeCompare(right.label);
    })
    .map((node) => (node.kind === "folder" ? { ...node, children: sortDocumentNodes(node.children) } : node));
}

function humanizePathSegment(segment) {
  return segment
    .replaceAll("-", " ")
    .replaceAll("_", " ")
    .split(" ")
    .filter(Boolean)
    .map((part) => part.charAt(0).toUpperCase() + part.slice(1))
    .join(" ");
}
