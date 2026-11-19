// Port of frontend-admin's modules/base/pages/apidoc-page.tsx. Fixes the
// "/web/apidoc" 400 ("orm: model \"apidoc\" is not registered"): main.js's
// generic "/web/:actionModel" route was swallowing this path and treating
// "apidoc" as an ORM model, since this page never existed in the Owl rewrite
// (see frontend-context.md's known-gap entry). Backend contract is the
// standalone /doc/*.json + arbitrary runtime-method routes documented by
// them, not a registered ORM model — this page talks to it directly via
// modules/base/api/apidoc.js, same as MarkdownPage talks to /md/*.

import { Component, loadTemplate, onWillStart, useState, xml } from "nodexui/owl";

import { apiDocPath, executeApiDocRequest, getApiDocGuide, getApiDocIndex, getApiDocLanguage, getApiDocModel } from "@/modules/base/api/apidoc.js";
import { buildBackendURL } from "@/services/api/jsonv2.js";
import { Alert, Badge, Button, Card, CardBody, CardHeader, CardTitle, Dialog, DialogContent, DialogHeader, DialogTitle, DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger, Icon, Input, Markdown, Spinner, Table, TabContent, Tabs, TabsContainer, TabsList, TabTrigger, Textarea } from "nodexui/component";

const template = await loadTemplate(new URL("./ApiDocPage.xml", import.meta.url));

function sampleValueForField(field) {
  if (field.selection && field.selection.length > 0) return field.selection[0].value;
  switch (field.kind) {
    case "boolean":
      return false;
    case "many2one":
    case "one2one":
    case "one2many":
    case "many2many":
      return false;
    case "id":
    case "integer":
    case "float":
      return 0;
    case "json":
      return field.type.startsWith("[]") ? [] : {};
    case "date":
      return "2026-01-01";
    case "datetime":
      return "2026-01-01T00:00:00Z";
    default:
      return "";
  }
}

function includeWritableField(field) {
  if (field.name === "id") return false;
  if (field.readonly || field.compute) return false;
  return true;
}

function writableFieldExample(fields) {
  const writableNames = Object.keys(fields).filter((name) => includeWritableField(fields[name])).sort();
  return Object.fromEntries(writableNames.map((name) => [name, sampleValueForField(fields[name])]));
}

function methodNameNeedsArgs(path) {
  return path.endsWith("/name_search") || path.endsWith("/name_create");
}

function defaultRequestBodyForMethod(method, fields) {
  if (method.request_example !== undefined) {
    return JSON.stringify(method.request_example, null, 2);
  }
  const fieldExample = writableFieldExample(fields);
  const fieldNames = Object.keys(fields).sort();
  const kwargs = {};
  const args = [];
  for (const parameter of Object.values(method.parameters)) {
    switch (parameter.name) {
      case "filter":
      case "domain":
        kwargs.filter = {};
        break;
      case "fields":
        if (method.path.endsWith("/default_get") || method.path.endsWith("/fields_get")) {
          args.push(fieldNames);
        } else {
          kwargs.fields = [];
        }
        break;
      case "groupby":
        kwargs.groupby = [];
        break;
      case "limit":
        kwargs.limit = parameter.default ?? 20;
        break;
      case "offset":
        kwargs.offset = parameter.default ?? 0;
        break;
      case "order":
      case "orderby":
        kwargs.order = parameter.default ?? "id asc";
        break;
      case "values":
        args.push(fieldExample);
        break;
      case "changed_fields":
        args.push([]);
        break;
      case "fields_spec":
        kwargs.fields_spec = {};
        break;
      case "ids":
        if (method.read_only && method.model_only) args.push([]);
        break;
      case "name":
        if (methodNameNeedsArgs(method.path)) args.push("");
        break;
    }
  }
  const payload = {};
  if (args.length > 0) payload.args = args;
  if (Object.keys(kwargs).length > 0) payload.kwargs = kwargs;
  return JSON.stringify(payload, null, 2);
}

export class ApiDocPage extends Component {
  static template = xml({ raw: [template] });
  static components = { Alert, Badge, Button, Card, CardBody, CardHeader, CardTitle, Dialog, DialogContent, DialogHeader, DialogTitle, DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger, Icon, Input, Markdown, Spinner, Table, TabContent, Tabs, TabsContainer, TabsList, TabTrigger, Textarea };

  setup() {
    this.state = useState({
      lang: getApiDocLanguage(),
      status: "loading",
      message: "",
      index: null,
      query: "",
      catalogCollapsed: false,
      guide: "",
      guideStatus: "loading",
      guideMessage: "",
      docDialog: { open: false, title: "", content: "" },
      selectedModel: "",
      modelDoc: null,
      modelStatus: "idle",
      modelMessage: "",
      selectedMethodName: "",
      route: "",
      requestBody: "",
      tryStatus: "idle",
      tryMessage: "",
      tryResponse: null,
    });

    onWillStart(() => Promise.all([this.loadIndex(), this.loadGuide()]));
  }

  // Loads the README usage guide independently so a guide failure never blocks the reference tab.
  async loadGuide() {
    this.state.guideStatus = "loading";
    try {
      this.state.guide = await getApiDocGuide(this.state.lang);
      this.state.guideStatus = "ready";
    } catch (error) {
      this.state.guideStatus = "error";
      this.state.guideMessage = error instanceof Error ? error.message : t("Failed to load the API guide");
    }
  }

  get filteredModels() {
    if (!this.state.index) return [];
    const needle = this.state.query.trim().toLowerCase();
    if (!needle) return this.state.index.models;
    return this.state.index.models.filter(
      (model) => model.model.toLowerCase().includes(needle) || model.name.toLowerCase().includes(needle),
    );
  }

  get sortedFields() {
    if (!this.state.modelDoc) return [];
    return Object.values(this.state.modelDoc.fields).sort((a, b) => a.name.localeCompare(b.name));
  }

  get sortedMethods() {
    if (!this.state.modelDoc) return [];
    return Object.entries(this.state.modelDoc.methods).sort(([a], [b]) => a.localeCompare(b));
  }

  get selectedMethod() {
    const found = this.sortedMethods.find(([name]) => name === this.state.selectedMethodName);
    return found ? found[1] : null;
  }

  get openApiURL() {
    return buildBackendURL(apiDocPath("/doc/openapi.json", this.state.lang));
  }

  async loadIndex() {
    this.state.status = "loading";
    try {
      const index = await getApiDocIndex(this.state.lang);
      this.state.index = index;
      this.state.status = "ready";
      const firstModel = index.models[0]?.model ?? "";
      if (firstModel) await this.selectModel(firstModel);
    } catch (error) {
      this.state.status = "error";
      this.state.message = error instanceof Error ? error.message : t("Failed to load API documentation index");
    }
  }

  // Unlike loadIndex() (used on first mount, when nothing is selected yet),
  // refresh() must keep the user's current model selection when it still
  // exists in the reloaded index, only falling back to the first model when
  // it doesn't — matches React's onRefresh in apidoc-page.tsx. Re-fetches the
  // model doc even if the selection didn't change (force:true), since the
  // whole point of Refresh is to pull fresh data for what's on screen.
  async refresh() {
    const previousSelectedModel = this.state.selectedModel;
    this.state.status = "loading";
    try {
      const index = await getApiDocIndex(this.state.lang);
      this.state.index = index;
      this.state.status = "ready";
      const selectedModel =
        index.models.find((model) => model.model === previousSelectedModel)?.model ??
        index.models[0]?.model ??
        "";
      if (selectedModel) {
        await this.selectModel(selectedModel, { force: true });
      } else {
        this.state.selectedModel = "";
        this.state.modelDoc = null;
        this.state.modelStatus = "idle";
        this.state.modelMessage = "";
      }
    } catch (error) {
      this.state.status = "error";
      this.state.message = error instanceof Error ? error.message : t("Failed to reload API documentation index");
    }
  }

  async selectModel(modelName, { force = false } = {}) {
    if (!modelName || (!force && modelName === this.state.selectedModel)) return;
    this.state.selectedModel = modelName;
    this.state.modelDoc = null;
    this.state.modelStatus = "loading";
    this.state.modelMessage = "";
    try {
      const modelDoc = await getApiDocModel(modelName, this.state.lang);
      this.state.modelDoc = modelDoc;
      this.state.modelStatus = "ready";
      const firstMethodName = Object.keys(modelDoc.methods).sort()[0] ?? "";
      this.selectMethod(firstMethodName);
    } catch (error) {
      this.state.modelStatus = "error";
      this.state.modelMessage = error instanceof Error ? error.message : t("Failed to load model documentation");
    }
  }

  // Opens the shared popup with the markdown body of a model/method DescriptionFile.
  showDescription(title, content) {
    this.state.docDialog = { open: true, title, content };
  }

  setDocDialogOpen(open) {
    this.state.docDialog.open = open;
  }

  // URL of the standalone base_apidoc viewer, which renders the build-time open_apidoc*.json of all module alias routes.
  get aliasViewerUrl() {
    const code = this.state.lang.split("_")[0];
    return `/vendor/base_apidoc/viewer.html?lang=${code === "vi" ? "vn" : code}`;
  }

  // Collapses or expands the Runtime Catalog so the model detail pane can use the full width.
  toggleCatalog() {
    this.state.catalogCollapsed = !this.state.catalogCollapsed;
  }

  setQuery(value) {
    this.state.query = value;
  }

  selectMethod(methodName) {
    this.state.selectedMethodName = methodName;
    const method = this.sortedMethods.find(([name]) => name === methodName)?.[1];
    this.state.route = method?.path ?? "";
    this.state.requestBody = method ? defaultRequestBodyForMethod(method, this.state.modelDoc?.fields ?? {}) : "";
    this.state.tryStatus = "idle";
    this.state.tryMessage = "";
    this.state.tryResponse = null;
  }

  setRequestBody(value) {
    this.state.requestBody = value;
  }

  async runRequest() {
    try {
      JSON.parse(this.state.requestBody);
    } catch (error) {
      this.state.tryStatus = "error";
      this.state.tryMessage = error instanceof Error ? t("Invalid JSON body: {message}", { message: error.message }) : t("Invalid JSON body");
      this.state.tryResponse = null;
      return;
    }
    this.state.tryStatus = "running";
    this.state.tryResponse = null;
    try {
      const response = await executeApiDocRequest(this.state.route, this.state.requestBody);
      this.state.tryStatus = "success";
      this.state.tryResponse = response;
    } catch (error) {
      this.state.tryStatus = "error";
      this.state.tryMessage = error instanceof Error ? error.message : t("Request execution failed");
    }
  }

  openOpenApiJSON() {
    window.open(this.openApiURL, "_blank", "noopener,noreferrer");
  }
}
