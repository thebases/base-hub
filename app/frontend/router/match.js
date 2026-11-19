// Minimal param-route matcher: turns "/web/:actionModel/:recordId?" style
// patterns into a regex once, then matches a live pathname against every
// registered pattern in declaration order (first match wins). A trailing
// "*name" segment is a splat: it must be the pattern's last segment and
// greedily captures the rest of the path, slashes included (e.g. "/view-md/*path"
// matching "/view-md/docs/api/overview.md" with path="docs/api/overview.md"),
// mirroring frontend-admin's TanStack "/view-md/$" wildcard route.

function compile(pattern) {
  const paramNames = [];
  const regexSource = pattern
    .replace(/\/+$/, "")
    .split("/")
    .map((segment) => {
      if (!segment) return "";
      if (segment.startsWith("*")) {
        const name = segment.slice(1) || "splat";
        paramNames.push(name);
        return "/(.+)";
      }
      if (segment.startsWith(":")) {
        const optional = segment.endsWith("?");
        const name = segment.slice(1, optional ? -1 : undefined);
        paramNames.push(name);
        return optional ? "(?:/([^/]+))?" : "/([^/]+)";
      }
      return `/${segment}`;
    })
    .join("");
  return { pattern, paramNames, regex: new RegExp(`^${regexSource}/?$`) };
}

export class Router {
  constructor(routeTable) {
    this.compiled = Object.entries(routeTable).map(([pattern, entry]) => ({
      ...compile(pattern),
      entry,
    }));
  }

  // register adds routes owned by a module's register.js. They go in front of the table so a
  // specific module path wins over the "/web/:actionModel" catch-all declared in router.js.
  register(routeTable) {
    const added = Object.entries(routeTable).map(([pattern, entry]) => ({ ...compile(pattern), entry }));
    this.compiled = [...added, ...this.compiled];
  }

  match(pathname) {
    for (const route of this.compiled) {
      const result = route.regex.exec(pathname);
      if (!result) continue;
      const params = {};
      route.paramNames.forEach((name, index) => {
        params[name] = result[index + 1];
      });
      return { entry: route.entry, params, pattern: route.pattern };
    }
    return null;
  }
}
