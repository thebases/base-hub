// Faithful JS port of frontend-admin's modules/knowledge/api.ts. Talks to the
// existing backend markdown routes (/md/menu.json, /md/:path, /md/api/files*)
// unchanged by this Owl port — only the caller substrate changed.

import { buildBackendURL, authenticatedHeaders } from "@/services/api/jsonv2.js";

export const MARKDOWN_MENU_UPDATED_EVENT = "basehub:markdown-menu-updated";

async function readResponse(response) {
  const text = await response.text();
  if (!text) return null;
  try {
    return JSON.parse(text);
  } catch {
    return text;
  }
}

async function ensureOK(response) {
  if (response.ok) return;
  const payload = await readResponse(response);
  throw new Error(`Response error: ${response.status} - ${JSON.stringify(payload)}`);
}

export async function fetchMarkdownMenu() {
  const response = await fetch(buildBackendURL("/md/menu.json"), {
    headers: authenticatedHeaders(),
    cache: "no-store",
  });
  await ensureOK(response);
  const payload = await readResponse(response);
  // Standard envelope is {result:{items,total}}; keep the bare shape as a fallback for older backends.
  const items = payload?.result?.items ?? payload?.items;
  return Array.isArray(items) ? items : [];
}

export async function fetchMarkdownDocument(relativePath) {
  const response = await fetch(buildBackendURL(`/md/${encodePathSegments(relativePath)}`), {
    headers: authenticatedHeaders({ Accept: "text/markdown, text/plain" }),
    cache: "no-store",
  });
  await ensureOK(response);
  return response.text();
}

export async function fetchPublicMarkdownDocument(relativePath) {
  const response = await fetch(buildBackendURL(`/md/${encodePathSegments(relativePath)}`), {
    headers: { Accept: "text/markdown, text/plain" },
    cache: "no-store",
  });
  await ensureOK(response);
  return response.text();
}

export async function createMarkdownDocument(name, path, content = "") {
  return writeMarkdownMutation("/md/api/files", "POST", { name, path, content });
}

export async function saveMarkdownDocument(path, content) {
  return writeMarkdownMutation(`/md/api/files/${encodePathSegments(path)}`, "PUT", { content });
}

export async function renameMarkdownDocument(fromPath, toPath) {
  const response = await fetch(buildBackendURL("/md/api/files/rename"), {
    method: "POST",
    headers: authenticatedHeaders(),
    cache: "no-store",
    body: JSON.stringify({ from_path: fromPath, to_path: toPath }),
  });
  await ensureOK(response);
}

export async function deleteMarkdownDocument(path) {
  const response = await fetch(buildBackendURL(`/md/api/files/${encodePathSegments(path)}`), {
    method: "DELETE",
    headers: authenticatedHeaders(),
    cache: "no-store",
  });
  await ensureOK(response);
}

function encodePathSegments(path) {
  return path
    .split("/")
    .filter((segment) => segment.length > 0)
    .map((segment) => encodeURIComponent(segment))
    .join("/");
}

async function writeMarkdownMutation(path, method, body) {
  const response = await fetch(buildBackendURL(path), {
    method,
    headers: authenticatedHeaders(),
    cache: "no-store",
    body: JSON.stringify(body),
  });
  await ensureOK(response);
}

export function emitMarkdownMenuUpdated() {
  if (typeof window === "undefined") return;
  window.dispatchEvent(new CustomEvent(MARKDOWN_MENU_UPDATED_EVENT));
}
