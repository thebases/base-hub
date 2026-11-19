import { router } from "@/router/router.js";
import { registerFormWidgets } from "@/modules/base/components/views/form/form-widget-registry.js";

// loadModuleFrontends lets any module extend the SPA without the base bundle naming it: the server
// lists every frontend/modules/<name>/register.js it ships (GET /frontend-modules.json) and each
// file default-exports { formWidgets?: {name -> {module, export}}, routes?: {pattern -> entry} }.
// Failures are logged and skipped so one broken module cannot block the whole app from booting.
export async function loadModuleFrontends() {
  let names = [];
  try {
    // index.html starts this request before the module graph downloads; fall back to fetching here.
    const response = await (window.__frontendModules ?? fetch("/frontend-modules.json", { cache: "no-cache" }));
    if (response.ok) names = (await response.json()).modules ?? [];
  } catch (error) {
    console.error("module frontends: manifest unavailable", error);
    return;
  }
  await Promise.all(
    names.map(async (name) => {
      try {
        const mod = await import(`@/modules/${name}/register.js`);
        const registration = mod.default ?? {};
        registerFormWidgets(registration.formWidgets);
        if (registration.routes) router.register(registration.routes);
      } catch (error) {
        console.error(`module frontends: ${name} register.js failed`, error);
      }
    }),
  );
}
