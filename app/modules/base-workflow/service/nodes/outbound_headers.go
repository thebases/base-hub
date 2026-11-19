package nodes

// File overview:
// - Responsibility: shared outbound HTTP header resolution used by both the notify and
//   post_output_object nodes. Not itself a node.
// - Key symbols: `actionOutboundHeaders`.

import (
	"fmt"
	"net/textproto"
	"strings"
)

// defaultOutboundContentType is the fallback Content-Type applied only when
// the action's configured headers don't already set one (case-insensitively).
const defaultOutboundContentType = "application/json"

// actionOutboundHeaders resolves the configured Headers action input, merging
// it over the application/json default so a custom Content-Type can still be
// overridden by the caller instead of being silently dropped. Shared by
// post_output_object and notify.
//
// params.authorization is optional and defaults to nil: when absent or blank
// no Authorization header is added. When set — typically an `@cred:<name>`
// reference already resolved to its decrypted token/api-key value by
// resolveCredentialRefs before the handler runs — it is written as the
// Authorization header. It is applied after the configured headers map so a
// credential-backed authorization always wins over a stale literal header.
func actionOutboundHeaders(params map[string]any) (map[string]string, error) {
	headers := map[string]string{}
	configured, ok, err := actionObjectParam(params, "headers")
	if err != nil {
		return nil, fmt.Errorf("params.headers must be a JSON object: %w", err)
	}
	if ok {
		for key, value := range configured {
			if str, ok := value.(string); ok {
				// Canonicalize so a caller-configured header (any casing) collapses
				// onto the same key instead of riding alongside the default below.
				headers[textproto.CanonicalMIMEHeaderKey(key)] = str
			}
		}
	}
	contentTypeKey := textproto.CanonicalMIMEHeaderKey("Content-Type")
	if _, exists := headers[contentTypeKey]; !exists {
		headers[contentTypeKey] = defaultOutboundContentType
	}
	if authorization := strings.TrimSpace(firstStringParam(params, "authorization")); authorization != "" {
		headers[textproto.CanonicalMIMEHeaderKey("Authorization")] = authorization
	}
	return headers, nil
}
