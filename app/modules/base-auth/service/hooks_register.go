package service

// File overview:
// - Responsibility: registers base-auth's kernel hooks so internal/ never imports this module.

import (
	"context"

	"git.thebase.vn/base_hub/internal/modules"
	"git.thebase.vn/base_hub/internal/orm"
)

func init() {
	modules.RegisterBearerTokenValidator(validateBearerToken)
}

// validateBearerToken checks a /rpc bearer token against the provider active
// for this call (CurrentValidator re-reads the config every time).
func validateBearerToken(ctx context.Context, accessToken string) (*orm.RBACIdentity, error) {
	return CurrentValidator().Validate(ctx, accessToken)
}
