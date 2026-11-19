// Port of frontend-admin's modules/knowledge/pages/markdown-page.tsx. Dialogs
// use BaseUI's pure-Owl `Dialog`/`DialogContent` controlled open/onOpenChange
// API; toasts use the pure-Owl `Toast` component. The MDXEditor-based editor
// is replaced by a plain textarea (MarkdownEdit) per the Phase 4 plan's
// user-confirmed decision — the Edit/Preview toggle below is unchanged from
// React, it just swaps substrate.

import { Component, loadTemplate, onMounted, onWillStart, onWillUnmount, useState, xml } from "nodexui/owl";

import {
  createMarkdownDocument,
  deleteMarkdownDocument,
  emitMarkdownMenuUpdated,
  fetchMarkdownDocument,
  fetchMarkdownMenu,
  renameMarkdownDocument,
  saveMarkdownDocument,
} from "@/modules/knowledge/api.js";
import { extractMarkdownHeadings } from "@/modules/knowledge/markdown-headings.js";
import { showToast } from "@/modules/base-bus/bus-root.js";
import { MarkdownEdit } from "@/modules/knowledge/components/MarkdownEdit.js";
import { MarkdownViewer } from "@/modules/knowledge/components/MarkdownViewer.js";
import { Alert, Badge, Button, Dialog, DialogContent, Input, Spinner } from "nodexui/component";

const template = await loadTemplate(new URL("./MarkdownPage.xml", import.meta.url));

const MARKDOWN_ROUTE = "/web/markdown";
const PUBLIC_MARKDOWN_ROUTE = "/view-md";

function currentMarkdownPath() {
  if (typeof window === "undefined") return "";
  return new URLSearchParams(window.location.search).get("path") ?? "";
}

function resolveMarkdownSelection(items, path) {
  if (!path) return "";
  const exactMatch = items.find((item) => item.relative_path === path);
  if (exactMatch) return exactMatch.relative_path;
  const normalizedLegacyPath = stripMarkdownNamespace(path);
  const legacyMatch = items.find((item) => stripMarkdownNamespace(item.relative_path) === normalizedLegacyPath);
  return legacyMatch?.relative_path ?? "";
}

function buildMarkdownRoute(path) {
  if (!path) return MARKDOWN_ROUTE;
  const params = new URLSearchParams({ path });
  return `${MARKDOWN_ROUTE}?${params.toString()}`;
}

function pushMarkdownRoute(path) {
  window.history.pushState(null, "", buildMarkdownRoute(path));
}

function replaceMarkdownRoute(path) {
  window.history.replaceState(null, "", buildMarkdownRoute(path));
}

function normalizeMarkdownPath(path) {
  const trimmed = path.trim().replaceAll("\\", "/").replace(/^\/+/, "");
  if (!trimmed) return "";
  return trimmed.toLowerCase().endsWith(".md") ? trimmed : `${trimmed}.md`;
}

// normalizePublicMarkdownPath always resolves into the writable "public"
// namespace: base (and its nested user_guide) is read-only, so the UI must
// only ever let a user create or move documents under public.
function normalizePublicMarkdownPath(path) {
  const normalized = normalizeMarkdownPath(stripMarkdownNamespace(path));
  if (!normalized) return "";
  return `public/${normalized}`;
}

function currentPublicMarkdownDirectory(path) {
  const normalized = stripMarkdownNamespace(path);
  const lastSlash = normalized.lastIndexOf("/");
  if (lastSlash === -1) return "";
  return normalized.slice(0, lastSlash);
}

function stripMarkdownNamespace(path) {
  const normalized = path.trim().replaceAll("\\", "/").replace(/^\/+/, "");
  if (normalized === "base" || normalized === "public") return "";
  if (normalized.startsWith("base/")) return normalized.slice("base/".length);
  if (normalized.startsWith("public/")) return normalized.slice("public/".length);
  return normalized;
}

function combineMarkdownPath(directory, fileName) {
  const cleanDirectory = directory.trim().replaceAll("\\", "/").replace(/^\/+|\/+$/g, "");
  const cleanFileName = fileName.trim().replaceAll("\\", "/").replace(/^\/+/, "");
  if (!cleanDirectory) return cleanFileName;
  if (!cleanFileName) return cleanDirectory;
  return `${cleanDirectory}/${cleanFileName}`;
}

function buildPublicMarkdownRoute(path) {
  const encodedPath = path
    .split("/")
    .filter((segment) => segment.length > 0)
    .map((segment) => encodeURIComponent(segment))
    .join("/");
  return new URL(`${PUBLIC_MARKDOWN_ROUTE}/${encodedPath}`, window.location.origin).toString();
}

export class MarkdownPage extends Component {
  static template = xml({ raw: [template] });
  static components = { MarkdownEdit, MarkdownViewer, Alert, Badge, Button, Dialog, DialogContent, Input, Spinner };

  setup() {
    this.state = useState({
      menuState: { status: "loading", items: [] },
      selectedPath: currentMarkdownPath(),
      documentState: { status: "idle", content: "" },
      draft: "",
      isEditing: false,
      isMutating: false,
      createDialogOpen: false,
      renameDialogOpen: false,
      shareDialogOpen: false,
      newFileLabel: "",
      newFileName: "",
      newFilePath: "",
      renamePath: "",
    });
    this.onPopState = () => {
      this.state.selectedPath = currentMarkdownPath();
      void this.loadDocument();
    };

    onWillStart(() => this.loadMenu());
    onMounted(() => window.addEventListener("popstate", this.onPopState));
    onWillUnmount(() => window.removeEventListener("popstate", this.onPopState));
  }

  get selectedItem() {
    return this.state.menuState.items.find((item) => item.relative_path === this.state.selectedPath) ?? null;
  }

  get canEditSelected() {
    return Boolean(this.selectedItem && !this.selectedItem.readonly);
  }

  get isDirty() {
    return this.state.draft !== this.state.documentState.content;
  }

  get headings() {
    return extractMarkdownHeadings(this.state.draft);
  }

  get shareURL() {
    return this.state.selectedPath ? buildPublicMarkdownRoute(this.state.selectedPath) : "";
  }

  async loadMenu() {
    try {
      const items = await fetchMarkdownMenu();
      this.state.menuState = { status: "ready", items };
      const urlPath = currentMarkdownPath();
      const nextPath = resolveMarkdownSelection(items, urlPath) || items[0]?.relative_path || "";
      this.state.selectedPath = nextPath;
      if (!urlPath && nextPath) {
        replaceMarkdownRoute(nextPath);
      } else if (urlPath && urlPath !== nextPath) {
        replaceMarkdownRoute(nextPath);
      }
      await this.loadDocument();
    } catch (error) {
      this.state.menuState = { status: "error", items: [], message: error instanceof Error ? error.message : t("Failed to load markdown menu") };
    }
  }

  async refreshMenu() {
    const items = await fetchMarkdownMenu();
    this.state.menuState = { status: "ready", items };
    return items;
  }

  async loadDocument() {
    const path = this.state.selectedPath;
    if (!path) {
      this.state.documentState = { status: "idle", content: "" };
      this.state.draft = "";
      this.state.renamePath = "";
      return;
    }
    this.state.documentState = { status: "loading", content: this.state.documentState.content };
    try {
      const content = await fetchMarkdownDocument(path);
      this.state.documentState = { status: "ready", content };
      this.state.draft = content;
      this.state.renamePath = path;
      if (this.selectedItem?.readonly) this.state.isEditing = false;
    } catch (error) {
      this.state.documentState = { status: "error", content: "", message: error instanceof Error ? error.message : t("Failed to load markdown document") };
    }
  }

  selectPath(path) {
    if (path === this.state.selectedPath) return;
    this.state.selectedPath = path;
    pushMarkdownRoute(path);
    void this.loadDocument();
  }

  setDraft(value) {
    this.state.draft = value;
  }

  startEdit() {
    this.state.isEditing = true;
  }

  stopEdit() {
    this.state.isEditing = false;
  }

  openCreateDialog() {
    this.state.newFileLabel = "";
    this.state.newFileName = "";
    this.state.newFilePath = currentPublicMarkdownDirectory(this.state.selectedPath);
    this.state.createDialogOpen = true;
  }

  closeCreateDialog() {
    this.state.createDialogOpen = false;
  }

  openRenameDialog() {
    this.state.renamePath = this.state.selectedPath;
    this.state.renameDialogOpen = true;
  }

  closeRenameDialog() {
    this.state.renameDialogOpen = false;
  }

  openShareDialog() {
    if (!this.state.selectedPath) return;
    this.state.shareDialogOpen = true;
  }

  closeShareDialog() {
    this.state.shareDialogOpen = false;
  }

  async handleCreate() {
    const normalizedPath = normalizePublicMarkdownPath(combineMarkdownPath(this.state.newFilePath, this.state.newFileName));
    if (!normalizedPath) return this.notify(t("Enter a markdown file name"), "danger");
    if (!this.state.newFileLabel.trim()) return this.notify(t("Enter a menu label"), "danger");

    this.state.isMutating = true;
    try {
      await createMarkdownDocument(this.state.newFileLabel.trim(), normalizedPath);
      await this.refreshMenu();
      emitMarkdownMenuUpdated();
      this.state.selectedPath = normalizedPath;
      this.state.draft = "";
      this.state.documentState = { status: "ready", content: "" };
      this.closeCreateDialog();
      this.state.isEditing = true;
      pushMarkdownRoute(normalizedPath);
      this.notify(t("Markdown file created"));
    } catch (error) {
      this.notify(error instanceof Error ? error.message : t("Failed to create markdown file"), "danger");
    } finally {
      this.state.isMutating = false;
    }
  }

  async handleSave() {
    if (!this.state.selectedPath) return;
    if (!this.canEditSelected) return this.notify(t("Base markdown documents are read-only"), "danger");

    this.state.isMutating = true;
    try {
      await saveMarkdownDocument(this.state.selectedPath, this.state.draft);
      this.state.documentState = { status: "ready", content: this.state.draft };
      this.state.isEditing = false;
      await this.refreshMenu();
      emitMarkdownMenuUpdated();
      this.notify(t("Markdown file saved"));
    } catch (error) {
      this.notify(error instanceof Error ? error.message : t("Failed to save markdown file"), "danger");
    } finally {
      this.state.isMutating = false;
    }
  }

  async handleRename() {
    if (!this.state.selectedPath) return;
    if (!this.canEditSelected) return this.notify(t("Base markdown documents are read-only"), "danger");
    const normalizedPath = normalizePublicMarkdownPath(this.state.renamePath);
    if (!normalizedPath) return this.notify(t("Enter a markdown file name"), "danger");

    this.state.isMutating = true;
    try {
      await renameMarkdownDocument(this.state.selectedPath, normalizedPath);
      await this.refreshMenu();
      emitMarkdownMenuUpdated();
      this.state.selectedPath = normalizedPath;
      this.state.renamePath = normalizedPath;
      this.closeRenameDialog();
      pushMarkdownRoute(normalizedPath);
      this.notify(t("Markdown file renamed"));
    } catch (error) {
      this.notify(error instanceof Error ? error.message : t("Failed to rename markdown file"), "danger");
    } finally {
      this.state.isMutating = false;
    }
  }

  async handleDelete() {
    const path = this.state.selectedPath;
    if (!path || !this.canEditSelected) {
      if (path && !this.canEditSelected) this.notify(t("Base markdown documents are read-only"), "danger");
      return;
    }
    if (!window.confirm(`Delete ${path}?`)) return;
    this.state.isMutating = true;
    try {
      await deleteMarkdownDocument(path);
      const items = await this.refreshMenu();
      emitMarkdownMenuUpdated();
      const nextPath = items[0]?.relative_path ?? "";
      this.state.selectedPath = nextPath;
      this.state.isEditing = false;
      replaceMarkdownRoute(nextPath);
      await this.loadDocument();
      this.notify(t("Markdown file deleted"));
    } catch (error) {
      this.notify(error instanceof Error ? error.message : t("Failed to delete markdown file"), "danger");
    } finally {
      this.state.isMutating = false;
    }
  }

  async handleCopyShareLink() {
    if (!this.shareURL) return;
    try {
      await navigator.clipboard.writeText(this.shareURL);
      this.notify(t("Share link copied"));
    } catch (error) {
      this.notify(error instanceof Error ? error.message : t("Failed to copy share link"), "danger");
    }
  }

  handleOpenShareTarget() {
    if (!this.shareURL) return;
    window.open(this.shareURL, "_blank", "noopener,noreferrer");
  }

  // Delegates to the app's single global toast (Shell.js's fixed top-right
  // overlay) instead of keeping page-local notification state — see
  // base-bus/bus-root.js's showToast() doc comment.
  notify(message, variant = "info") {
    showToast({ message, variant });
  }
}
