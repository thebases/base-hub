package models

// File overview:
// - Responsibility: defines the `credential` module model contract for `sys.credential` — a named
//   credential this system uses to authenticate against another system (RBAC username/password,
//   OIDC client-credentials grant, OIDC password grant, or a static API key). Secret fields are
//   AES-GCM encrypted at rest under a key derived from app.encryptKey (`secretbox`) and stripped
//   from every runtime read and write response; only the auth-flow code in the sibling `service`
//   package (via the lookup/store hooks below) ever sees plaintext.
// - Key symbols: `SysCredentialRecord`, `SysCredentialModelSpec`, `LookupSysCredentialSecrets`,
//   `StoreSysCredentialToken`.
// - Pattern: Leans on metadata-driven registration so storage mapping, runtime methods, and secret
//   handling stay centralized instead of being scattered across handlers.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
	"git.thebase.vn/base_hub/internal/utils/anyconv"
	"git.thebase.vn/base_hub/modules/base-credential/secretbox"
)

// Credential type selection values, shared by service/auth_client.go dispatch.
const (
	CredentialTypeRBAC         = "rbac"
	CredentialTypeOIDCClient   = "oidc_client"
	CredentialTypeOIDCPassword = "oidc_password"
	CredentialTypeAPIKey       = "apikey"
)

// DefaultRefreshBeforeSeconds is how far ahead of ExpiredTime the refresh cron
// re-authenticates by default, giving in-flight requests a safety margin
// instead of racing a token that expires mid-call.
const DefaultRefreshBeforeSeconds int64 = 300

// secretFields lists every column whose value is stored encrypted and must
// never be echoed back in a runtime create/write response.
var secretFields = []string{"password", "client_secret", "access_token", "refresh_token"}

// bookkeepingFields are owned by the login/refresh flow (StoreSysCredentialToken,
// StoreSysCredentialLoginFailure) and stripped from client create/write so a
// caller cannot clear a failing credential's backoff or forge its login state.
var bookkeepingFields = []string{"next_attempt_at"}

// Refresh backoff after a failed login: the first retry waits loginRetryBaseDelay,
// each further consecutive failure doubles the previous delay up to loginRetryMaxDelay,
// so permanently broken credentials stop occupying the cron's per-tick claim slots.
const (
	loginRetryBaseDelay = time.Minute
	loginRetryMaxDelay  = 6 * time.Hour
)

// SysCredentialRecord stores one named credential this system uses to call
// another system. Only the fields relevant to CredentialType are expected to
// be populated; unused fields stay empty.
type SysCredentialRecord struct {
	orm.BaseModel
	Name                 string    `bun:"name" json:"name" meta:"index,label:Name,help:Stores the unique reference name used to look this credential up (e.g. in workflow action params as @cred:<name>)."`
	CredentialType       string    `bun:"credential_type" json:"credential_type" meta:"index,label:Credential Type,help:Stores which auth flow this credential uses." selection:"(rbac,RBAC),(oidc_client,OIDC Client Credentials),(oidc_password,OIDC Password),(apikey,API Key)"`
	Description          string    `bun:"description" json:"description" meta:"label:Description,help:Stores human-readable notes about what system this credential connects to."`
	AuthURL              string    `bun:"auth_url" json:"auth_url" meta:"label:Auth URL,help:Stores the endpoint called to obtain or refresh a token. Required for rbac, oidc_client, and oidc_password."`
	UserName             string    `bun:"user_name" json:"user_name" meta:"label:Username,help:Stores the login username. Used by rbac and oidc_password."`
	Password             string    `bun:"password" json:"password" meta:"secret,label:Password,help:Stores the login password, encrypted at rest and never returned by the API. Used by rbac and oidc_password."`
	ClientID             string    `bun:"client_id" json:"client_id" meta:"label:Client ID,help:Stores the OIDC client identifier. Used by oidc_client and oidc_password."`
	ClientSecret         string    `bun:"client_secret" json:"client_secret" meta:"secret,label:Client Secret,help:Stores the OIDC client secret, encrypted at rest and never returned by the API. Used by oidc_client and oidc_password."`
	Scope                string    `bun:"scope" json:"scope" meta:"label:Scope,help:Stores the optional OAuth/OIDC scope requested during login."`
	AccessToken          string    `bun:"access_token" json:"access_token" meta:"secret,label:Access Token / API Key,help:Stores the current access token (rbac, oidc_client, oidc_password) or the static API key (apikey), encrypted at rest and never returned by the API."`
	RefreshToken         string    `bun:"refresh_token" json:"refresh_token" meta:"secret,label:Refresh Token,help:Stores the refresh token returned by the last login, encrypted at rest and never returned by the API. Empty when the auth flow does not issue one."`
	ExpiredTime          time.Time `bun:"expired_time,nullzero" json:"expired_time" meta:"label:Expired Time,help:Stores when AccessToken expires. Null means the token does not expire (typical for apikey)."`
	RefreshBeforeSeconds int64     `bun:"refresh_before_seconds" json:"refresh_before_seconds" meta:"label:Refresh Before Seconds,help:Stores how many seconds ahead of ExpiredTime the cron job re-authenticates. Default 300."`
	LastLoginStatus      string    `bun:"last_login_status,nullzero" json:"last_login_status" meta:"label:Last Login Status,help:Stores whether the most recent login/refresh attempt succeeded." selection:"(ok,Ok),(error,Error)"`
	LastLoginError       string    `bun:"last_login_error,nullzero" json:"last_login_error" meta:"label:Last Login Error,help:Stores the error message from the most recent failed login/refresh attempt."`
	LastLoginAt          time.Time `bun:"last_login_at,nullzero" json:"last_login_at" meta:"label:Last Login At,help:Stores the timestamp of the most recent login/refresh attempt, successful or not."`
	NextAttemptAt        time.Time `bun:"next_attempt_at,nullzero" json:"next_attempt_at" meta:"readonly,label:Next Attempt At,help:Stores the earliest time the refresh cron retries this credential after a failed login. Null when the last attempt succeeded."`
	CompanyID            *int64    `bun:"company_id" json:"company_id" meta:"kind:many2one,model:tbl.company,ondelete:SET NULL,index,label:Company ID,help:Links this record to the related tbl.company record for company id."`
}

const SysCredentialModelName = "sys.credential"

// SysCredentialModelSpec stays exported: the init below and tests read its storage name and methods.
var SysCredentialModelSpec = orm.ModelSpec{
	Name:        SysCredentialModelName,
	Label:       "Credential",
	Description: "Named credential used to authenticate against another system, with cron-driven token refresh",
	// read, search_read, search_fetch, read_group, copy_data and copy never return secretFields.
	RuntimeMethods: append([]orm.RuntimeMethod{
		{Name: "create", Public: true, ModelOnly: true, Call: createSysCredential},
		{Name: "write", Public: true, UsesPathID: true, Call: writeSysCredential},
		{Name: "unlink", Public: true, UsesPathID: true, Call: unlinkSysCredential},
		{Name: "login", Public: true, UsesPathID: true, Call: loginSysCredential},
	}, secretbox.RedactedReadMethods(secretFields...)...),
}

var SysCredentialModel = orm.Define[SysCredentialRecord](&Models, 10, SysCredentialModelSpec)

// SysCredentialEncryptHook and SysCredentialDecryptHook encrypt secret fields under a key derived
// from app.encryptKey (secretbox). They are variables only so tests can swap them.
var (
	SysCredentialEncryptHook func(plaintext string) (string, error)  = sealSysCredentialSecret
	SysCredentialDecryptHook func(ciphertext string) (string, error) = openSysCredentialSecret
)

func init() {
	// Re-encrypt legacy zero-key values on every migrated database, and take part in rotate-encrypt-key.
	secretbox.RegisterSecretTable("base-credential", SysCredentialModelSpec.ResolveStorageName(), sysCredentialSecretLabel, secretFields...)
}

func normalizeSysCredentialValues(values orm.RecordMap, creating bool) (orm.RecordMap, error) {
	normalized := cloneRecordMap(values)
	for _, field := range bookkeepingFields {
		delete(normalized, field)
	}

	for _, field := range []string{"name", "credential_type", "description", "auth_url", "user_name", "client_id", "scope"} {
		if value, ok := stringFromAny(normalized[field]); ok {
			normalized[field] = strings.TrimSpace(value)
		}
	}

	if creating {
		name := strings.TrimSpace(stringValue(normalized["name"]))
		if name == "" {
			log.Errorf("sys.credential name is required")
			return nil, orm.InvalidParamsf("sys.credential name is required")
		}
		credType := strings.TrimSpace(stringValue(normalized["credential_type"]))
		if !isKnownCredentialType(credType) {
			log.Errorf("sys.credential credential_type %q is not supported", credType)
			return nil, orm.InvalidParamsf("sys.credential credential_type %q is not supported", credType)
		}
		if credType != CredentialTypeAPIKey && strings.TrimSpace(stringValue(normalized["auth_url"])) == "" {
			log.Errorf("sys.credential auth_url is required for credential_type %q", credType)
			return nil, orm.InvalidParamsf("sys.credential auth_url is required for credential_type %q", credType)
		}
		if _, ok := normalized["refresh_before_seconds"]; !ok {
			normalized["refresh_before_seconds"] = DefaultRefreshBeforeSeconds
		}
	}

	if raw, ok := normalized["refresh_before_seconds"]; ok {
		seconds, parsed := int64FromAny(raw)
		if !parsed || seconds < 0 {
			log.Errorf("sys.credential refresh_before_seconds must be a non-negative integer")
			return nil, orm.InvalidParamsf("sys.credential refresh_before_seconds must be a non-negative integer")
		}
		normalized["refresh_before_seconds"] = seconds
	}

	// Encrypt every secret field supplied on this call in place. Plaintext is
	// never persisted; only ciphertext reaches the storage layer.
	for _, field := range secretFields {
		raw, exists := normalized[field]
		if !exists {
			continue
		}
		plaintext := strings.TrimSpace(stringValue(raw))
		if plaintext == "" {
			normalized[field] = ""
			continue
		}
		if SysCredentialEncryptHook == nil {
			log.Errorf("sys.credential encrypt hook is not initialized")
			return nil, fmt.Errorf("sys.credential encrypt hook is not initialized")
		}
		encrypted, err := SysCredentialEncryptHook(plaintext)
		if err != nil {
			log.Errorf("sys.credential encrypt %s: %v", field, err)
			return nil, fmt.Errorf("sys.credential encrypt %s: %w", field, err)
		}
		normalized[field] = encrypted
	}

	return normalized, nil
}

func isKnownCredentialType(credType string) bool {
	switch credType {
	case CredentialTypeRBAC, CredentialTypeOIDCClient, CredentialTypeOIDCPassword, CredentialTypeAPIKey:
		return true
	default:
		return false
	}
}

// scrubSysCredentialSecrets deletes encrypted secret columns from a record
// map so runtime create/write responses never echo ciphertext to callers.
func scrubSysCredentialSecrets(record orm.RecordMap) orm.RecordMap {
	for _, field := range secretFields {
		delete(record, field)
	}
	return record
}

func createSysCredential(ctx context.Context, model *orm.ModelMetadata, _ []string, args []any, kwargs map[string]any) (any, error) {
	valsList, err := recordMapListFromArgs(args, kwargs)
	if err != nil {
		log.Errorf("sys.credential create: %v", err)
		return nil, fmt.Errorf("sys.credential create: %w", err)
	}
	results := make([]orm.RecordMap, 0, len(valsList))
	for _, values := range valsList {
		normalized, err := normalizeSysCredentialValues(values, true)
		if err != nil {
			return nil, err
		}
		// Secrets are already sealed above; CreateRecord adds the company pin and the computed-field
		// and company guards (plan T87) without touching ciphertext.
		created, err := orm.CreateRecord(ctx, orm.RuntimeModelRef(SysCredentialModelName, model), normalized)
		if err != nil {
			log.Errorf("sys.credential create: %v", err)
			return nil, fmt.Errorf("sys.credential create: %w", err)
		}
		results = append(results, scrubSysCredentialSecrets(created))
	}
	if len(results) == 1 {
		return results[0], nil
	}
	return results, nil
}

func writeSysCredential(ctx context.Context, model *orm.ModelMetadata, ids []string, args []any, kwargs map[string]any) (any, error) {
	if len(ids) == 0 {
		log.Errorf("sys.credential write requires ids")
		return false, orm.InvalidParamsf("sys.credential write requires ids")
	}
	values, err := recordMapFromArgs(args, kwargs, "values")
	if err != nil {
		log.Errorf("sys.credential write: %v", err)
		return false, fmt.Errorf("sys.credential write: %w", err)
	}
	if values == nil {
		values = orm.RecordMap{}
	}
	normalized, err := normalizeSysCredentialValues(values, false)
	if err != nil {
		return false, err
	}
	updated, err := orm.WriteRecords(ctx, orm.RuntimeModelRef(SysCredentialModelName, model), ids, normalized)
	if err != nil {
		log.Errorf("sys.credential write: %v", err)
		return false, fmt.Errorf("sys.credential write: %w", err)
	}
	return secretbox.Redact(updated, secretFields...), nil
}

func unlinkSysCredential(ctx context.Context, model *orm.ModelMetadata, ids []string, _ []any, _ map[string]any) (any, error) {
	if len(ids) == 0 {
		log.Errorf("sys.credential unlink requires ids")
		return false, orm.InvalidParamsf("sys.credential unlink requires ids")
	}
	deleted, err := orm.RuntimeRepositoryUnlink(ctx, orm.RuntimeModelRef(SysCredentialModelName, model), ids)
	if err != nil {
		log.Errorf("sys.credential unlink: %v", err)
		return false, fmt.Errorf("sys.credential unlink: %w", err)
	}
	return deleted, nil
}

// loginSysCredential is a manually-triggerable runtime action (e.g. from the
// form view) that forces an immediate (re-)authentication, using the same
// path the refresh cron uses. LoginHook is wired by service/runtime.go so
// this model package never imports net/http directly.
func loginSysCredential(ctx context.Context, _ *orm.ModelMetadata, ids []string, _ []any, _ map[string]any) (any, error) {
	if len(ids) == 0 {
		log.Errorf("sys.credential login requires ids")
		return false, orm.InvalidParamsf("sys.credential login requires ids")
	}
	if LoginHook == nil {
		log.Errorf("sys.credential login hook is not initialized")
		return false, fmt.Errorf("sys.credential login hook is not initialized")
	}
	credID, ok := int64FromAny(ids[0])
	if !ok {
		log.Errorf("sys.credential login: invalid id %v", ids[0])
		return false, orm.InvalidParamsf("sys.credential login: invalid id %v", ids[0])
	}
	if err := LoginHook(ctx, credID); err != nil {
		return false, err
	}
	return true, nil
}

// LoginHook is set by service/runtime.go to the real auth-flow implementation
// (service.LoginCredential) so this models package stays free of net/http
// and HTTP-client concerns.
var LoginHook func(ctx context.Context, credentialID int64) error

// SysCredentialSecrets is the decrypted view returned by LookupSysCredentialSecrets.
type SysCredentialSecrets struct {
	ID             int64
	CredentialType string
	AuthURL        string
	UserName       string
	Password       string
	ClientID       string
	ClientSecret   string
	Scope          string
	AccessToken    string
	RefreshToken   string
	ExpiredTime    time.Time
	// LastLoginAt/NextAttemptAt/LastLoginStatus feed the failure backoff (nextLoginAttemptAt).
	LastLoginAt     time.Time
	NextAttemptAt   time.Time
	LastLoginStatus string
}

// LookupSysCredentialSecrets fetches and decrypts one credential row by
// database id, for use by the auth-flow and refresh-cron code in the
// sibling `service` package.
func LookupSysCredentialSecrets(ctx context.Context, credentialID int64) (*SysCredentialSecrets, error) {
	rows, err := orm.RuntimeRepositorySearchRead(ctx, orm.RuntimeModelRef(SysCredentialModelName, nil), orm.RecordMap{
		"id":      credentialID,
		"__limit": 1,
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("sys.credential lookup %d: %w", credentialID, err)
	}
	if len(rows) == 0 {
		return nil, orm.NotFoundf("sys.credential %d not found", credentialID)
	}
	return decryptSysCredentialRow(rows[0])
}

// LookupSysCredentialSecretsByName resolves @cred:<name> references for the
// workflow module's credential resolver.
func LookupSysCredentialSecretsByName(ctx context.Context, name string) (*SysCredentialSecrets, error) {
	if strings.TrimSpace(name) == "" {
		return nil, orm.InvalidParamsf("sys.credential: name is required")
	}
	rows, err := orm.RuntimeRepositorySearchRead(ctx, orm.RuntimeModelRef(SysCredentialModelName, nil), orm.RecordMap{
		"name":    strings.TrimSpace(name),
		"__limit": 1,
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("sys.credential lookup %q: %w", name, err)
	}
	if len(rows) == 0 {
		return nil, orm.NotFoundf("sys.credential %q not found", name)
	}
	return decryptSysCredentialRow(rows[0])
}

func decryptSysCredentialRow(row orm.RecordMap) (*SysCredentialSecrets, error) {
	if SysCredentialDecryptHook == nil {
		return nil, fmt.Errorf("sys.credential decrypt hook is not initialized")
	}
	decrypt := func(cipher string) (string, error) {
		if strings.TrimSpace(cipher) == "" {
			return "", nil
		}
		return SysCredentialDecryptHook(cipher)
	}
	password, err := decrypt(stringValue(row["password"]))
	if err != nil {
		return nil, fmt.Errorf("sys.credential decrypt password: %w", err)
	}
	clientSecret, err := decrypt(stringValue(row["client_secret"]))
	if err != nil {
		return nil, fmt.Errorf("sys.credential decrypt client_secret: %w", err)
	}
	accessToken, err := decrypt(stringValue(row["access_token"]))
	if err != nil {
		return nil, fmt.Errorf("sys.credential decrypt access_token: %w", err)
	}
	refreshToken, err := decrypt(stringValue(row["refresh_token"]))
	if err != nil {
		return nil, fmt.Errorf("sys.credential decrypt refresh_token: %w", err)
	}
	id, _ := int64FromAny(row["id"])
	expiredTime, _ := anyconv.Time(row["expired_time"])
	lastLoginAt, _ := anyconv.Time(row["last_login_at"])
	nextAttemptAt, _ := anyconv.Time(row["next_attempt_at"])
	return &SysCredentialSecrets{
		ID:              id,
		CredentialType:  stringValue(row["credential_type"]),
		AuthURL:         stringValue(row["auth_url"]),
		UserName:        stringValue(row["user_name"]),
		Password:        password,
		ClientID:        stringValue(row["client_id"]),
		ClientSecret:    clientSecret,
		Scope:           stringValue(row["scope"]),
		AccessToken:     accessToken,
		RefreshToken:    refreshToken,
		ExpiredTime:     expiredTime,
		LastLoginAt:     lastLoginAt,
		NextAttemptAt:   nextAttemptAt,
		LastLoginStatus: stringValue(row["last_login_status"]),
	}, nil
}

// StoreSysCredentialToken persists a fresh access/refresh token pair after a
// successful login or refresh, encrypting both before write and recording
// success bookkeeping. expiredAt is the zero time when the provider reported no
// expiry: it is stored as NULL ("does not expire", skipped by the refresh cron)
// rather than keeping the previous, already-past expiry, which would make the
// cron re-login on every tick.
func StoreSysCredentialToken(ctx context.Context, credentialID int64, accessToken, refreshToken string, expiredAt time.Time) error {
	if SysCredentialEncryptHook == nil {
		return fmt.Errorf("sys.credential encrypt hook is not initialized")
	}
	encryptedAccess, err := encryptIfNotEmpty(accessToken)
	if err != nil {
		return fmt.Errorf("sys.credential encrypt access_token: %w", err)
	}
	encryptedRefresh, err := encryptIfNotEmpty(refreshToken)
	if err != nil {
		return fmt.Errorf("sys.credential encrypt refresh_token: %w", err)
	}
	values := orm.RecordMap{
		"access_token":      encryptedAccess,
		"refresh_token":     encryptedRefresh,
		"last_login_status": "ok",
		"last_login_error":  "",
		"last_login_at":     time.Now().UTC(),
		// Success ends any failure backoff.
		"next_attempt_at": nil,
		"expired_time":    nil,
	}
	if !expiredAt.IsZero() {
		values["expired_time"] = expiredAt
	}
	_, err = orm.RuntimeRepositoryWrite(ctx, orm.RuntimeModelRef(SysCredentialModelName, nil), []string{fmt.Sprint(credentialID)}, values)
	return err
}

// StoreSysCredentialLoginFailure records a failed login/refresh attempt so
// operators can see why a credential's token went stale without exposing any
// secret material in the error message. previous is the row as read before the
// attempt (nil when unknown); it drives the exponential retry backoff.
func StoreSysCredentialLoginFailure(ctx context.Context, credentialID int64, previous *SysCredentialSecrets, loginErr error) error {
	now := time.Now().UTC()
	values := orm.RecordMap{
		"last_login_status": "error",
		"last_login_error":  loginErr.Error(),
		"last_login_at":     now,
		"next_attempt_at":   nextLoginAttemptAt(previous, now),
	}
	_, err := orm.RuntimeRepositoryWrite(ctx, orm.RuntimeModelRef(SysCredentialModelName, nil), []string{fmt.Sprint(credentialID)}, values)
	return err
}

// nextLoginAttemptAt returns when the refresh cron may retry after a failure at now.
// The previous delay is recovered from the prior failure's own bookkeeping
// (next_attempt_at - last_login_at, both written by the same failure) instead of
// a separate counter column; a manual login that fails also extends the backoff.
func nextLoginAttemptAt(previous *SysCredentialSecrets, now time.Time) time.Time {
	delay := loginRetryBaseDelay
	if previous != nil && previous.LastLoginStatus == "error" && !previous.NextAttemptAt.IsZero() && !previous.LastLoginAt.IsZero() {
		if prior := previous.NextAttemptAt.Sub(previous.LastLoginAt); prior > 0 {
			delay = 2 * prior
		}
	}
	return now.Add(min(max(delay, loginRetryBaseDelay), loginRetryMaxDelay))
}

func encryptIfNotEmpty(plaintext string) (string, error) {
	if strings.TrimSpace(plaintext) == "" {
		return "", nil
	}
	return SysCredentialEncryptHook(plaintext)
}

// sysCredentialSecretLabel scopes the key derived from app.encryptKey to this model.
const sysCredentialSecretLabel = SysCredentialModelName

func sealSysCredentialSecret(plaintext string) (string, error) {
	return secretbox.Seal(secretbox.AppKey(), sysCredentialSecretLabel, plaintext)
}

func openSysCredentialSecret(ciphertext string) (string, error) {
	return secretbox.Open(secretbox.AppKey(), sysCredentialSecretLabel, ciphertext)
}
