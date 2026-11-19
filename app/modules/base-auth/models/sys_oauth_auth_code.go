package models

// File overview:
// - Responsibility: defines `sys.oauth_auth_code` — a hashed-at-rest, single-use authorization
//   code backing the builtin OAuth 2.0 Authorization Code grant (RFC 6749 §4.1), issued by
//   GET/POST /oauth/authorize after a browser login and redeemed once by
//   POST /oauth/token grant_type=authorization_code. See
//   .agents/planning/2026-08-09-oauth-authorization-code-flow-plan.md.
// - Key symbols: `SysOauthAuthCodeRecord`, `InsertSysOauthAuthCode`, `ConsumeSysOauthAuthCode`.
// - Pattern: mirrors sys_oauth_refresh_token.go's hashed-opaque-token convention (SHA-256, not
//   bcrypt — this is a high-entropy random value, not a low-entropy human-chosen secret), minus
//   the family/rotation machinery since a code is single-use, never rotated.

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"git.thebase.vn/base_hub/internal/orm"
	"github.com/uptrace/bun"
)

// SysOauthAuthCodeRecord stores one issued authorization code, hashed at rest.
// Carries its own explicit bun.BaseModel table tag (same reason
// sys_oauth_refresh_token.go documents) because InsertSysOauthAuthCode/
// ConsumeSysOauthAuthCode issue raw *bun.DB queries, not just metadata-driven
// runtime handler calls.
type SysOauthAuthCodeRecord struct {
	bun.BaseModel `bun:"table:sys_oauth_auth_code,alias:sys_oauth_auth_code"`
	orm.MetadataBaseModel
	ClientID    string    `bun:"client_id" json:"client_id" meta:"index,label:Client ID"`
	RedirectURI string    `bun:"redirect_uri" json:"redirect_uri" meta:"label:Redirect URI"`
	UserID      int64     `bun:"user_id" json:"user_id" meta:"index,label:User"`
	DBName      string    `bun:"db_name" json:"db_name" meta:"index,label:Database Name"`
	CodeHash    string    `bun:"code_hash" json:"code_hash" meta:"index,unique,label:Code Hash"`
	ExpiresAt   time.Time `bun:"expires_at" json:"expires_at" meta:"index,label:Expires At"`
	Used        bool      `bun:"used" json:"used" meta:"index,label:Used"`
	// CodeChallenge/CodeChallengeMethod carry RFC 7636 PKCE parameters bound at
	// issuance, when the authorize request included them. Both are empty for
	// a caller that skips PKCE (this flow treats PKCE as optional-if-present,
	// not mandatory — see authorize_code.go's verifyPKCE).
	CodeChallenge       string `bun:"code_challenge,nullzero" json:"code_challenge" meta:"label:Code Challenge"`
	CodeChallengeMethod string `bun:"code_challenge_method,nullzero" json:"code_challenge_method" meta:"label:Code Challenge Method"`
	// Scope is round-tripped verbatim from the authorize request into the
	// token response — no scoped-permission model exists yet, so it's not
	// enforced, only echoed back per RFC 6749 §5.1.
	Scope string `bun:"scope,nullzero" json:"scope" meta:"label:Scope"`
}

const SysOauthAuthCodeModelName = "sys.oauth_auth_code"

var SysOauthAuthCodeModel = orm.Define[SysOauthAuthCodeRecord](&Models, 10, orm.ModelSpec{
	Name:        SysOauthAuthCodeModelName,
	Label:       "OAuth Authorization Code",
	Description: "Hashed, single-use authorization code issued by GET/POST /oauth/authorize and redeemed by grant_type=authorization_code",
})

// hashAuthCode returns the SHA-256 hex digest stored for an authorization
// code. Kept as its own copy rather than reusing HashRefreshToken (same
// algorithm) so each token type's hashing call site stays self-documenting.
func hashAuthCode(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}

// InsertSysOauthAuthCode records a newly issued authorization code.
func InsertSysOauthAuthCode(ctx context.Context, db *bun.DB, clientID, redirectURI string, userID int64, dbName, plaintext string, expiresAt time.Time, codeChallenge, codeChallengeMethod, scope string) error {
	row := &SysOauthAuthCodeRecord{
		ClientID:            clientID,
		RedirectURI:         redirectURI,
		UserID:              userID,
		DBName:              dbName,
		CodeHash:            hashAuthCode(plaintext),
		ExpiresAt:           expiresAt,
		Used:                false,
		CodeChallenge:       codeChallenge,
		CodeChallengeMethod: codeChallengeMethod,
		Scope:               scope,
	}
	_, err := db.NewInsert().Model(row).Exec(ctx)
	if err != nil {
		return fmt.Errorf("sys.oauth_auth_code insert: %w", err)
	}
	return nil
}

// authCodeRow is the minimal shape read back at exchange time.
type authCodeRow struct {
	ClientID            string `bun:"client_id"`
	RedirectURI         string `bun:"redirect_uri"`
	UserID              int64  `bun:"user_id"`
	DBName              string `bun:"db_name"`
	CodeChallenge       string `bun:"code_challenge"`
	CodeChallengeMethod string `bun:"code_challenge_method"`
	Scope               string `bun:"scope"`
}

// ConsumeSysOauthAuthCode atomically marks one unused, unexpired code as used
// and returns the row it was bound to. The UPDATE ... WHERE used = FALSE ...
// RETURNING is a single statement specifically so two concurrent exchange
// attempts for the same code can never both succeed (sql.ErrNoRows means
// "not found, already used, or expired" — callers must not distinguish these
// to avoid leaking which case applies to a caller with no legitimate way to
// know).
func ConsumeSysOauthAuthCode(ctx context.Context, db *bun.DB, plaintext string) (*authCodeRow, error) {
	var row authCodeRow
	err := db.NewUpdate().
		Table("sys_oauth_auth_code").
		Set("used = TRUE").
		Where("code_hash = ?", hashAuthCode(plaintext)).
		Where("used = FALSE").
		Where("expires_at > ?", time.Now()).
		Returning("client_id, redirect_uri, user_id, db_name, COALESCE(code_challenge, '') AS code_challenge, COALESCE(code_challenge_method, '') AS code_challenge_method, COALESCE(scope, '') AS scope").
		Scan(ctx, &row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, orm.InvalidParamsf("invalid or expired authorization code")
		}
		return nil, err
	}
	return &row, nil
}
