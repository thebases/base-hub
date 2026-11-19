package service

// File overview:
// - Responsibility: generic "activation with code" primitive any base-hub module can reuse to gate
//   a one-time state transition on one of its own model's rows behind a short-lived, HMAC-hashed
//   code (e.g. "device moves from pending to active once the printed activation code is entered").
//   Adapted from device-edc-service's internal/services/device_activation_service.go +
//   internal/services/provisioning/crypto.go, generalized from a device "serial" to this repo's
//   subject_model+subject_id shape so base-auth doesn't need to import any consuming module.
// - Key symbols: `IssueActivationCode`, `VerifyActivationCode`.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"git.thebase.vn/base_hub/internal/utils"
	authmodels "git.thebase.vn/base_hub/modules/base-auth/models"
	"github.com/uptrace/bun"
)

// DefaultActivationCodeLength matches device-edc-service's 8-digit numeric
// activation code (10^8 code space).
const DefaultActivationCodeLength = 8

// DefaultActivationCodeTTL is used whenever a caller passes ttl <= 0.
const DefaultActivationCodeTTL = 60 * time.Minute

// IssuedActivationCode is returned to the caller issuing a code. Code is the
// plaintext value to hand to whoever must enter it (printed on a device
// label, sent by SMS, ...); it is never persisted — only its HMAC digest is.
type IssuedActivationCode struct {
	Code      string
	ExpiresAt time.Time
}

// IssueActivationCode generates a random numeric code for one
// (subjectModel, subjectID) pair — e.g. subjectModel="device.device",
// subjectID=<device row id> — invalidates any outstanding unused code for
// that same subject, and persists only the code's HMAC digest.
//
// db may be a *bun.DB or a bun.Tx: pass a bun.Tx when the caller also needs
// to reset the subject row itself (e.g. back to a "pending activation"
// state) atomically alongside issuing the new code, the same way
// device-edc-service's ensureDeviceReadyForActivationTx runs inside the same
// transaction as the activation upsert.
func IssueActivationCode(ctx context.Context, db bun.IDB, subjectModel string, subjectID int64, issuedBy int64, ttl time.Duration, codeLength int) (*IssuedActivationCode, error) {
	subjectModel = strings.TrimSpace(subjectModel)
	if subjectModel == "" || subjectID <= 0 {
		return nil, fmt.Errorf("activation: subjectModel and subjectID are required")
	}
	if ttl <= 0 {
		ttl = DefaultActivationCodeTTL
	}
	if codeLength <= 0 {
		codeLength = DefaultActivationCodeLength
	}

	secret, err := LoadOrCreateActivationCodeSecret(ctx)
	if err != nil {
		return nil, err
	}

	code, err := utils.RandomNumericString(codeLength)
	if err != nil {
		return nil, fmt.Errorf("activation: generate code: %w", err)
	}

	expiresAt := time.Now().UTC().Add(ttl)
	if err := authmodels.InsertSysActivationCode(ctx, db, subjectModel, subjectID, hashActivationCode(secret, code), issuedBy, expiresAt); err != nil {
		return nil, err
	}
	return &IssuedActivationCode{Code: code, ExpiresAt: expiresAt}, nil
}

// VerifyActivationCode atomically consumes a code for one subject, so two
// concurrent verification attempts can never both succeed. Returns
// authmodels.ErrActivationCodeInvalid on any mismatch, expiry, or reuse —
// callers exposing this behind an externally reachable endpoint must forward
// that single error as-is rather than distinguishing the reason, the same
// oracle-avoidance rule sys_oauth_auth_code.go documents for authorization
// codes. Pass a bun.Tx to fold the caller's own state transition (e.g.
// flipping the subject row to "active") into the same atomic unit as the
// code consumption.
func VerifyActivationCode(ctx context.Context, db bun.IDB, subjectModel string, subjectID int64, code string) error {
	subjectModel = strings.TrimSpace(subjectModel)
	code = strings.TrimSpace(code)
	if subjectModel == "" || subjectID <= 0 || code == "" {
		return authmodels.ErrActivationCodeInvalid
	}

	secret, err := LoadOrCreateActivationCodeSecret(ctx)
	if err != nil {
		return err
	}
	return authmodels.ConsumeSysActivationCode(ctx, db, subjectModel, subjectID, hashActivationCode(secret, code))
}

// hashActivationCode returns the HMAC-SHA256 hex digest stored for an
// activation code. Unlike the high-entropy opaque tokens in
// sys_oauth_refresh_token.go/sys_oauth_auth_code.go (safe with a plain
// SHA-256 digest since brute-forcing a random 256-bit token is infeasible
// regardless of secrecy), activation codes are short, human-typeable,
// low-entropy values (8 numeric digits = 10^8 space). Keying the digest with
// a server-only secret is what stops an attacker who reads a DB leak from
// brute-forcing every possible code offline in seconds.
func hashActivationCode(secret, code string) string {
	return utils.GenerateHMAC256(secret, strings.TrimSpace(code))
}
