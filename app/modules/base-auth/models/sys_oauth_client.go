package models

// File overview:
// - Responsibility: defines `sys.oauth_client` — a registered third-party/OAuth caller
//   authenticated via `grant_type=client_credentials`. This is the direct, spec-compliant
//   successor to the static `X-API-Key` service credential: each automation caller gets its own
//   revocable secret instead of one shared key for all of them (RBAC/OAuth backend plan, §3).
// - Key symbols: `SysOauthClientRecord`, `SysOauthClientModel`, `LookupSysOauthClientByID`,
//   `HashClientSecret`, `VerifyClientSecret`.
// - Pattern: the client secret is bcrypt-hashed at rest and never echoed back after creation,
//   mirroring `base_users.mcp_api_key`'s one-time-reveal masking convention.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"

	"database/sql"
	"errors"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
	"github.com/uptrace/bun"
	"golang.org/x/crypto/bcrypt"
)

// SysOauthClientRecord stores one registered automation/service caller.
type SysOauthClientRecord struct {
	orm.BaseModel
	ClientID         string `bun:"client_id" json:"client_id" meta:"index,unique,label:Client ID,help:Stores the public identifier this caller presents at the token endpoint."`
	ClientSecretHash string `bun:"client_secret_hash" json:"client_secret_hash" meta:"secret,label:Client Secret Hash,help:Stores the bcrypt hash of the client secret. Never returned by the API after creation."`
	ServiceName      string `bun:"service_name" json:"service_name" meta:"index,label:Service Name,help:Stores a human-readable name for the automation/service this credential belongs to."`
	// RoleID carries kind:many2one so master-data seed files can reference a
	// role by external id (e.g. "base.group_system") the same way base.users'
	// group_id does, instead of needing a hardcoded numeric id. RPC callers can
	// still pass a raw numeric role_id directly — this metadata only adds
	// symbolic-id resolution, it does not change the underlying column.
	RoleID       int64 `bun:"role_id" json:"role_id" meta:"kind:many2one,model:tbl.groups,label:Role,help:Stores the tbl_groups id the issued token is bound to. Ignored when IsSuperadmin is true."`
	IsSuperadmin bool  `bun:"is_superadmin" json:"is_superadmin" meta:"label:Is Superadmin,help:Stores whether tokens issued to this client bypass RBAC checks entirely, same trust level as the legacy static API key."`
	// RedirectURIs is the exact-match allow-list of callback URLs this client
	// may be redirected to after a successful GET/POST /oauth/authorize login.
	// Required only for grant_type=authorization_code; client_credentials-only
	// callers can leave this empty. Exact match (not prefix) is enforced by
	// service.ValidateAuthorizeClient — the classic Authorization Code flow
	// open-redirect vulnerability lives in loosening this check.
	RedirectURIs []string `bun:"redirect_uris,type:jsonb,nullzero" json:"redirect_uris" meta:"kind:json,label:Redirect URIs,help:Exact-match allow-list of callback URLs this client may redirect to after browser-based authorization. Required to use grant_type=authorization_code."`
	// AllowedGrants limits the client-bound grants this client may use (plan T36). Empty keeps
	// the behavior clients had before the field existed: every grant below is allowed.
	AllowedGrants []string `bun:"allowed_grants,type:jsonb,nullzero" json:"allowed_grants" meta:"kind:json,label:Allowed Grants,help:Grants this client may use: authorization_code, implicit, client_credentials. Empty allows all of them."`
	// IsPublic marks a client that cannot keep its secret (SPA, mobile app, CLI). Its
	// authorization_code requests must use S256 PKCE; client_credentials and implicit are refused.
	IsPublic bool `bun:"is_public" json:"is_public" meta:"label:Public Client,help:Set for a client that cannot keep its secret (SPA/mobile/CLI): authorization_code then requires S256 PKCE and client_credentials/implicit are refused."`
	// CompanyID scopes which company's operators can see/manage this client
	// registration row; it does not affect the issued token's own runtime
	// authority, which is governed entirely by RoleID/IsSuperadmin.
	CompanyID *int64 `bun:"company_id" json:"company_id" meta:"kind:many2one,model:tbl.company,ondelete:SET NULL,index,label:Company ID,help:Links this record to the related tbl.company record for company id."`
}

const SysOauthClientModelName = "sys.oauth_client"

var SysOauthClientModel = orm.Define[SysOauthClientRecord](&Models, 10, orm.ModelSpec{
	Name:        SysOauthClientModelName,
	Label:       "OAuth Client",
	Description: "Registered service/automation caller authenticated via grant_type=client_credentials",
	RuntimeMethods: []orm.RuntimeMethod{
		{Name: "create", Public: true, ModelOnly: true, Call: createSysOauthClient},
		{Name: "write", Public: true, UsesPathID: true, Call: writeSysOauthClient},
		{Name: "unlink", Public: true, UsesPathID: true, Call: unlinkSysOauthClient},
		{Name: "generate_client_secret", Public: true, UsesPathID: true, Call: generateClientSecretSysOauthClient},
	},
})

// GenerateClientSecret returns a high-entropy random secret suitable for a
// client_credentials caller (32 random bytes, hex-encoded).
func GenerateClientSecret() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("sys.oauth_client generate secret: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

// HashClientSecret bcrypt-hashes a plaintext client secret for storage.
func HashClientSecret(plaintext string) (string, error) {
	hashed, err := bcrypt.GenerateFromPassword([]byte(plaintext), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("sys.oauth_client hash secret: %w", err)
	}
	return string(hashed), nil
}

// VerifyClientSecret reports whether plaintext matches the stored bcrypt hash.
func VerifyClientSecret(hash, plaintext string) bool {
	if strings.TrimSpace(hash) == "" || plaintext == "" {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plaintext)) == nil
}

// oauthClientRow is the minimal shape read back for token issuance, scanned
// directly against the tenant-scoped Bun DB (mirrors internal/orm/runtime_rbac.go's
// rbacUserRow pattern rather than going through the metadata-driven handler,
// since this lookup happens before any RBAC identity exists).
type oauthClientRow struct {
	ID               int64    `bun:"id"`
	ClientID         string   `bun:"client_id"`
	ClientSecretHash string   `bun:"client_secret_hash"`
	ServiceName      string   `bun:"service_name"`
	RoleID           int64    `bun:"role_id"`
	IsSuperadmin     bool     `bun:"is_superadmin"`
	Active           bool     `bun:"active"`
	RedirectURIs     []string `bun:"redirect_uris,type:jsonb"`
	AllowedGrants    []string `bun:"allowed_grants,type:jsonb"`
	IsPublic         bool     `bun:"is_public"`
}

// OAuth grants a client registration can be limited to (sys.oauth_client.allowed_grants).
const (
	OAuthGrantAuthorizationCode = "authorization_code"
	OAuthGrantImplicit          = "implicit"
	OAuthGrantClientCredentials = "client_credentials"
)

var oauthClientGrants = map[string]bool{
	OAuthGrantAuthorizationCode: true,
	OAuthGrantImplicit:          true,
	OAuthGrantClientCredentials: true,
}

// AllowsGrant reports whether this client may use grant. A public client never gets the
// grants that need a kept secret or put a token in the browser (client_credentials, implicit);
// otherwise an empty allowed_grants list allows every grant.
func (r *oauthClientRow) AllowsGrant(grant string) bool {
	if r == nil {
		return false
	}
	if r.IsPublic && (grant == OAuthGrantClientCredentials || grant == OAuthGrantImplicit) {
		return false
	}
	if len(r.AllowedGrants) == 0 {
		return true
	}
	for _, allowed := range r.AllowedGrants {
		if allowed == grant {
			return true
		}
	}
	return false
}

// RequiresS256PKCE reports whether this client's authorization_code requests need S256 PKCE.
func (r *oauthClientRow) RequiresS256PKCE() bool {
	return r != nil && r.IsPublic
}

// normalizeOAuthClientGrants validates allowed_grants on create/write: a list of known grant
// names (also accepted as a comma/space separated string), trimmed and de-duplicated.
func normalizeOAuthClientGrants(values orm.RecordMap) error {
	raw, present := values["allowed_grants"]
	if !present {
		return nil
	}
	var items []string
	switch typed := raw.(type) {
	case nil, bool:
	case string:
		items = strings.FieldsFunc(typed, func(r rune) bool { return r == ',' || r == ' ' })
	case []string:
		items = typed
	case []any:
		for _, item := range typed {
			text, ok := item.(string)
			if !ok {
				return orm.InvalidParamsf("sys.oauth_client allowed_grants must be a list of grant names")
			}
			items = append(items, text)
		}
	default:
		return orm.InvalidParamsf("sys.oauth_client allowed_grants must be a list of grant names")
	}
	grants := make([]string, 0, len(items))
	seen := map[string]bool{}
	for _, item := range items {
		grant := strings.ToLower(strings.TrimSpace(item))
		if grant == "" || seen[grant] {
			continue
		}
		if !oauthClientGrants[grant] {
			return orm.InvalidParamsf("sys.oauth_client allowed_grants: unknown grant %q (use authorization_code, implicit, client_credentials)", item)
		}
		seen[grant] = true
		grants = append(grants, grant)
	}
	values["allowed_grants"] = grants
	return nil
}

// oauthClientRowSelect builds the shared column list for oauthClientRow scans.
func oauthClientRowSelect(db *bun.DB) *bun.SelectQuery {
	return db.NewSelect().
		TableExpr("sys_oauth_client AS c").
		ColumnExpr("c.id").
		ColumnExpr("c.client_id").
		ColumnExpr("c.client_secret_hash").
		ColumnExpr("c.service_name").
		ColumnExpr("c.role_id").
		ColumnExpr("c.is_superadmin").
		ColumnExpr("c.active").
		ColumnExpr("COALESCE(c.redirect_uris, '[]'::jsonb) AS redirect_uris").
		ColumnExpr("COALESCE(c.allowed_grants, '[]'::jsonb) AS allowed_grants").
		ColumnExpr("COALESCE(c.is_public, false) AS is_public")
}

// LookupSysOauthClientByID returns the active oauth client row for clientID, or nil if not found.
func LookupSysOauthClientByID(ctx context.Context, db *bun.DB, clientID string) (*oauthClientRow, error) {
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		return nil, orm.InvalidParamsf("sys.oauth_client client_id is required")
	}
	var row oauthClientRow
	err := oauthClientRowSelect(db).Where("c.client_id = ?", clientID).Limit(1).Scan(ctx, &row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	if !row.Active {
		return nil, nil
	}
	return &row, nil
}

func createSysOauthClient(ctx context.Context, model *orm.ModelMetadata, _ []string, args []any, kwargs map[string]any) (any, error) {
	valsList, err := recordMapListFromArgs(args, kwargs)
	if err != nil {
		log.Errorf("sys.oauth_client create: %v", err)
		return nil, fmt.Errorf("sys.oauth_client create: %w", err)
	}
	results := make([]orm.RecordMap, 0, len(valsList))
	for _, values := range valsList {
		normalized := cloneRecordMap(values)
		if strings.TrimSpace(stringValue(normalized["client_id"])) == "" {
			log.Errorf("sys.oauth_client client_id is required")
			return nil, orm.InvalidParamsf("sys.oauth_client client_id is required")
		}
		plaintext := strings.TrimSpace(stringValue(normalized["client_secret"]))
		if plaintext == "" {
			plaintext, err = GenerateClientSecret()
			if err != nil {
				return nil, err
			}
		}
		hash, err := HashClientSecret(plaintext)
		if err != nil {
			return nil, err
		}
		delete(normalized, "client_secret")
		normalized["client_secret_hash"] = hash
		if err := normalizeOAuthClientGrants(normalized); err != nil {
			return nil, err
		}
		// CreateRecord pins the company and keeps the computed-field and company guards (plan T87).
		created, err := orm.CreateRecord(ctx, orm.RuntimeModelRef(SysOauthClientModelName, model), normalized)
		if err != nil {
			log.Errorf("sys.oauth_client create: %v", err)
			return nil, fmt.Errorf("sys.oauth_client create: %w", err)
		}
		// One-time reveal: the plaintext secret is only ever visible in this
		// create response, same convention as action_generate_mcp_api_key.
		delete(created, "client_secret_hash")
		created["client_secret"] = plaintext
		results = append(results, created)
	}
	if len(results) == 1 {
		return results[0], nil
	}
	return results, nil
}

func writeSysOauthClient(ctx context.Context, model *orm.ModelMetadata, ids []string, args []any, kwargs map[string]any) (any, error) {
	if len(ids) == 0 {
		return false, orm.InvalidParamsf("sys.oauth_client write requires ids")
	}
	values, err := recordMapFromArgs(args, kwargs, "values")
	if err != nil {
		return false, fmt.Errorf("sys.oauth_client write: %w", err)
	}
	if values == nil {
		values = orm.RecordMap{}
	}
	normalized := cloneRecordMap(values)
	if plaintext := strings.TrimSpace(stringValue(normalized["client_secret"])); plaintext != "" {
		hash, err := HashClientSecret(plaintext)
		if err != nil {
			return false, err
		}
		delete(normalized, "client_secret")
		normalized["client_secret_hash"] = hash
	}
	if err := normalizeOAuthClientGrants(normalized); err != nil {
		return false, err
	}
	updated, err := orm.WriteRecords(ctx, orm.RuntimeModelRef(SysOauthClientModelName, model), ids, normalized)
	if err != nil {
		return false, fmt.Errorf("sys.oauth_client write: %w", err)
	}
	return updated, nil
}

func unlinkSysOauthClient(ctx context.Context, model *orm.ModelMetadata, ids []string, _ []any, _ map[string]any) (any, error) {
	if len(ids) == 0 {
		return false, orm.InvalidParamsf("sys.oauth_client unlink requires ids")
	}
	deleted, err := orm.RuntimeRepositoryUnlink(ctx, orm.RuntimeModelRef(SysOauthClientModelName, model), ids)
	if err != nil {
		return false, fmt.Errorf("sys.oauth_client unlink: %w", err)
	}
	return deleted, nil
}

func generateClientSecretSysOauthClient(ctx context.Context, model *orm.ModelMetadata, ids []string, _ []any, _ map[string]any) (any, error) {
	if len(ids) == 0 {
		return nil, orm.InvalidParamsf("sys.oauth_client.generate_client_secret requires ids")
	}
	plaintext, err := GenerateClientSecret()
	if err != nil {
		return nil, err
	}
	hash, err := HashClientSecret(plaintext)
	if err != nil {
		return nil, err
	}
	// Rotate only the stored hash and reveal the plaintext in this response;
	// callers must copy it immediately because no later read path exposes it.
	if _, err := orm.WriteRecords(ctx, orm.RuntimeModelRef(SysOauthClientModelName, model), ids[:1], orm.RecordMap{"client_secret_hash": hash}); err != nil {
		return nil, fmt.Errorf("sys.oauth_client.generate_client_secret: %w", err)
	}
	rows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(SysOauthClientModelName, model), ids[:1], []string{"client_id"})
	if err != nil {
		return nil, fmt.Errorf("sys.oauth_client.generate_client_secret: %w", err)
	}
	clientID := ""
	if len(rows) > 0 {
		clientID = stringValue(rows[0]["client_id"])
	}
	return orm.RecordMap{
		"oauth_client_id":     clientID,
		"oauth_client_secret": plaintext,
	}, nil
}
