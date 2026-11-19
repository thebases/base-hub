package base

// File overview:
// - Responsibility: captures shared API response and constant primitives for constant, keeping cross-cutting behavior consistent across routes.
// - Key symbols: `GetCodeMeaning`.
// - Pattern: Keeps one focused responsibility per file so adjacent packages can reuse this logic without depending on unrelated transport or persistence details.

// Application and business logic codes. The constants are the values sent on
// the wire; appCodeMap is keyed by them so the two can never disagree again.
const (
	SuccessCode            int = 0
	InvalidInput           int = 1001
	UserNotFound           int = 1002
	PermissionDenied       int = 1003
	NoRecordFound          int = 1011
	BizRuleViolation       int = 2001
	ExternalServiceFailure int = 3001
	TargetNotFound         int = 4004
	InternalServiceFailure int = 5001
	UnknownError           int = 9999
)

// Application response codes and their meanings
var appCodeMap = map[int]string{
	SuccessCode:            "Success",
	InvalidInput:           "Invalid input data",
	UserNotFound:           "User not found",
	PermissionDenied:       "Permission denied",
	NoRecordFound:          "No record Found",
	BizRuleViolation:       "Business rule violation",
	ExternalServiceFailure: "External service failure",
	TargetNotFound:         "Target not found",
	InternalServiceFailure: "Internal service failure",
	UnknownError:           "Unknown application error",
}

// Get meaning of a code
func GetCodeMeaning(code int) string {
	if message, exists := appCodeMap[code]; exists {
		return message
	}
	return "Unknown code"
}
