package orm

// File overview:
// - Responsibility: the API error-code registry and typed error shared by the RPC and alias transports.
// - Key symbols: `Error`, `NewError`, the `Err*` code constants, `ErrorCodeForStatus`, `ErrorFrom`.
// - Contract: numbers are permanent once published; new codes are appended, never renumbered.

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
)

// Registered API error codes (JSON-RPC 2.0 style). See .agents/planning/2026-10-11-api-response-standard-plan.md.
const (
	ErrParse               = -32700 // 400: body is not valid JSON
	ErrInvalidRequest      = -32600 // 400: wrong envelope shape
	ErrMethodNotFound      = -32601 // 404: unknown model, method or route
	ErrInvalidParams       = -32602 // 400: validation failed
	ErrInternal            = -32603 // 500: unexpected, fixed message
	ErrServer              = -32000 // 400: generic runtime error (default for a plain error)
	ErrUnauthorized        = -32001 // 401
	ErrForbidden           = -32003 // 403
	ErrNotFound            = -32004 // 404
	ErrConflict            = -32009 // 409
	ErrIdempotencyConflict = -32010 // 409
	ErrBusinessRule        = -32022 // 422
	ErrRateLimited         = -32029 // 429
	ErrUnavailable         = -32050 // 503
)

// InternalErrorMessage is the only text a client sees for an unexpected failure.
const InternalErrorMessage = "an unexpected error occurred"

// Error is a typed API failure: an integer code clients branch on, the HTTP status that carries
// it, a human message, and optional structured extras.
type Error struct {
	Code    int
	Status  int
	Message string
	Data    map[string]any
}

// Error implements error.
func (e *Error) Error() string { return e.Message }

// NewError builds a typed error; a zero status is derived from the code.
func NewError(code, status int, message string, data ...map[string]any) *Error {
	if status == 0 {
		status = statusForCode(code)
	}
	out := &Error{Code: code, Status: status, Message: message}
	if len(data) > 0 {
		out.Data = data[0]
	}
	return out
}

// statusForCode returns the registry's HTTP status for a code (400 for unregistered ones).
func statusForCode(code int) int {
	switch code {
	case ErrMethodNotFound, ErrNotFound:
		return http.StatusNotFound
	case ErrInternal:
		return http.StatusInternalServerError
	case ErrUnauthorized:
		return http.StatusUnauthorized
	case ErrForbidden:
		return http.StatusForbidden
	case ErrConflict, ErrIdempotencyConflict:
		return http.StatusConflict
	case ErrBusinessRule:
		return http.StatusUnprocessableEntity
	case ErrRateLimited:
		return http.StatusTooManyRequests
	case ErrUnavailable:
		return http.StatusServiceUnavailable
	default:
		return http.StatusBadRequest
	}
}

// ErrorCodeForStatus picks the registry code for a bare HTTP status.
func ErrorCodeForStatus(status int) int {
	switch {
	case status == http.StatusUnauthorized:
		return ErrUnauthorized
	case status == http.StatusForbidden:
		return ErrForbidden
	case status == http.StatusNotFound:
		return ErrNotFound
	case status == http.StatusMethodNotAllowed:
		return ErrMethodNotFound
	case status == http.StatusConflict:
		return ErrConflict
	case status == http.StatusUnprocessableEntity:
		return ErrBusinessRule
	case status == http.StatusTooManyRequests:
		return ErrRateLimited
	case status == http.StatusServiceUnavailable, status == http.StatusBadGateway, status == http.StatusGatewayTimeout:
		return ErrUnavailable
	case status >= 500:
		return ErrInternal
	case status == http.StatusBadRequest:
		return ErrServer
	default:
		return ErrInvalidRequest
	}
}

// aliasCodes maps the legacy string codes of AliasError to registry codes.
var aliasCodes = map[string]int{
	"unauthorized":         ErrUnauthorized,
	"forbidden":            ErrForbidden,
	"not_found":            ErrNotFound,
	"conflict":             ErrConflict,
	"invalid_json":         ErrParse,
	"invalid_id":           ErrInvalidParams,
	"bad_request":          ErrServer,
	"idempotency_conflict": ErrIdempotencyConflict,
	"rate_limited":         ErrRateLimited,
	"unavailable":          ErrUnavailable,
}

// ErrorFrom normalizes any error into a typed one: *Error as-is, *AliasError by its string code
// (falling back to its status), anything else a plain -32000/400.
func ErrorFrom(err error) *Error {
	var typed *Error
	if errors.As(err, &typed) {
		return typed
	}
	if classified := ClassifyError(err); classified != nil {
		return classified
	}
	var aliasErr *AliasError
	if errors.As(err, &aliasErr) {
		code, ok := aliasCodes[strings.ToLower(aliasErr.Code)]
		var data map[string]any
		if !ok {
			code = ErrorCodeForStatus(aliasErr.Status)
			if aliasErr.Status == http.StatusBadRequest {
				code = ErrInvalidParams
			}
			// A module-specific string code has no registry number; keep it for clients as data.reason.
			if aliasErr.Code != "" {
				data = map[string]any{"reason": aliasErr.Code}
			}
		}
		message := aliasErr.Message
		if message == "" {
			message = http.StatusText(aliasErr.Status)
		}
		return &Error{Code: code, Status: aliasErr.Status, Message: message, Data: data}
	}
	return &Error{Code: ErrServer, Status: http.StatusBadRequest, Message: err.Error()}
}

// NotFoundf is a -32004/404 for a missing record or resource.
func NotFoundf(format string, args ...any) *Error {
	return NewError(ErrNotFound, 0, fmt.Sprintf(format, args...))
}

// Forbiddenf is a -32003/403 for an access or ownership denial.
func Forbiddenf(format string, args ...any) *Error {
	return NewError(ErrForbidden, 0, fmt.Sprintf(format, args...))
}

// Conflictf is a -32009/409 for a uniqueness or state clash.
func Conflictf(format string, args ...any) *Error {
	return NewError(ErrConflict, 0, fmt.Sprintf(format, args...))
}

// InvalidParamsf is a -32602/400 for a failed validation.
func InvalidParamsf(format string, args ...any) *Error {
	return NewError(ErrInvalidParams, 0, fmt.Sprintf(format, args...))
}

var (
	classifiersMu sync.RWMutex
	classifiers   []func(error) *Error
)

// RegisterErrorClassifier lets a module map its sentinel errors onto registry codes without
// wrapping them (errors.Is keeps working). A classifier returns nil for errors it doesn't know.
func RegisterErrorClassifier(fn func(error) *Error) {
	classifiersMu.Lock()
	defer classifiersMu.Unlock()
	classifiers = append(classifiers, fn)
}

// ClassifyError asks the registered classifiers; nil means nobody recognized the error.
func ClassifyError(err error) *Error {
	classifiersMu.RLock()
	defer classifiersMu.RUnlock()
	for _, fn := range classifiers {
		if typed := fn(err); typed != nil {
			return typed
		}
	}
	return nil
}

// legacyParamReasons are legacy string codes that mean "the input was invalid".
var legacyParamReasons = map[string]bool{"invalid_request": true, "invalid_sync": true, "invalid_id": true}

// LegacyError converts an alias Error hook's (status, body) into a typed error. The string code
// of a {"error","message"} body is kept as data.reason; other body keys are kept as data.
func LegacyError(status int, body any) *Error {
	fields, _ := body.(map[string]any)
	reason, _ := fields["error"].(string)
	message, hasMessage := fields["message"].(string)
	if !hasMessage {
		message = reason
	}
	code := ErrorCodeForStatus(status)
	if legacyParamReasons[reason] {
		code = ErrInvalidParams
	}
	if message == "" {
		message = http.StatusText(status)
	}
	out := &Error{Code: code, Status: status, Message: message}
	for key, value := range fields {
		if key == "error" && !hasMessage || key == "message" {
			continue
		}
		// A reason that just restates the registry code (e.g. "not_found" on -32004) is noise.
		if key == "error" && aliasCodes[strings.ToLower(reason)] == code {
			continue
		}
		if out.Data == nil {
			out.Data = map[string]any{}
		}
		if key == "error" {
			key = "reason"
		}
		out.Data[key] = value
	}
	return out
}
