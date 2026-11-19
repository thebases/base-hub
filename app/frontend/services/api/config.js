// Runtime config, ported from frontend-admin's config.ts. Reads window.__ENV__
// (set by ./env.js) instead of Vite's import.meta.env — there is no build step
// on this static tree to substitute VITE_* values at compile time.
const ENV = window.__ENV__ || {};
const DEFAULT_DEV_BACKEND_ORIGIN = "http://localhost:8180";

function normalizeOrigin(value) {
  return value.endsWith("/") ? value.slice(0, -1) : value;
}

function resolveBackendOrigin() {
  const configured = (ENV.API_GW_URL || "").trim();

  if (configured) {
    if (/^https?:\/\//i.test(configured)) {
      return normalizeOrigin(configured);
    }
    if (typeof window !== "undefined") {
      return normalizeOrigin(new URL(configured, window.location.origin).toString());
    }
  }

  if (typeof window !== "undefined") {
    return normalizeOrigin(window.location.origin);
  }
  return DEFAULT_DEV_BACKEND_ORIGIN;
}

export const BACKEND_API_PREFIX = resolveBackendOrigin();

function readPositiveInteger(value, fallback) {
  const parsed = Number(value);
  if (!Number.isFinite(parsed) || parsed <= 0) {
    return fallback;
  }
  return Math.trunc(parsed);
}

export const DEFAULT_DB_NAME = ENV.DEFAULT_DB_NAME || "";
export const BUS_POLL_INTERVAL_MS = readPositiveInteger(ENV.BUS_POLL_INTERVAL_MS, 30_000);
