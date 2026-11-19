// JS port of frontend-admin's views/base-view/oauth-client-reveal-result.ts.
// Parses sys.oauth_client secret-generation RPC results into the dialog's
// display shape without exposing any model-specific response plumbing to the
// dialog component.
export function parseOauthClientRevealResult(result) {
  if (!result || typeof result !== "object") return null;
  const clientId = result.oauth_client_id;
  if (typeof clientId !== "string" || !clientId) return null;
  const clientSecret = result.oauth_client_secret;
  return {
    clientId,
    clientSecret: typeof clientSecret === "string" ? clientSecret : null,
    alreadyExists: result.oauth_client_already_exists === true,
  };
}
