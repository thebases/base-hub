package cmd

// File overview:
// - Responsibility: keeps the HTTP access log free of credentials whatever `logging.pattern` says.
// - Key symbols: `sanitizeAccessLogPattern`.
// - Pattern: allowlist of fiber logger tags; the pattern comes from config.yaml or sys.config_param
//   (group_system can edit it), so tags that print headers, bodies, cookies, forms or query strings
//   (bearer tokens, session cookies, `?token=` on bus streams, login bodies) are removed at boot.

import (
	"regexp"
	"strings"
)

var accessLogTagPattern = regexp.MustCompile(`\$\{([^}]*)\}`)

// accessLogAllowedTags are fiber logger tags that never carry client secrets.
// `url`, `queryParams` and `query:` are left out because some routes take a token
// in the query string; `reqHeaders`, `body`, `resBody`, `form:`, `cookie:` because
// they print credentials directly.
var accessLogAllowedTags = map[string]bool{
	"pid": true, "time": true, "referer": true, "protocol": true, "scheme": true,
	"port": true, "ip": true, "ips": true, "host": true, "method": true, "path": true,
	"route": true, "ua": true, "latency": true, "status": true, "bytesSent": true,
	"bytesReceived": true, "error": true,
	"black": true, "red": true, "green": true, "yellow": true, "blue": true,
	"magenta": true, "cyan": true, "white": true, "reset": true,
}

// accessLogAllowedHeaders are the only request/response headers a pattern may print
// through `reqHeader:<name>` / `respHeader:<name>` (lower-case).
var accessLogAllowedHeaders = map[string]bool{
	"user-agent": true, "content-type": true, "content-length": true, "accept": true,
	"accept-language": true, "x-request-id": true, "x-forwarded-for": true,
	"x-real-ip": true, "referer": true, "origin": true,
}

// sanitizeAccessLogPattern drops every `${tag}` that is not allowlisted. `${reqHeaders}`
// becomes `${ua}` so existing patterns keep a client hint in that slot. `locals:` stays:
// locals are set by server code, not by the client.
func sanitizeAccessLogPattern(pattern string) string {
	return accessLogTagPattern.ReplaceAllStringFunc(pattern, func(token string) string {
		tag := token[2 : len(token)-1]
		switch {
		case accessLogAllowedTags[tag]:
			return token
		case tag == "reqHeaders":
			return "${ua}"
		case strings.HasPrefix(tag, "locals:"):
			return token
		case strings.HasPrefix(tag, "reqHeader:"):
			if accessLogAllowedHeaders[strings.ToLower(strings.TrimPrefix(tag, "reqHeader:"))] {
				return token
			}
		case strings.HasPrefix(tag, "respHeader:"):
			if accessLogAllowedHeaders[strings.ToLower(strings.TrimPrefix(tag, "respHeader:"))] {
				return token
			}
		}
		return ""
	})
}
