// Base website builder host page (B0): loads one page + its site, mounts the builder canvas, and wires
// the top bar (undo/redo/remove/discard/save/publish). Editing logic lives in ../../builder.

import { Component, loadTemplate, onMounted, onWillStart, onWillUnmount, useRef, useState, xml } from "nodexui/owl";
import { Icon, TabContent, Tabs, TabsContainer, TabsList, TabTrigger } from "nodexui/component";

import { createModelRpc } from "@/services/api/jsonv2.js";
import { resolveRelationValue } from "@/modules/base/components/views/form/form-field-normalizer.js";
import { uploadImage } from "@/modules/website/builder/host/upload.js";
import { deleteCustomBlock, fetchOptionSchemas, fetchSnippetCatalog, renameCustomBlock, saveCustomBlock } from "@/modules/website/builder/host/catalog.js";
import { createMenuItem, deleteMenuItem, fetchMenuTree, moveMenuItem, updateMenuItem } from "@/modules/website/builder/host/menu_api.js";
import { fetchChromeTemplates, fetchDefaultChrome, previewThemeCss } from "@/modules/website/builder/host/site_api.js";
import { THEME_FIELDS, themeFormFrom, themeFromForm } from "@/modules/website/builder/core/theme_fields.js";
import { createBuilder } from "@/modules/website/builder/builder.js";
import { serializeElement } from "@/modules/website/builder/core/serialize.js";
import { sanitizeHtml } from "@/services/html-sanitize.js";

const template = await loadTemplate(new URL("./BuilderPage.xml", import.meta.url));

const PageRpc = createModelRpc("tbl.website_page");
const SiteRpc = createModelRpc("tbl.website_site");
const TranslationRpc = createModelRpc("tbl.website_page_translation");

// The published site's own CSS, so the canvas shows what ships. Missing before the first publish (404 is harmless).
const CANVAS_CSS = ["/_site/vendor/bootstrap.min.css", "/_site/styles.css"];
// No scripts in the canvas: the iframe is sandboxed without allow-scripts so page HTML never runs while editing.
const CANVAS_JS = [];

const PAGE_FIELDS = ["name", "site_id", "body_html", "revision", "is_indexed", "is_in_menu", "publish_at", "seo", "route_path", "custom_css"];
const SITE_FIELDS = ["header_html", "footer_html", "revision", "theme", "supported_languages", "default_language"];
const TRANSLATION_FIELDS = ["id", "page_id", "lang", "body_html", "revision"];
const SCOPE_FIELD = { header: "header_html", footer: "footer_html" };

// RFC3339 <-> the value format of <input type="datetime-local"> (local time, no zone).
function toLocalInput(iso) {
  const d = iso ? new Date(iso) : null;
  if (!d || Number.isNaN(d.getTime())) return "";
  const pad = (n) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
}
function fromLocalInput(value) {
  const d = value ? new Date(value) : null;
  return d && !Number.isNaN(d.getTime()) ? d.toISOString() : "";
}

function unwrap(response) {
  if (response.error) throw new Error(response.error.message || "Request failed");
  return Array.isArray(response.result) ? response.result[0] : response.result;
}

export class BuilderPage extends Component {
  static template = xml({ raw: [template] });
  static components = { Icon, TabContent, Tabs, TabsContainer, TabsList, TabTrigger };

  setup() {
    this.canvasRef = useRef("canvas");
    this.fileRef = useRef("file");
    this.state = useState({
      title: "", error: "", message: "", busy: false, dirty: false, canUndo: false, canRedo: false, hasSelection: false, canMoveUp: false, canMoveDown: false, linkOpen: false, linkUrl: "", blocks: [], blockQuery: "", tab: "blocks", chromeTemplates: [], chromeCurrent: "", ownChrome: false, optionGroups: [], blockName: "", menu: [], menuNew: { name: "", url: "", parent: "" }, grid: false, lang: "", languages: [], scope: "page", device: "desktop", props: {}, theme: {}, themeGroups: [],
    });
    this.builder = createBuilder();
    this.pageId = Number(this.props.params?.pageId);
    onWillStart(() => this.load());
    onMounted(() => this.mountCanvas());
    onWillUnmount(() => this.builder.plugins.destroy());
  }

  get canvas() {
    return this.builder.plugins.get("canvas");
  }

  async load() {
    if (!Number.isInteger(this.pageId) || this.pageId <= 0) {
      this.state.error = "Website page not found";
      return;
    }
    try {
      const page = unwrap(await PageRpc.read([this.pageId], PAGE_FIELDS));
      const siteId = Number(resolveRelationValue(page?.site_id));
      this.siteId = siteId;
      const site = unwrap(await SiteRpc.read([siteId], SITE_FIELDS));
      this.page = page;
      this.site = site || {};
      this.state.title = page?.name || "Page";
      this.syncPageProps();
      this.syncTheme();
      await this.loadTranslations();
    } catch (error) {
      this.state.error = error instanceof Error ? error.message : "Failed to load page";
    }
  }

  async mountCanvas() {
    if (this.state.error) return;
    const { canvas } = this;
    const blocks = this.builder.plugins.get("blocks");
    this.state.blocks = blocks.catalog();
    // The built-in blocks stay usable if the catalog call fails.
    blocks.loadCatalog(() => fetchSnippetCatalog(this.siteId)).then((list) => { this.state.blocks = list; }).catch(() => {});
    this.plugin("options").load(fetchOptionSchemas).then(() => this.refreshOptions()).catch(() => {});
    this.builder.on("selection-changed", (block) => {
      this.state.hasSelection = !!block;
      this.syncMoveState();
      this.refreshOptions();
      if (block && !this.inserting) this.activateTab("customize");
    });
    this.builder.on("insert-start", () => { this.inserting = true; });
    this.builder.on("insert-end", () => { this.inserting = false; });
    this.themeCss = await previewThemeCss(this.site.theme || {}).catch(() => "");
    await this.mountScope("page");
  }

  // (Re)mounts the canvas on one editable region: the page body, or the site header/footer.
  // Languages other than the site default that this page can be translated into, and the saved translations.
  async loadTranslations() {
    const def = String(this.site.default_language || "en").toLowerCase();
    const supported = Array.isArray(this.site.supported_languages) ? this.site.supported_languages : [];
    this.state.languages = supported.map((l) => String(l).toLowerCase()).filter((l, i, all) => l !== def && all.indexOf(l) === i);
    this.translations = {};
    if (!this.state.languages.length) return;
    const response = await TranslationRpc.search([["page_id", "=", this.pageId]], TRANSLATION_FIELDS, "id ASC", undefined, 50);
    for (const row of response.result || []) this.translations[row.lang] = row;
  }

  // The body shown for the language being edited ("" = the page's own language).
  currentBody() {
    return this.state.lang ? this.translations[this.state.lang]?.body_html || "" : this.page.body_html || "";
  }

  async setLang(lang) {
    if (lang === this.state.lang) return;
    this.plugin("text").flush(true);
    if (this.canvas.getBodyHtml() !== this.canvas.history.savedSnapshot) {
      this.state.error = "Save or discard your changes before switching language.";
      return;
    }
    this.state.error = "";
    this.state.lang = lang;
    const leavePage = lang && this.state.tab === "page";
    await this.guarded(() => this.mountScope("page"));
    if (leavePage) this.activateTab("blocks");
  }

  async mountScope(scope) {
    const { canvas } = this;
    const lang = scope === "page" ? this.state.lang : "";
    // A page that carries its own header/footer (migrated pages, starter templates) is published without
    // the site one, so the canvas shows the same.
    const body = lang ? this.currentBody() || this.page.body_html || "" : this.page.body_html || "";
    this.state.grid = false;
    await canvas.mount(this.canvasRef.el, {
      headerHtml: body.includes('id="site-header"') ? "" : this.site.header_html || "",
      footerHtml: body.includes('id="site-footer"') ? "" : this.site.footer_html || "",
      bodyHtml: lang ? this.currentBody() : this.page.body_html || "",
      cssUrls: CANVAS_CSS,
      jsUrls: CANVAS_JS,
      scope,
      themeCss: this.themeCss,
      pageCss: this.page.custom_css || "",
    });
    this.state.scope = scope;
    // The page's own header/footer (migrated pages, starter templates) replaces the site one on that page.
    this.state.ownChrome = scope !== "page" && (this.page.body_html || "").includes(`id="site-${scope}"`);
    Object.assign(this.state, { dirty: false, canUndo: false, canRedo: false, hasSelection: false });
    canvas.history.on((s) => {
      Object.assign(this.state, { dirty: s.dirty, canUndo: s.canUndo, canRedo: s.canRedo });
      this.syncMoveState();
      this.state.chromeCurrent = this.state.scope === "page" ? "" : this.plugin("chrome").currentName();
    });
    this.plugin("blocks").attachDrop();
    // A new translation starts as a copy of the page: an unsaved edit until saved.
    if (lang && !this.translations[lang]) {
      await canvas.history.run("start translation", () => { canvas.root.innerHTML = sanitizeHtml(this.page.body_html, { profile: "website" }); });
    }
    // A site without a header/footer starts from the default one; it is an unsaved edit until saved.
    const field = SCOPE_FIELD[scope];
    if (field && !String(this.site[field] || "").trim()) {
      const defaults = await fetchDefaultChrome();
      await canvas.history.run("start from default", () => { canvas.root.innerHTML = defaults[field] || ""; });
    }
    this.state.chromeCurrent = scope === "page" ? "" : this.plugin("chrome").currentName();
    // Header/footer scope offers the template picker; it is loaded once per kind and never blocks editing.
    if (scope !== "page") {
      fetchChromeTemplates(scope).then((list) => { this.state.chromeTemplates = list; }).catch(() => { this.state.chromeTemplates = []; });
    }
  }

  // Template tab (header/footer scope): swaps the whole region, keeping the site's own content and settings.
  chromeTemplateName(tpl) {
    return String(tpl.key).split("/").pop();
  }
  // "None" hides the site header/footer: a marker region the canvas can still select and undo; publish skips it.
  noChromeTemplate() {
    const kind = this.state.scope;
    const label = kind === "header" ? "No header" : "No footer";
    return { key: `${kind}/none`, name: "None", description: `Hide the site ${kind} on every page.`,
      html: `<div id="site-${kind}" class="bh-chrome-none text-center text-muted small py-3" data-bh-${kind}="none">${label}</div>` };
  }
  applyChromeTemplate(tpl) {
    return this.guarded(async () => {
      await this.plugin("chrome").apply(tpl.html);
      this.state.chromeCurrent = this.plugin("chrome").currentName();
    });
  }

  // The title returns to the admin home; unsaved edits ask first.
  leave(ev) {
    if (this.state.dirty && !window.confirm("You have unsaved changes. Leave the builder?")) ev.preventDefault();
  }

  async setScope(scope) {
    if (scope === this.state.scope) return;
    this.plugin("text").flush(true);
    if (this.canvas.getBodyHtml() !== this.canvas.history.savedSnapshot) {
      this.state.error = "Save or discard your changes before switching what you edit.";
      return;
    }
    this.state.error = "";
    this.state.chromeTemplates = [];
    await this.guarded(() => this.mountScope(scope));
    // The header/footer opens on its templates; the page has none. (Tab panels switch only through their trigger, see activateTab.)
    if (scope !== "page") this.activateTab("template");
    else if (this.state.tab === "template") this.activateTab("blocks");
  }

  // Menu tab: the site's navigation tree, edited in place (rendered into the header at publish).
  async loadMenu() {
    await this.guarded(async () => { this.state.menu = await fetchMenuTree(this.siteId); });
  }
  // Controlled Tabs: the Menu tab also loads its data.
  setTab(tab) {
    if (tab === "menu") return this.openMenuTab();
    this.state.tab = tab;
  }
  // The tab panels are switched by their trigger (a Bootstrap tab), not by `value`: setting state.tab alone
  // would highlight the trigger while the old panel stayed visible. So programmatic changes click the trigger.
  async activateTab(tab) {
    // A trigger that only exists in some scopes (Template) renders a frame or two after the scope changes.
    let trigger = null;
    for (let i = 0; i < 10 && !trigger; i++) {
      await new Promise((resolve) => requestAnimationFrame(resolve));
      trigger = document.querySelector(`.bh-builder-side [role="tab"][data-bs-target$="-${tab}"]`);
    }
    // The trigger's own shown event updates state.tab; it must not be set first, or Bootstrap sees the tab as already active.
    if (!trigger) {
      this.state.tab = tab;
      return;
    }
    // Selecting several elements in a row re-triggers this while Bootstrap's fade is still running, which can
    // leave the previous pane `active` next to the new one (it then stays on top). A trigger that is already
    // active needs no click.
    if (!trigger.classList.contains("active")) trigger.click();
    const settle = () => {
      const pane = document.querySelector(trigger.getAttribute("data-bs-target") || "#__none__");
      const list = trigger.closest('[role="tablist"]');
      if (!pane || !list) return;
      list.querySelectorAll('[role="tab"]').forEach((t) => {
        t.classList.toggle("active", t === trigger);
        t.setAttribute("aria-selected", String(t === trigger));
      });
      pane.parentElement?.querySelectorAll(":scope > .tab-pane").forEach((p) => {
        if (p !== pane) p.classList.remove("active", "show");
      });
      pane.classList.add("active", "show");
      this.state.tab = tab;
    };
    await new Promise((resolve) => requestAnimationFrame(resolve));
    settle();
    // Bootstrap finishes its own switch after the fade; settle again so its late class changes cannot win.
    setTimeout(settle, 400);
  }
  async openMenuTab() {
    this.state.tab = "menu";
    await this.loadMenu();
  }
  async menuSet(item, field, value) {
    await this.guarded(async () => { await updateMenuItem(item.id, { [field]: value }); });
    await this.loadMenu();
  }
  async menuMove(item, direction) {
    await this.guarded(() => moveMenuItem(this.state.menu, item.id, direction));
    await this.loadMenu();
  }
  async menuRemove(item) {
    await this.guarded(() => deleteMenuItem(item.id));
    await this.loadMenu();
  }
  async menuAdd() {
    const { name, url, parent } = this.state.menuNew;
    if (!name.trim()) {
      this.state.error = "Give the entry a name.";
      return;
    }
    await this.guarded(async () => {
      await createMenuItem(this.siteId, { name: name.trim(), url: url.trim(), parent: Number(parent) || 0 });
      this.state.menuNew = { name: "", url: "", parent: "" };
    });
    await this.loadMenu();
  }

  syncPageProps() {
    const p = this.page || {};
    this.state.props = {
      name: p.name || "", is_indexed: p.is_indexed !== false, is_in_menu: !!p.is_in_menu,
      publish_at: toLocalInput(p.publish_at), seo_title: p.seo?.title || "", seo_description: p.seo?.description || "",
      redirect_old_url: false, route_path: p.route_path || "", custom_css: p.custom_css || "",
    };
  }

  // Writes the page properties (not the body) with the last-read revision; a rename changes the route.
  async applyPageProps() {
    const props = this.state.props;
    await this.guarded(async () => {
      const response = await PageRpc.write([this.pageId], {
        name: props.name, is_indexed: props.is_indexed, is_in_menu: props.is_in_menu,
        publish_at: fromLocalInput(props.publish_at),
        seo: { ...(this.page.seo || {}), title: props.seo_title, description: props.seo_description },
        custom_css: props.custom_css,
        redirect_old_url: !!props.redirect_old_url,
        revision: this.page.revision,
      });
      if (response.error) throw new Error(response.error.message || "Could not save the page properties");
      this.page = unwrap(await PageRpc.read([this.pageId], PAGE_FIELDS));
      this.state.title = this.page.name || "Page";
      this.canvas.setPageCss(this.page.custom_css);
      this.syncPageProps();
      this.state.message = props.publish_at ? "Properties saved, publish scheduled" : "Properties saved";
    });
  }

  syncTheme() {
    this.state.theme = themeFormFrom(this.site.theme);
    const groups = new Map();
    for (const f of THEME_FIELDS) {
      if (!groups.has(f.group)) groups.set(f.group, []);
      groups.get(f.group).push(f);
    }
    this.state.themeGroups = [...groups].map(([name, fields]) => ({ name, fields }));
  }

  // Shows the draft theme in the canvas after a short pause (the stylesheet is rendered server-side).
  setTheme(path, value) {
    this.state.theme[path] = value;
    clearTimeout(this.themeTimer);
    this.themeTimer = setTimeout(async () => {
      try {
        this.themeCss = await previewThemeCss(themeFromForm(this.site.theme, this.state.theme));
        this.canvas.setThemeCss(this.themeCss);
        this.state.error = "";
      } catch (error) {
        this.state.error = error instanceof Error ? error.message : "Theme preview failed";
      }
    }, 300);
  }

  async applyTheme() {
    await this.guarded(async () => {
      const theme = themeFromForm(this.site.theme, this.state.theme);
      const response = await SiteRpc.write([this.siteId], { theme, revision: this.site.revision });
      if (response.error) throw new Error(response.error.message || "Could not save the theme");
      this.site = unwrap(await SiteRpc.read([this.siteId], SITE_FIELDS));
      this.state.message = "Theme saved; publish to make it live";
    });
  }

  // Blocks filtered by the search box (name, group, keywords) and grouped for the sidebar.
  get blockGroups() {
    const q = this.state.blockQuery.trim().toLowerCase();
    const groups = new Map();
    for (const block of this.state.blocks) {
      const hay = [block.name, block.group, ...(block.keywords || [])].join(" ").toLowerCase();
      if (q && !hay.includes(q)) continue;
      const name = block.group || "content";
      if (!groups.has(name)) groups.set(name, []);
      groups.get(name).push(block);
    }
    return [...groups].map(([name, blocks]) => ({ name, blocks }));
  }

  refreshOptions() {
    this.state.optionGroups = this.plugin("options").groupsFor(this.plugin("selection").selected).map((group) => ({
      ...group,
      controls: group.controls.map(({ control, value }) => ({
        control,
        value,
        // Select options are addressed by index so class-, attribute- and style-valued selects render the same way.
        options: control.type === "select" ? control.values.map((v, i) => ({ key: String(i), label: v.label, selected: String(i) === value })) : [],
      })),
    }));
  }
  async setOption(control, value) {
    await this.guarded(() => this.plugin("options").set(control, value));
    this.refreshOptions();
  }
  // Uploads the chosen file and applies its address to an image-valued control.
  async optionImage(control, ev) {
    const file = ev.target.files?.[0];
    ev.target.value = "";
    if (!file) return;
    await this.guarded(async () => this.setOption(control, await uploadImage(file)));
  }
  async renameBlock(block) {
    const name = window.prompt("Block name", block.name);
    if (!name || !name.trim() || name.trim() === block.name) return;
    await this.guarded(async () => {
      await renameCustomBlock(block.key, name.trim());
      this.state.blocks = await this.plugin("blocks").loadCatalog(() => fetchSnippetCatalog(this.siteId));
    });
  }
  async removeBlock(block) {
    if (!window.confirm(`Delete the block "${block.name}"? Pages that already use it are not changed.`)) return;
    await this.guarded(async () => {
      await deleteCustomBlock(block.key);
      this.state.blocks = await this.plugin("blocks").loadCatalog(() => fetchSnippetCatalog(this.siteId));
    });
  }
  async saveBlock() {
    const block = this.plugin("selection").selected;
    if (!block) return;
    await this.guarded(async () => {
      const name = this.state.blockName.trim() || block.dataset.snippet || "Custom block";
      await saveCustomBlock(this.siteId, name, serializeElement(block));
      this.state.blockName = "";
      this.state.blocks = await this.plugin("blocks").loadCatalog(() => fetchSnippetCatalog(this.siteId));
      this.state.message = "Block saved";
    });
  }

  plugin(id) { return this.builder.plugins.get(id); }
  syncMoveState() {
    const overlay = this.plugin("overlay");
    this.state.canMoveUp = overlay.canMove(-1);
    this.state.canMoveDown = overlay.canMove(1);
  }
  // Runs a builder action and surfaces failures in the page instead of an unhandled rejection.
  async guarded(fn) {
    this.state.error = "";
    try {
      await fn();
    } catch (error) {
      this.state.error = error instanceof Error ? error.message : "Action failed";
    }
  }
  format(command) { return this.guarded(() => this.plugin("text").format(command)); }
  move(direction) { return this.guarded(() => this.plugin("overlay").move(direction)); }
  toggleGrid() {
    this.state.grid = !this.state.grid;
    this.plugin("selection").setGridMode(this.state.grid);
  }
  selectParent() { this.plugin("selection").selectParent(); }
  cloneBlock() { return this.guarded(() => this.plugin("overlay").clone()); }
  openLink() {
    if (!this.plugin("text").saveRange()) {
      this.state.error = "Select some text first";
      return;
    }
    this.state.error = "";
    this.state.linkOpen = true;
  }
  async submitLink() {
    await this.guarded(() => this.plugin("text").applyLink(this.state.linkUrl));
    this.state.linkOpen = false;
    this.state.linkUrl = "";
  }
  pickImage() { this.fileRef.el.click(); }
  async imageChosen(ev) {
    const file = ev.target.files?.[0];
    ev.target.value = "";
    if (file) await this.guarded(() => this.plugin("media").insertImageFile(file));
  }

  undo() { this.canvas.history.undo(); }
  redo() { this.canvas.history.redo(); }
  discard() { this.canvas.history.discard(); }
  removeSelected() { return this.builder.plugins.get("remove").removeSelected(); }
  insertBlock(key) { return this.builder.plugins.get("blocks").insert(key); }
  dragBlock(ev, key) {
    ev.dataTransfer.setData("application/x-bh-snippet", key);
    ev.dataTransfer.effectAllowed = "copy";
  }

  // Toolbar entry point: save() already reports the failure in the page, so it is not rethrown here.
  async saveClick() {
    try {
      await this.save();
    } catch {
      /* shown in state.error */
    }
  }

  // Saves body_html with the revision read last: a concurrent edit elsewhere fails the CAS instead of being overwritten.
  async save() {
    this.state.busy = true;
    this.state.error = "";
    try {
      this.plugin("text").flush(true);
      const html = this.canvas.getBodyHtml();
      const field = SCOPE_FIELD[this.state.scope];
      if (this.state.scope === "page" && this.state.lang) {
        const existing = this.translations[this.state.lang];
        const response = existing
          ? await TranslationRpc.write([existing.id], { body_html: html, revision: existing.revision })
          : await TranslationRpc.create({ page_id: this.pageId, lang: this.state.lang, body_html: html });
        if (response.error) throw new Error(response.error.message || "Save failed");
        await this.loadTranslations();
      } else if (field) {
        const response = await SiteRpc.write([this.siteId], { [field]: html, revision: this.site.revision });
        if (response.error) throw new Error(response.error.message || "Save failed");
        this.site = unwrap(await SiteRpc.read([this.siteId], SITE_FIELDS));
      } else {
        const response = await PageRpc.write([this.pageId], { body_html: html, revision: this.page.revision });
        if (response.error) throw new Error(response.error.message || "Save failed");
        this.page = unwrap(await PageRpc.read([this.pageId], PAGE_FIELDS));
      }
      this.canvas.history.markSaved(html);
      this.state.message = "Saved";
    } catch (error) {
      this.state.error = error instanceof Error ? error.message : "Save failed";
      throw error;
    } finally {
      this.state.busy = false;
    }
  }

  async publish() {
    try {
      // Typing may still be inside the commit delay, so compare content, not the cached dirty flag.
      this.plugin("text").flush(true);
      if (this.canvas.getBodyHtml() !== this.canvas.history.savedSnapshot) await this.save();
    } catch {
      return;
    }
    this.state.busy = true;
    try {
      // Header/footer belong to every page, so publishing them republishes the whole site.
      const response = SCOPE_FIELD[this.state.scope]
        ? await SiteRpc.callById("action_publish", String(this.siteId))
        : await PageRpc.callById("action_publish", String(this.pageId));
      if (response.error) throw new Error(response.error.message || "Publish failed");
      this.state.message = "Published";
    } catch (error) {
      this.state.error = error instanceof Error ? error.message : "Publish failed";
    } finally {
      this.state.busy = false;
    }
  }
}
