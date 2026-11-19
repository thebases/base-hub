// Loads a classic (non-module) vendor script on demand and memoizes the promise per URL, so
// libraries that only a few pages use (marked, turndown, PouchDB) stay off the critical path of
// /login and /web instead of being parsed by every cold load. A failed load is not memoized, so
// the next caller retries.

const pending = new Map();

function loadScriptOnce(src, isReady = () => false) {
  if (isReady()) return Promise.resolve();
  const existing = pending.get(src);
  if (existing) return existing;
  const promise = new Promise((resolve, reject) => {
    const script = document.createElement("script");
    script.src = src;
    script.async = true;
    script.onload = () => resolve();
    script.onerror = () => {
      pending.delete(src);
      reject(new Error(t("Failed to load script {src}", { src })));
    };
    document.head.appendChild(script);
  });
  pending.set(src, promise);
  return promise;
}

// vendorURL resolves a path under the SPA's vendor/ folder against the document base ("/").
function vendorURL(name) {
  return new URL(`./vendor/${name}`, document.baseURI).href;
}

export function loadMarked() {
  return loadScriptOnce(vendorURL("marked.min.js"), () => Boolean(window.marked));
}

export function loadTurndown() {
  return loadScriptOnce(vendorURL("turndown.js"), () => Boolean(window.TurndownService));
}

export function loadPouchDB() {
  return loadScriptOnce(vendorURL("pouchdb-9.0.0.min.js"), () => Boolean(window.PouchDB));
}
