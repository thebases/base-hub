package libs

// File overview:
// - Responsibility: resolves @cred:<name> and @syscred:<name> references in workflow action param
//   maps by looking up and decrypting named credential values at execution time.
// - Key symbols: `ResolveCredentialRefs`, `credentialRefPrefix`, `sysCredentialRefPrefix`.
// - Pattern: Keeps credential resolution close to the execution engine so secrets are decrypted
//   only at the moment the step runs, minimizing their lifetime in memory.

import (
	"context"
	"fmt"
	"strings"

	credentialmodels "git.thebase.vn/base_hub/modules/base-credential/models"
	workflowmodels "git.thebase.vn/base_hub/modules/base-workflow/models"
)

const (
	credentialRefPrefix    = "@cred:"
	sysCredentialRefPrefix = "@syscred:"
)

// credentialRefLookup resolves one ref string to its plaintext; a package var only so tests can
// stub the credential stores.
var credentialRefLookup = resolveCredentialRefValue

// ResolveCredentialRefs resolves the @cred:<name> / @syscred:<name> references the workflow author
// wrote and returns params with each of them replaced by its plaintext. Returns a new map when
// anything resolves; neither input is mutated.
//
// authored is the stored step config (before payload interpolation); params is the same map after
// interpolation, i.e. what the handler would receive. Eligibility is decided on authored only:
// a key resolves when its authored value, trimmed, is a string starting with "@cred:" or
// "@syscred:", and the ref is read verbatim from that authored text (so "@cred:{{name}}" looks up
// the literal name "{{name}}" and fails, the payload never picks which secret is decrypted).
// Every other key keeps its interpolated value, even when that value now looks like a ref:
// "{{token}}" or "Bearer {{token}}" fed {"token":"@syscred:x"} is sent as that literal text.
// Resolving refs found in interpolated output would let any caller of a /wh or topic trigger
// exfiltrate a secret through a templated header plus a templated URL (WF-S-1).
func ResolveCredentialRefs(ctx context.Context, authored, params map[string]any) (map[string]any, error) {
	var resolved map[string]any
	for k, v := range authored {
		ref, ok := authoredCredentialRef(v)
		if !ok {
			continue
		}
		if resolved == nil {
			resolved = make(map[string]any, len(params))
			for kk, vv := range params {
				resolved[kk] = vv
			}
		}
		value, err := credentialRefLookup(ctx, k, ref)
		if err != nil {
			return nil, err
		}
		resolved[k] = value
	}
	if resolved == nil {
		return params, nil
	}
	return resolved, nil
}

// ResolveCredentialRef resolves one stored @cred:/@syscred: reference that is not a step param
// (the webhook trigger hmac_secret, T43). ctx should carry the workflow scope
// (workflowmodels.ContextWithWorkflowScope) so only that workflow's credentials resolve.
func ResolveCredentialRef(ctx context.Context, ref string) (string, error) {
	ref, ok := authoredCredentialRef(ref)
	if !ok {
		return "", fmt.Errorf("workflow: value is not a @cred:/@syscred: reference")
	}
	return credentialRefLookup(ctx, "", ref)
}

// AuthoredCredentialRefs returns, per param name, the @cred:/@syscred: reference the author wrote
// (the same keys ResolveCredentialRefs resolves), so a node can store the reference instead of the
// resolved secret. Returns nil when there is none.
func AuthoredCredentialRefs(authored map[string]any) map[string]string {
	var refs map[string]string
	for k, v := range authored {
		ref, ok := authoredCredentialRef(v)
		if !ok {
			continue
		}
		if refs == nil {
			refs = map[string]string{}
		}
		refs[k] = ref
	}
	return refs
}

// authoredCredentialRef reports whether an authored param value is a credential ref and returns
// it trimmed.
func authoredCredentialRef(v any) (string, bool) {
	s, ok := v.(string)
	if !ok {
		return "", false
	}
	trimmed := strings.TrimSpace(s)
	return trimmed, strings.HasPrefix(trimmed, credentialRefPrefix) || strings.HasPrefix(trimmed, sysCredentialRefPrefix)
}

// resolveCredentialRefValue resolves a single @cred:/@syscred: ref string to its plaintext value.
// For @syscred: refs feeding the "authorization" param, the decrypted access token is prefixed
// with "Basic " or "Bearer " based on the sys.credential's credential_type, so the resolved value
// is ready to use verbatim as an HTTP Authorization header.
func resolveCredentialRefValue(ctx context.Context, paramName, ref string) (string, error) {
	if strings.HasPrefix(ref, sysCredentialRefPrefix) {
		name := strings.TrimPrefix(ref, sysCredentialRefPrefix)
		secrets, err := credentialmodels.LookupSysCredentialSecretsByName(ctx, name)
		if err != nil {
			return "", err
		}
		if paramName == "authorization" {
			return sysCredentialAuthorizationHeaderPrefix(secrets.CredentialType) + secrets.AccessToken, nil
		}
		return secrets.AccessToken, nil
	}
	name := strings.TrimPrefix(ref, credentialRefPrefix)
	return workflowmodels.LookupWorkflowCredential(ctx, name)
}

// sysCredentialAuthorizationHeaderPrefix maps a sys.credential credential_type to the HTTP
// Authorization scheme prefix its access token should be sent with: rbac logins are
// username/password-backed sessions, sent as "Basic "; every OAuth/API-key flow (oidc_client,
// oidc_password, apikey) issues a bearer-style token, sent as "Bearer ".
func sysCredentialAuthorizationHeaderPrefix(credentialType string) string {
	if credentialType == credentialmodels.CredentialTypeRBAC {
		return "Basic "
	}
	return "Bearer "
}
