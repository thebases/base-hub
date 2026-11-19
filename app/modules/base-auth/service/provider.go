package service

// File overview:
// - Responsibility: selects the active TokenIssuer per call, never cached at startup, so
//   AuthService.Enable can flip via sys_config_param without a restart, matching this repo's
//   SyncRuntimeConfig convention (RBAC/OAuth backend plan §5).
// - Key symbols: `CurrentProvider`.

import (
	"sync"

	"git.thebase.vn/base_hub/internal/config"
)

var (
	builtinOnce     sync.Once
	builtinInstance *BuiltinProvider

	oidcOnce     sync.Once
	oidcInstance *OIDCProvider
)

// CurrentProvider returns the active TokenIssuer for this call. Read live off
// config.Get() every time — only the OIDC provider's own JWKS cache applies
// an internal TTL, the provider *selection* itself must never be stale.
func CurrentProvider() TokenIssuer {
	if config.Get().AuthService.Enable {
		oidcOnce.Do(func() { oidcInstance = NewOIDCProvider() })
		return oidcInstance
	}
	builtinOnce.Do(func() { builtinInstance = NewBuiltinProvider() })
	return builtinInstance
}

// CurrentValidator returns the active TokenValidator for this call. Same live
// selection as CurrentProvider, narrowed to bearer-check-only callers that
// have no business depending on the full issuance/refresh/revoke surface.
func CurrentValidator() TokenValidator {
	return CurrentProvider()
}
