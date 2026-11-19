package base

import "testing"

// TestGetCodeMeaningCoversEveryConstant guards K-13: every code constant the
// API sends must resolve to its meaning (SuccessCode and UnknownError did not).
func TestGetCodeMeaningCoversEveryConstant(t *testing.T) {
	for _, code := range []int{SuccessCode, InvalidInput, UserNotFound, PermissionDenied, NoRecordFound,
		BizRuleViolation, ExternalServiceFailure, TargetNotFound, InternalServiceFailure, UnknownError} {
		if GetCodeMeaning(code) == "Unknown code" {
			t.Errorf("code %d has no meaning", code)
		}
	}
}
