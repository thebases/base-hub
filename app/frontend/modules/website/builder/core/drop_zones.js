// A snippet may restrict where it can be dropped with `allowedIn`: a list of CSS selectors the
// container must match (the contract behind data-snippet-allowed-in). No list means anywhere.
export function canInsertInto(snippet, container) {
  const allowed = snippet.allowedIn;
  if (!allowed || allowed.length === 0) return true;
  return allowed.some((selector) => container.matches(selector));
}

// Top-level page regions (<header>/<main>/<footer>), found even when the page wraps them in a container
// (the seeded pages put them inside `#site-page`). Regions nested in another region are not top-level.
export function pageRegions(root) {
  return [...root.querySelectorAll("header, main, footer")].filter((el) => {
    // The editable root is itself a <main> (data-region="page"), so only ancestors below it count.
    const outer = el.parentElement.closest("header, main, footer");
    return !outer || outer === root || !root.contains(outer);
  });
}
