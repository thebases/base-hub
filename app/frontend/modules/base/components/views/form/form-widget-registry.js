// Named form-widget registry.
//
// A form arch `<widget name="some_widget"/>` (no children) resolves here to a
// module-owned Owl component, lazily imported the same way router.js lazily
// imports page modules — so the base bundle never statically depends on a
// feature module (edc, ...), and a module's widget code only loads when a
// form that uses it actually renders.
//
// Entry shape: name -> { module: "@/modules/<mod>/widgets/<X>.js", export: "<ExportName>" }.
// The mounted component receives these props (all optional on its side):
//   ctx      — FormView.archCtx (values, fieldMap, model, isReadonly,
//              isCreateMode, onFieldChange, ...)
//   node     — the parsed arch node (attrs holds every raw XML attribute,
//              so widget-specific options can be passed as attributes)
//   record   — shorthand for ctx.values
//   readonly — shorthand for ctx.isReadonly
//
// Base modules add a line below; any other module registers its widgets from its own
// frontend/modules/<mod>/register.js through registerFormWidgets (see module-registry.js), so this
// base file never names an edition module.
const FORM_WIDGETS = {
  // base module
  address_view: { module: "@/modules/base/widgets/AddressView/AddressView.js", export: "AddressView" },
  // workflow module
  workflow_lua_script: { module: "@/modules/workflow/widgets/LuaScriptParams/LuaScriptParams.js", export: "LuaScriptParams" },
};

// registerFormWidgets merges a module's name -> { module, export } entries into the registry.
export function registerFormWidgets(entries) {
  Object.assign(FORM_WIDGETS, entries ?? {});
}

// isFormWidget tells the form renderer whether a `<widget name="..."/>` belongs to this registry,
// so unregistered legacy widgets (web_ribbon, ...) keep their generic rendering.
export function isFormWidget(name) {
  return Object.hasOwn(FORM_WIDGETS, name ?? "");
}

const loaded = new Map();

// resolveFormWidget imports (once) and returns the component class registered
// under `name`, or null when the name is unknown.
export async function resolveFormWidget(name) {
  const entry = FORM_WIDGETS[name];
  if (!entry) return null;
  if (!loaded.has(name)) {
    loaded.set(name, import(entry.module).then((mod) => mod[entry.export] ?? mod.default ?? null));
  }
  return loaded.get(name);
}
