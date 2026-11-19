// leaflet-loader.js — lazy-loads the vendored Leaflet bundle (JS + CSS +
// marker images, downloaded once under vendor/leaflet/) exactly once per page
// load, mirroring StudioWorkspace.js's loadBaseWebEditor() pattern for
// third-party assets this app doesn't bundle via npm/importmap.

const LEAFLET_CSS_URL = new URL("../../../../../vendor/leaflet/leaflet.css", import.meta.url).toString();
const LEAFLET_SCRIPT_URL = new URL("../../../../../vendor/leaflet/leaflet.js", import.meta.url).toString();
const MARKER_ICON_URL = new URL("../../../../../vendor/leaflet/images/marker-icon.png", import.meta.url).toString();
const MARKER_ICON_2X_URL = new URL("../../../../../vendor/leaflet/images/marker-icon-2x.png", import.meta.url).toString();
const MARKER_SHADOW_URL = new URL("../../../../../vendor/leaflet/images/marker-shadow.png", import.meta.url).toString();

let loadPromise = null;

/** Injects the vendored Leaflet stylesheet + script once and resolves with window.L. */
export function loadLeaflet() {
  if (window.L) return Promise.resolve(window.L);
  if (loadPromise) return loadPromise;
  loadPromise = new Promise((resolve, reject) => {
    if (!document.querySelector("link[data-leaflet-style]")) {
      const link = document.createElement("link");
      link.rel = "stylesheet";
      link.href = LEAFLET_CSS_URL;
      link.dataset.leafletStyle = "true";
      document.head.appendChild(link);
    }
    const script = document.createElement("script");
    script.src = LEAFLET_SCRIPT_URL;
    script.async = true;
    script.onload = () => {
      if (!window.L) {
        reject(new Error("leaflet.js loaded but window.L is missing"));
        return;
      }
      // Point the default marker icon at the vendored images explicitly —
      // Leaflet's own auto-detection of its CSS/image base path is brittle
      // once the script isn't served from its own package layout.
      window.L.Icon.Default.mergeOptions({
        iconUrl: MARKER_ICON_URL,
        iconRetinaUrl: MARKER_ICON_2X_URL,
        shadowUrl: MARKER_SHADOW_URL,
      });
      resolve(window.L);
    };
    script.onerror = () => reject(new Error("Failed to load leaflet.js"));
    document.body.appendChild(script);
  });
  return loadPromise;
}
