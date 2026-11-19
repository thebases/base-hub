// Port of frontend-admin's modules/base/api/health.ts, retargeted to this
// app's fetch substrate (buildBackendURL/authenticatedHeaders from
// services/api/jsonv2.js) instead of a hand-rolled authHeaders()/
// BACKEND_API_PREFIX pair — same shape as api/apidoc.js's readJSON. Backend
// contract is unchanged: GET /health/detail. Unlike apidoc.js's readJSON,
// this explicitly dispatches the shared AUTH_INVALID_EVENT on a 401, since
// this endpoint is hit outside of any BaseJsonV2Rpc call (which already does
// that internally on 401/403) and health.ts's own authHeaders-based fetch did
// the same dispatch explicitly.

import { buildBackendURL, authenticatedHeaders, emitInvalidAuthEvent } from "@/services/api/jsonv2.js";

const REQUEST_TIMEOUT_MS = 15000;

export async function getHealthDetail() {
  const controller = new AbortController();
  const timeoutID = window.setTimeout(() => controller.abort(), REQUEST_TIMEOUT_MS);
  try {
    const response = await fetch(buildBackendURL("/health/detail"), {
      method: "GET",
      headers: authenticatedHeaders(),
      signal: controller.signal,
    });
    const payloadText = await response.text();
    const payload = payloadText ? JSON.parse(payloadText) : null;
    if (!response.ok) {
      if (response.status === 401) {
        emitInvalidAuthEvent();
      }
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
      throw new Error(t("Invalid JSON response from health check endpoint"));
    }
    if (error instanceof Error) throw error;
    throw new Error(t("Unknown health check request error"));
  } finally {
    window.clearTimeout(timeoutID);
  }
}
