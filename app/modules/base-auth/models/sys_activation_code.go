package models

// File overview:
// - Responsibility: defines `sys.activation_code` — a hashed-at-rest, single-use activation code
//   any module's model can attach to one of its own rows to gate a state transition (e.g. "device
//   goes from pending to active") behind a short-lived, out-of-band-delivered code. Generalizes
//   device-edc-service's internal/models/device_activation.go (there hard-tied to a device
//   "serial") into a subject_model+subject_id shape so it isn't owned by any one module's table.
// - Key symbols: `SysActivationCodeRecord`, `InsertSysActivationCode`, `ConsumeSysActivationCode`,
//   `GetActiveSysActivationCode`.
// - Pattern: mirrors sys_oauth_auth_code.go's hashed-opaque-code convention (explicit bun.BaseModel
//   table tag because these functions issue raw *bun.DB/bun.Tx queries, not just metadata-driven
//   runtime handler calls); ConsumeSysActivationCode keeps the same single-statement
//   UPDATE ... WHERE ... RETURNING shape so two concurrent verify attempts for the same subject
//   can never both succeed.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"git.thebase.vn/base_hub/internal/orm"
	"github.com/uptrace/bun"
)

// SysActivationCodeRecord stores one issued activation code, hashed at rest.
// SubjectModel/SubjectID name the target row this code gates (e.g.
// SubjectModel="device.device", SubjectID=<that device's id>) — any module's
// model can be a subject without base-auth importing that module.
type SysActivationCodeRecord struct {
	bun.BaseModel `bun:"table:sys_activation_code,alias:sys_activation_code"`
	orm.MetadataBaseModel
	SubjectModel string    `bun:"subject_model" json:"subject_model" meta:"index,label:Subject Model"`
	SubjectID    int64     `bun:"subject_id" json:"subject_id" meta:"index,label:Subject ID"`
	CodeHash     string    `bun:"code_hash" json:"code_hash" meta:"index,unique,label:Code Hash"`
	IssuedBy     int64     `bun:"issued_by,nullzero" json:"issued_by" meta:"label:Issued By"`
	ExpiresAt    time.Time `bun:"expires_at" json:"expires_at" meta:"index,label:Expires At"`
	UsedAt       time.Time `bun:"used_at,nullzero" json:"used_at" meta:"label:Used At"`
}

const SysActivationCodeModelName = "sys.activation_code"

var SysActivationCodeModel = orm.Define[SysActivationCodeRecord](&Models, 10, orm.ModelSpec{
	Name:        SysActivationCodeModelName,
	Label:       "Activation Code",
	Description: "Hashed, single-use activation code gating a state transition on another module's model row",
})

var (
	// ErrActivationCodeInvalid deliberately covers "no such code", "already
	// used", and "expired" alike. Callers gating an externally reachable
	// verify endpoint must not let a caller distinguish those cases (same
	// oracle concern sys_oauth_auth_code.go's ConsumeSysOauthAuthCode
	// documents). Use GetActiveSysActivationCode for admin/ops surfaces that
	// need the specific reason.
	ErrActivationCodeInvalid = errors.New("activation code invalid or expired")
	// ErrActivationCodeNotFound is returned only by the introspection path
	// (GetActiveSysActivationCode), never by ConsumeSysActivationCode.
	ErrActivationCodeNotFound = errors.New("activation code not found")
)

// InsertSysActivationCode invalidates any outstanding unused code for the
// same subject, then inserts the new hashed row, so a subject only ever has
// one live activation code at a time (mirrors device-edc-service's
// upsert-by-serial semantics without needing a composite unique constraint).
// db may be a *bun.DB or a bun.Tx so callers can fold code issuance into
// their own subject-row transaction.
func InsertSysActivationCode(ctx context.Context, db bun.IDB, subjectModel string, subjectID int64, codeHash string, issuedBy int64, expiresAt time.Time) error {
	if _, err := db.NewUpdate().
		Table("sys_activation_code").
		Set("used_at = ?", time.Now()).
		Where("subject_model = ?", subjectModel).
		Where("subject_id = ?", subjectID).
		Where("used_at IS NULL").
		Exec(ctx); err != nil {
		return fmt.Errorf("sys.activation_code invalidate previous: %w", err)
	}

	row := &SysActivationCodeRecord{
		SubjectModel: subjectModel,
		SubjectID:    subjectID,
		CodeHash:     codeHash,
		IssuedBy:     issuedBy,
		ExpiresAt:    expiresAt,
	}
	if _, err := db.NewInsert().Model(row).Exec(ctx); err != nil {
		return fmt.Errorf("sys.activation_code insert: %w", err)
	}
	return nil
}

// ConsumeSysActivationCode atomically marks one matching, unused, unexpired
// code as used. The single UPDATE ... WHERE ... RETURNING statement is what
// prevents two concurrent verify attempts for the same subject from both
// succeeding — callers must not add a separate SELECT-then-UPDATE around
// this. Returns ErrActivationCodeInvalid for any non-match (see that error's
// doc comment for why the reason is not distinguished).
func ConsumeSysActivationCode(ctx context.Context, db bun.IDB, subjectModel string, subjectID int64, codeHash string) error {
	res, err := db.NewUpdate().
		Table("sys_activation_code").
		Set("used_at = ?", time.Now()).
		Where("subject_model = ?", subjectModel).
		Where("subject_id = ?", subjectID).
		Where("code_hash = ?", codeHash).
		Where("used_at IS NULL").
		Where("expires_at > ?", time.Now()).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("sys.activation_code consume: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("sys.activation_code consume: %w", err)
	}
	if affected == 0 {
		return ErrActivationCodeInvalid
	}
	return nil
}

// GetActiveSysActivationCode returns the current outstanding (unused) code
// row for a subject, if any. Admin/ops introspection only (e.g. "has this
// device already been issued a pending code, and when does it expire") —
// never use it to gate the actual code check, that must go through
// ConsumeSysActivationCode's atomic path.
func GetActiveSysActivationCode(ctx context.Context, db bun.IDB, subjectModel string, subjectID int64) (*SysActivationCodeRecord, error) {
	var row SysActivationCodeRecord
	err := db.NewSelect().
		Model(&row).
		Where("subject_model = ?", subjectModel).
		Where("subject_id = ?", subjectID).
		Where("used_at IS NULL").
		Order("id DESC").
		Limit(1).
		Scan(ctx)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrActivationCodeNotFound
		}
		return nil, err
	}
	return &row, nil
}
