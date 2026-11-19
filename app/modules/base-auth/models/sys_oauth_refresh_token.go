package models

// File overview:
// - Responsibility: defines `sys.oauth_refresh_token` — a hashed-at-rest, rotate-on-use refresh
//   token backing the builtin OAuth 2.0 token issuer (RBAC/OAuth backend plan, §3). Reuse of a
//   revoked token invalidates the whole family, the standard refresh-rotation breach-detection
//   pattern: `RevokeSysOauthRefreshTokenFamily` is called the moment reuse is detected.
// - Key symbols: `SysOauthRefreshTokenRecord`, `InsertSysOauthRefreshToken`, `ConsumeSysOauthRefreshToken`.

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

// SysOauthRefreshTokenRecord stores one issued refresh token, hashed at rest.
// The explicit bun.BaseModel table tag (mirroring internal/models/sys_config.go)
// is required here because, unlike sys.oauth_client, this model is also
// written/read via raw *bun.DB calls (InsertSysOauthRefreshToken,
// ConsumeSysOauthRefreshToken, ...) that need Bun's own table-name inference
// to resolve correctly, not just the metadata-driven runtime handler.
type SysOauthRefreshTokenRecord struct {
	bun.BaseModel `bun:"table:sys_oauth_refresh_token,alias:sys_oauth_refresh_token"`
	orm.MetadataBaseModel
	UserID    int64     `bun:"user_id" json:"user_id" meta:"index,label:User"`
	DBName    string    `bun:"db_name" json:"db_name" meta:"index,label:Database Name"`
	TokenHash string    `bun:"token_hash" json:"token_hash" meta:"secret,index,unique,label:Token Hash"`
	FamilyID  string    `bun:"family_id" json:"family_id" meta:"index,label:Family ID,help:Stores the id shared by every token in a rotation chain, so reuse of a revoked token can invalidate the whole family."`
	IssuedAt  time.Time `bun:"issued_at" json:"issued_at" meta:"label:Issued At"`
	ExpiresAt time.Time `bun:"expires_at" json:"expires_at" meta:"index,label:Expires At"`
	RevokedAt time.Time `bun:"revoked_at,nullzero" json:"revoked_at" meta:"label:Revoked At"`
	// SubjectKind records what UserID refers to. Only RefreshTokenSubjectUser
	// rows may be refreshed: rows written before this column existed are empty
	// and indistinguishable from the client_credentials rows that stored a
	// sys_oauth_client id in UserID, so Refresh must refuse them (SEC-T-3).
	SubjectKind string `bun:"subject_kind" json:"subject_kind" meta:"label:Subject Kind,help:Stores what user_id refers to. Only 'user' tokens can be refreshed; empty legacy rows are refused."`
}

// RefreshTokenSubjectUser marks a refresh token whose user_id is a base_users id.
const RefreshTokenSubjectUser = "user"

const SysOauthRefreshTokenModelName = "sys.oauth_refresh_token"

var SysOauthRefreshTokenModel = orm.Define[SysOauthRefreshTokenRecord](&Models, 10, orm.ModelSpec{
	Name:        SysOauthRefreshTokenModelName,
	Label:       "OAuth Refresh Token",
	Description: "Hashed, rotate-on-use refresh token issued by the builtin OAuth 2.0 token endpoint",
})

// HashRefreshToken returns the SHA-256 hex digest stored for a refresh token.
// A random, high-entropy opaque token needs only a fast digest (not bcrypt)
// since it is not a low-entropy human-chosen secret (RBAC/OAuth plan §3).
func HashRefreshToken(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}

type refreshTokenRow struct {
	ID        int64     `bun:"id"`
	UserID    int64     `bun:"user_id"`
	DBName    string    `bun:"db_name"`
	FamilyID  string    `bun:"family_id"`
	ExpiresAt time.Time `bun:"expires_at"`
	RevokedAt time.Time `bun:"revoked_at"`
	// SubjectKind is "" for legacy rows (see SysOauthRefreshTokenRecord.SubjectKind).
	SubjectKind string `bun:"subject_kind"`
}

// InsertSysOauthRefreshToken records a newly issued refresh token. Refresh
// tokens are only ever issued to base_users identities (client_credentials
// gets none, RFC 6749 §4.4.3), so every row is stamped as a user row.
func InsertSysOauthRefreshToken(ctx context.Context, db bun.IDB, userID int64, dbName, familyID, plaintext string, issuedAt, expiresAt time.Time) error {
	row := &SysOauthRefreshTokenRecord{
		UserID:      userID,
		DBName:      dbName,
		TokenHash:   HashRefreshToken(plaintext),
		FamilyID:    familyID,
		IssuedAt:    issuedAt,
		ExpiresAt:   expiresAt,
		SubjectKind: RefreshTokenSubjectUser,
	}
	_, err := db.NewInsert().Model(row).Exec(ctx)
	if err != nil {
		return fmt.Errorf("sys.oauth_refresh_token insert: %w", err)
	}
	return nil
}

// ConsumeSysOauthRefreshToken spends a refresh token: one conditional UPDATE
// revokes it only if it is still unrevoked and unexpired, and returns its row.
// Exactly one of any number of concurrent callers presenting the same token
// gets the row back (SEC-T-13, BUG-M-8); the separate select-then-revoke this
// replaces let two concurrent refreshes both mint a new pair in the family.
// The caller issues the replacement in the same transaction as this consume
// and must not revoke the token again.
//
// When nothing was consumed, a lookup classifies the token: unknown → invalid,
// revoked → reused=true (the caller must revoke the whole family via
// RevokeSysOauthRefreshTokenFamily after its transaction ends; a lost race
// also lands here: its UPDATE waits on the winner's row lock, so the family
// revoke sees and kills the winner's new token too), else expired.
func ConsumeSysOauthRefreshToken(ctx context.Context, db bun.IDB, plaintext string) (*refreshTokenRow, bool, error) {
	hash := HashRefreshToken(plaintext)
	now := time.Now()
	var consumed []refreshTokenRow
	err := db.NewRaw(`UPDATE sys_oauth_refresh_token SET revoked_at = ?
		WHERE token_hash = ? AND revoked_at IS NULL AND expires_at > ?
		RETURNING id, user_id, db_name, family_id, expires_at, revoked_at, COALESCE(subject_kind, '') AS subject_kind`,
		now, hash, now).Scan(ctx, &consumed)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, false, err
	}
	if len(consumed) > 0 {
		return &consumed[0], false, nil
	}

	var row refreshTokenRow
	err = db.NewSelect().
		TableExpr("sys_oauth_refresh_token AS t").
		ColumnExpr("t.id").
		ColumnExpr("t.user_id").
		ColumnExpr("t.db_name").
		ColumnExpr("t.family_id").
		ColumnExpr("t.expires_at").
		ColumnExpr("COALESCE(t.revoked_at, 'epoch'::timestamptz) AS revoked_at").
		ColumnExpr("COALESCE(t.subject_kind, '') AS subject_kind").
		Where("t.token_hash = ?", hash).
		Limit(1).
		Scan(ctx, &row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, false, orm.InvalidParamsf("invalid refresh token")
		}
		return nil, false, err
	}
	if !row.RevokedAt.IsZero() && row.RevokedAt.Year() > 1970 {
		return &row, true, nil
	}
	return nil, false, fmt.Errorf("refresh token has expired")
}

// RevokeSysOauthRefreshTokenByHash marks one refresh token revoked (logout).
func RevokeSysOauthRefreshTokenByHash(ctx context.Context, db *bun.DB, plaintext string) error {
	_, err := db.NewUpdate().
		Table("sys_oauth_refresh_token").
		Set("revoked_at = ?", time.Now()).
		Where("token_hash = ?", HashRefreshToken(plaintext)).
		Where("revoked_at IS NULL").
		Exec(ctx)
	return err
}

// RevokeSysOauthRefreshTokenFamily revokes every token sharing familyID, used
// as the breach-detection response when a revoked token is replayed.
func RevokeSysOauthRefreshTokenFamily(ctx context.Context, db *bun.DB, familyID string) error {
	_, err := db.NewUpdate().
		Table("sys_oauth_refresh_token").
		Set("revoked_at = ?", time.Now()).
		Where("family_id = ?", familyID).
		Where("revoked_at IS NULL").
		Exec(ctx)
	return err
}
