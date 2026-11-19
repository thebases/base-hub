package service

// File overview:
// - Responsibility: calls the configured auth endpoint for one sys.credential row and returns the
//   resulting token pair + expiry, dispatching on CredentialType (rbac, oidc_client, oidc_password,
//   apikey). This is the only place in the module that speaks HTTP to a third-party auth endpoint.
// - Key symbols: `LoginCredential`, `callAuthEndpoint`, `parseTokenResponse`.
// - Pattern: Treats every call as best-effort against an untrusted third party — response shape is
//   parsed defensively and failures are wrapped with enough context to diagnose without leaking secrets.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"time"

	"git.thebase.vn/base_hub/internal/services/log"
	credentialmodels "git.thebase.vn/base_hub/modules/base-credential/models"
)

// LoginCredential performs the auth flow for one credential row (looked up by
// id), then persists the resulting token pair via StoreSysCredentialToken. It
// is safe to call concurrently for different credential ids; callers (the
// refresh cron and the manual `login` runtime action) are responsible for not
// calling it twice for the same id at once.
func LoginCredential(ctx context.Context, credentialID int64) error {
	secrets, err := credentialmodels.LookupSysCredentialSecrets(ctx, credentialID)
	if err != nil {
		return err
	}

	// apikey credentials have no login flow: the key is the value the caller
	// already stored in AccessToken on create/write. Nothing to refresh.
	if secrets.CredentialType == credentialmodels.CredentialTypeAPIKey {
		return nil
	}

	accessToken, refreshToken, expiresIn, err := callAuthEndpoint(ctx, secrets)
	if err != nil {
		if recordErr := credentialmodels.StoreSysCredentialLoginFailure(ctx, credentialID, secrets, err); recordErr != nil {
			log.Errorf("sys.credential %d: record login failure: %v", credentialID, recordErr)
		}
		return fmt.Errorf("sys.credential %d login: %w", credentialID, err)
	}

	var expiredAt time.Time
	if expiresIn != nil {
		expiredAt = time.Now().UTC().Add(time.Duration(*expiresIn) * time.Second)
	}
	// A refresh response that omits refresh_token (common for client-credentials
	// grants) keeps the previously stored refresh token rather than wiping it.
	if refreshToken == "" {
		refreshToken = secrets.RefreshToken
	}
	return credentialmodels.StoreSysCredentialToken(ctx, credentialID, accessToken, refreshToken, expiredAt)
}

// callAuthEndpoint dispatches to the request shape for the credential's type
// and parses a best-effort token response.
func callAuthEndpoint(ctx context.Context, secrets *credentialmodels.SysCredentialSecrets) (accessToken, refreshToken string, expiresIn *int64, err error) {
	if secrets.AuthURL == "" {
		return "", "", nil, fmt.Errorf("auth_url is not configured")
	}

	var contentType string
	var payload []byte

	switch secrets.CredentialType {
	case credentialmodels.CredentialTypeRBAC:
		contentType = "application/json"
		payload, err = json.Marshal(map[string]string{"username": secrets.UserName, "password": secrets.Password})
		if err != nil {
			return "", "", nil, fmt.Errorf("encode auth request: %w", err)
		}
	case credentialmodels.CredentialTypeOIDCClient:
		contentType = "application/x-www-form-urlencoded"
		payload = []byte(oidcForm(map[string]string{
			"grant_type":    "client_credentials",
			"client_id":     secrets.ClientID,
			"client_secret": secrets.ClientSecret,
			"scope":         secrets.Scope,
		}).Encode())
	case credentialmodels.CredentialTypeOIDCPassword:
		contentType = "application/x-www-form-urlencoded"
		payload = []byte(oidcForm(map[string]string{
			"grant_type":    "password",
			"username":      secrets.UserName,
			"password":      secrets.Password,
			"client_id":     secrets.ClientID,
			"client_secret": secrets.ClientSecret,
			"scope":         secrets.Scope,
		}).Encode())
	default:
		return "", "", nil, fmt.Errorf("unsupported credential_type %q", secrets.CredentialType)
	}

	status, raw, err := tokenFetcher.PostAuth(ctx, secrets.AuthURL, contentType, payload)
	if err != nil {
		return "", "", nil, fmt.Errorf("call auth endpoint: %w", err)
	}
	if status < 200 || status >= 300 {
		return "", "", nil, fmt.Errorf("auth endpoint returned status %d", status)
	}

	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", "", nil, fmt.Errorf("parse auth response: %w", err)
	}
	return parseTokenResponse(parsed)
}

// oidcForm builds an application/x-www-form-urlencoded body, dropping empty values so an
// unused optional field (e.g. scope) is not sent as a literal empty string.
func oidcForm(fields map[string]string) url.Values {
	form := url.Values{}
	for key, value := range fields {
		if value != "" {
			form.Set(key, value)
		}
	}
	return form
}

// parseTokenResponse accepts common OAuth2 field names plus a couple of
// RBAC-style aliases, since third-party RBAC login endpoints vary in shape.
func parseTokenResponse(body map[string]any) (accessToken, refreshToken string, expiresIn *int64, err error) {
	accessToken = firstStringField(body, "access_token", "accessToken", "token")
	if accessToken == "" {
		return "", "", nil, fmt.Errorf("auth response missing access_token")
	}
	refreshToken = firstStringField(body, "refresh_token", "refreshToken")

	if raw, ok := firstField(body, "expires_in", "expiresIn", "expired_in"); ok {
		if seconds, ok := toInt64(raw); ok {
			expiresIn = &seconds
		}
	}
	return accessToken, refreshToken, expiresIn, nil
}

func firstField(body map[string]any, keys ...string) (any, bool) {
	for _, key := range keys {
		if value, ok := body[key]; ok {
			return value, true
		}
	}
	return nil, false
}

func firstStringField(body map[string]any, keys ...string) string {
	value, ok := firstField(body, keys...)
	if !ok {
		return ""
	}
	if s, ok := value.(string); ok {
		return s
	}
	return ""
}

func toInt64(raw any) (int64, bool) {
	switch typed := raw.(type) {
	case float64:
		return int64(typed), true
	case int64:
		return typed, true
	case int:
		return int64(typed), true
	case string:
		value, err := strconv.ParseInt(typed, 10, 64)
		return value, err == nil
	default:
		return 0, false
	}
}
