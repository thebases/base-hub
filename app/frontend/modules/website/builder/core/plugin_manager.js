// Plugin core. A plugin has a unique `id`, optional `dependencies` (plugin ids that must be started
// first) and optional `resources` (named contributions other plugins read, e.g. block catalogs).
// Plugins are plain classes constructed with a shared `ctx`; the manager orders them
// topologically and rejects unknown dependencies and cycles at startup, not at first use.

export class Plugin {
  static id = "";
  static dependencies = [];
  constructor(ctx, plugins) {
    this.ctx = ctx;
    this.plugins = plugins; // resolved dependencies by id
    this.resources = {};
  }
  start() {}
  destroy() {}
}

export class PluginManager {
  constructor(ctx, PluginClasses) {
    this.ctx = ctx;
    this.byId = {};
    this.order = [];
    const classes = new Map();
    for (const P of PluginClasses) {
      if (!P.id) throw new Error("plugin without id");
      if (classes.has(P.id)) throw new Error(`duplicate plugin id: ${P.id}`);
      classes.set(P.id, P);
    }
    const state = new Map(); // id -> 1 visiting, 2 done
    const visit = (id, from) => {
      const P = classes.get(id);
      if (!P) throw new Error(`plugin ${from} depends on unknown plugin ${id}`);
      if (state.get(id) === 2) return;
      if (state.get(id) === 1) throw new Error(`plugin dependency cycle at ${id}`);
      state.set(id, 1);
      for (const dep of P.dependencies || []) visit(dep, id);
      state.set(id, 2);
      this.order.push(P);
    };
    for (const id of classes.keys()) visit(id, "(root)");
    for (const P of this.order) {
      const deps = {};
      for (const dep of P.dependencies || []) deps[dep] = this.byId[dep];
      this.byId[P.id] = new P(ctx, deps);
    }
  }
  get(id) {
    return this.byId[id];
  }
  start() {
    for (const P of this.order) this.byId[P.id].start();
  }
  destroy() {
    for (const P of [...this.order].reverse()) this.byId[P.id].destroy();
  }
  // Collects a named resource list from every plugin, in start order.
  resource(name) {
    return this.order.flatMap((P) => this.byId[P.id].resources[name] || []);
  }
}
