// Port of frontend-admin's modules/base/api/apidoc.ts, retargeted to this
// app's fetch substrate (buildBackendURL/authenticatedHeaders from
// services/api/jsonv2.js) instead of a hand-rolled authHeaders()/BACKEND_API_PREFIX
// pair. Backend contract is unchanged: /doc/index.json, /doc/:model.json,
// /doc/openapi.json (GET) and arbitrary runtime method routes (POST) documented
// by those payloads.

import { buildBackendURL, authenticatedHeaders } from "@/services/api/jsonv2.js";
import { DEFAULT_LANG } from "@/services/i18n/lang.js";
import { authStore } from "@/stores/auth-service.js";

const REQUEST_TIMEOUT_MS = 15000;
// Returns the app's current UI language (the header language button); /doc/* maps it to a documentation language and falls back to English.
export function getApiDocLanguage() {
  const { uiLang, lang } = authStore.getState();
  return uiLang || lang || DEFAULT_LANG;
}

// Appends ?lang=<code> to a /doc path.
export function apiDocPath(path, lang) {
  return `${path}${path.includes("?") ? "&" : "?"}lang=${encodeURIComponent(lang)}`;
}

async function readJSON(path) {
  const controller = new AbortController();
  const timeoutID = window.setTimeout(() => controller.abort(), REQUEST_TIMEOUT_MS);
  try {
    const response = await fetch(buildBackendURL(path), {
      method: "GET",
      headers: authenticatedHeaders(),
      signal: controller.signal,
      cache: "no-store",
    });
    const payloadText = await response.text();
    const payload = payloadText ? JSON.parse(payloadText) : null;
    if (!response.ok) {
      const message = payload && typeof payload === "object" && "message" in payload
        ? String(payload.message)
        : t("Request failed with status {status}", { status: response.status });
      throw new Error(message);
    }
    return payload;
  } catch (error) {
    if (error instanceof DOMException && error.name === "AbortError") {
      throw new Error(t("No response received from server"));
    }
    if (error instanceof SyntaxError) {
      throw new Error(t("Invalid JSON response from API docs endpoint"));
    }
    if (error instanceof Error) throw error;
    throw new Error(t("Unknown API docs request error"));
  } finally {
    window.clearTimeout(timeoutID);
  }
}

export function getApiDocIndex(lang = DEFAULT_API_DOC_LANGUAGE) {
  return readJSON(apiDocPath("/doc/index.json", lang));
}

export function getApiDocModel(modelName, lang = DEFAULT_API_DOC_LANGUAGE) {
  return readJSON(apiDocPath(`/doc/${encodeURIComponent(modelName)}.json`, lang));
}

export async function executeApiDocRequest(path, body) {
  const controller = new AbortController();
  const timeoutID = window.setTimeout(() => controller.abort(), REQUEST_TIMEOUT_MS);
  try {
    const response = await fetch(buildBackendURL(path), {
      method: "POST",
      headers: authenticatedHeaders({ "Content-Type": "application/json" }),
      body,
      signal: controller.signal,
      cache: "no-store",
    });
    const payloadText = await response.text();
    let formattedBody = payloadText;
    if (payloadText) {
      try {
        formattedBody = JSON.stringify(JSON.parse(payloadText), null, 2);
      } catch {
        formattedBody = payloadText;
      }
    }
    return { status: response.status, body: formattedBody || t("(empty response)") };
  } catch (error) {
    if (error instanceof DOMException && error.name === "AbortError") {
      throw new Error(t("No response received from server"));
    }
    if (error instanceof Error) throw error;
    throw new Error(t("Unknown API docs request error"));
  } finally {
    window.clearTimeout(timeoutID);
  }
}

// Fetches the markdown usage guide (documents/README.md) as plain text.
export async function getApiDocGuide(lang = DEFAULT_API_DOC_LANGUAGE) {
  const response = await fetch(buildBackendURL(apiDocPath("/doc/guide.md", lang)), {
    method: "GET",
    headers: authenticatedHeaders(),
    cache: "no-store",
  });
  if (!response.ok) throw new Error(t("Request failed with status {status}", { status: response.status }));
  return response.text();
}

// Lists instruction documents per module (kernel docs plus installed modules').
export function getInstructionIndex(lang = DEFAULT_LANG) {
  return readJSON(apiDocPath("/doc/instructions.json", lang));
}

// Fetches one instruction document as markdown text.
export async function getInstruction(module, path, lang = DEFAULT_LANG) {
  const query = `/doc/instruction.md?module=${encodeURIComponent(module)}&path=${encodeURIComponent(path)}`;
  const response = await fetch(buildBackendURL(apiDocPath(query, lang)), {
    method: "GET",
    headers: authenticatedHeaders(),
    cache: "no-store",
  });
  if (!response.ok) throw new Error(t("Request failed with status {status}", { status: response.status }));
  return response.text();
}
