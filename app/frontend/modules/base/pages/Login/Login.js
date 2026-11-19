import { Component, loadTemplate, useState, xml } from "nodexui/owl";

import {
  getPinnedRpcDatabaseName,
  getSessionInfo,
  isRpcDatabasePinned,
  oauthLoginPassword,
} from "@/services/api/jsonv2.js";
import { DEFAULT_DB_NAME } from "@/services/api/config.js";
import { authStore } from "@/stores/auth-service.js";
import { navigate } from "@/router/navigation.js";
import { BuildVersionLabel } from "@/modules/base/components/BuildVersionLabel.js";
import {
  Alert,
  Button,
  Icon,
  Input,
} from "nodexui/component";

const template = await loadTemplate(new URL("./Login.xml", import.meta.url));

function normalizeCompanyIDs(raw) {
  if (!Array.isArray(raw)) {
    return [];
  }
  const seen = new Set();
  return raw.flatMap((value) => {
    const parsed = Number(value);
    if (!Number.isFinite(parsed) || parsed <= 0 || seen.has(parsed)) {
      return [];
    }
    seen.add(parsed);
    return [parsed];
  });
}

function normalizeCurrentCompanyID(raw) {
  const parsed = Number(raw);
  return Number.isFinite(parsed) && parsed > 0 ? parsed : null;
}

// authenticateSession exchanges login/password for an OAuth 2.0 token pair via
// POST /oauth/token, then hydrates the profile fields (uid/username/company
// scope/lang) that endpoint doesn't carry (RFC 6749 §5.1) with one follow-up
// GET /oauth/me call using the fresh access token. Ported 1:1 from
// frontend-admin's login-form.tsx.
async function authenticateSession({ database, email, password }) {
  const db = database || DEFAULT_DB_NAME;
  const tokenPair = await oauthLoginPassword({ db, login: email, password });
  const sessionInfo = await getSessionInfo(tokenPair.access_token, db);
  if (!sessionInfo.result?.valid) {
    throw new Error(t("Login response was not valid"));
  }
  const userContext = sessionInfo.result.user_context ?? {};
  return {
    accessToken: tokenPair.access_token,
    refreshToken: tokenPair.refresh_token,
    expiresIn: tokenPair.expires_in,
    db: sessionInfo.result.db || db,
    dbHeaderName: sessionInfo.result.db_header || "X-Database-Name",
    uid: sessionInfo.result.uid,
    username: sessionInfo.result.username || email,
    allowedCompanyIds: normalizeCompanyIDs(userContext.allowed_company_ids),
    companies: Array.isArray(userContext.allowed_companies) ? userContext.allowed_companies : [],
    currentCompanyId: normalizeCurrentCompanyID(userContext.company_id),
    lang:
      typeof userContext.lang === "string" && userContext.lang.trim()
        ? userContext.lang.trim()
        : "en_us",
  };
}

// The menu and bus modules are only needed once a session exists, so they are imported on demand
// to keep them (and their request waterfall) off the signed-out /login critical path.
async function clearSessionCaches() {
  const [{ clearCachedSidebarMenus }, { clearLastNotificationId }] = await Promise.all([
    import("@/modules/base/api/menu.js"),
    import("@/modules/base-bus/bus-service.js"),
  ]);
  clearCachedSidebarMenus();
  clearLastNotificationId();
}

export class Login extends Component {
  static template = xml({ raw: [template] });
  static components = {
    BuildVersionLabel,
    Alert,
    Button,
    Icon,
    Input,
  };

  setup() {
    const authState = authStore.getState();
    const pinnedDBName = getPinnedRpcDatabaseName();
    this.state = useState({
      values: {
        database: pinnedDBName || authState.db || DEFAULT_DB_NAME,
        email: "",
        password: "",
      },
      databasePinned: isRpcDatabasePinned(),
      defaultDbName: DEFAULT_DB_NAME,
      showPassword: false,
      isSubmitting: false,
      loginError: "",
      signedIn: Boolean(authState.accessToken),
      username: authState.username,
    });

    this.unsubscribe = authStore.subscribe((next) => {
      this.state.signedIn = Boolean(next.accessToken);
      this.state.username = next.username;
    });
  }

  willUnmount() {
    this.unsubscribe?.();
  }

  togglePassword() {
    this.state.showPassword = !this.state.showPassword;
  }

  onForgotPassword() {
    // Visual-only for parity with frontend-admin's login-form.tsx — no
    // password-reset flow exists on the backend yet.
  }

  async onSubmit(ev) {
    ev.preventDefault();
    this.state.isSubmitting = true;
    this.state.loginError = "";

    try {
      const credentials = await authenticateSession(this.state.values);
      authStore.setOAuthTokens(
        credentials.username,
        credentials.accessToken,
        credentials.refreshToken,
        credentials.expiresIn,
        {
          db: credentials.db,
          dbHeaderName: credentials.dbHeaderName,
          uid: credentials.uid,
          allowedCompanyIds: credentials.allowedCompanyIds,
          companies: credentials.companies,
          currentCompanyId: credentials.currentCompanyId,
          lang: credentials.lang,
        },
      );
      // Resolve the display timezone before the first screen renders any datetime.
      await authStore.loadTimezone();
      // Hand off to the authenticated shell now that the sidebar (Shell.js,
      // ui.menu-driven) exists — there was nowhere to redirect to before it.
      navigate("/web");
    } catch (error) {
      this.state.loginError =
        error instanceof Error ? error.message : t("Login failed");
    } finally {
      this.state.isSubmitting = false;
    }
  }

  // logout is the "switch account" action shown when the login page is
  // opened while already authenticated — clears the same auth-scoped caches
  // as Shell.js's logout so a subsequent login doesn't inherit stale state.
  async logout() {
    await clearSessionCaches();
    authStore.clearAuth();
  }
}
