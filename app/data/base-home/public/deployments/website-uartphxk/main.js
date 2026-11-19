const ROUTES = {
  "/": {
    "css": "./pages/Home/Home.css",
    "module": "./pages/Home/Home.js",
    "name": "Home",
    "seo": {
      "description": "Welcome to our website.",
      "lang": "en",
      "title": "Home"
    }
  }
};
const HOME_ROUTE = "/";

let currentApp = null;
let cssLink = null;

// Per-route head data: title, description, robots, canonical, hreflang alternates, <html lang>.
// Language links are exposed as <link rel="alternate" data-bh-seo> for the language selector.
function setMeta(name, content) {
  let el = document.head.querySelector('meta[name="' + name + '"][data-bh-seo]');
  if (!content) { if (el) el.remove(); return; }
  if (!el) { el = document.createElement("meta"); el.setAttribute("name", name); el.setAttribute("data-bh-seo", ""); document.head.appendChild(el); }
  el.setAttribute("content", content);
}

function applySeo(seo) {
  if (!seo) return;
  document.title = seo.title || document.title;
  document.documentElement.setAttribute("lang", seo.lang || "en");
  setMeta("description", seo.description);
  setMeta("robots", seo.robots);
  document.head.querySelectorAll("link[data-bh-seo]").forEach((el) => el.remove());
  const add = (rel, attrs) => {
    const el = document.createElement("link");
    el.setAttribute("rel", rel);
    el.setAttribute("data-bh-seo", "");
    Object.keys(attrs).forEach((k) => el.setAttribute(k, attrs[k]));
    document.head.appendChild(el);
  };
  if (seo.canonical) add("canonical", { href: seo.canonical });
  (seo.alternates || []).forEach((a) => add("alternate", { hreflang: a.lang, href: a.href }));
  document.documentElement.setAttribute("data-bh-langs", JSON.stringify(seo.langs || []));
}

async function mountRoute(path) {
  const entry = ROUTES[path] ?? ROUTES[HOME_ROUTE];
  if (!entry) return;
  if (!cssLink) {
    cssLink = document.createElement("link");
    cssLink.rel = "stylesheet";
    cssLink.id = "page-css";
    document.head.appendChild(cssLink);
  }
  cssLink.href = entry.css;
  const mod = await import(entry.module);
  const PageComponent = mod[entry.name];
  if (currentApp) {
    currentApp.destroy();
    currentApp = null;
  }
  const root = document.getElementById("app");
  root.innerHTML = "";
  // owl.mount resolves to the root component, not the app, so keep the App to be able to destroy it on navigation.
  const app = new owl.App(PageComponent);
  await app.mount(root);
  currentApp = app;
  applySeo(entry.seo);
}

function navigate(path) {
  if (!(path in ROUTES)) return;
  history.pushState({}, "", path);
  void mountRoute(path);
}

document.body.addEventListener("click", (ev) => {
  const link = ev.target.closest ? ev.target.closest("a[href]") : null;
  if (!link) return;
  const href = link.getAttribute("href");
  if (!href || !(href in ROUTES) || link.target === "_blank") return;
  ev.preventDefault();
  navigate(href);
});

window.addEventListener("popstate", () => mountRoute(location.pathname));

if ("serviceWorker" in navigator) {
  navigator.serviceWorker.register("./sw.js").catch(() => {});
}

void mountRoute(location.pathname);
