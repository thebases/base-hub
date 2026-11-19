// Runtime config for the static, no-bundler SPA. Edit directly per deploy
// target — nothing substitutes these values at build time.
window.__ENV__ = {
  API_GW_URL: "",
  DEFAULT_DB_NAME: "",
  BUS_POLL_INTERVAL_MS: 30000,
  // Server mount prefix (see internal/cmd/server.go's configureStaticAssetRoutes).
  // This app is mounted at root "/", so there is no prefix to strip.
  BASE_URL: "",
};
