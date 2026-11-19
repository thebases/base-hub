package modules

// File overview:
// - Responsibility: inbound hook letting the auth module validate /rpc bearer tokens without
//   internal/ importing modules/base-auth.
// - Key symbols: `BearerTokenValidator`, `RegisterBearerTokenValidator`, `ValidateBearerToken`,
//   `ErrNoBearerTokenValidator`.

import (
	"context"
	"errors"
	"sync/atomic"

	"git.thebase.vn/base_hub/internal/orm"
)

// BearerTokenValidator resolves an access token into the caller identity, or
// returns an error when the token is not valid.
type BearerTokenValidator func(ctx context.Context, accessToken string) (*orm.RBACIdentity, error)

// ErrNoBearerTokenValidator: no auth module registered a validator, so every
// bearer token is rejected (fail closed).
var ErrNoBearerTokenValidator = errors.New("bearer token validation is not available")

// atomic so tests can swap the validator while requests are in flight.
var bearerTokenValidator atomic.Pointer[BearerTokenValidator]

// RegisterBearerTokenValidator installs the token check (base-auth calls it
// from init()). nil clears it.
func RegisterBearerTokenValidator(fn BearerTokenValidator) {
	if fn == nil {
		bearerTokenValidator.Store(nil)
		return
	}
	bearerTokenValidator.Store(&fn)
}

// ValidateBearerToken runs the registered validator; without one it fails closed.
func ValidateBearerToken(ctx context.Context, accessToken string) (*orm.RBACIdentity, error) {
	fn := bearerTokenValidator.Load()
	if fn == nil {
		return nil, ErrNoBearerTokenValidator
	}
	return (*fn)(ctx, accessToken)
}
