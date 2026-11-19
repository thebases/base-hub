package models

// File overview:
// - Responsibility: normalises and validates sys.sequence create/write values.
// - Key symbols: `normalizeSysSequenceValues`, `validateSysSequenceValues`, `sequenceCodeFromArgs`.

import (
	"fmt"
	"strings"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
)

// normalizeSysSequenceValues applies the SRS-F-017 defaults and validation to
// one create/write payload without mutating the caller's map.
func normalizeSysSequenceValues(values orm.RecordMap, creating bool) (orm.RecordMap, error) {
	working := cloneRecordMap(values)

	// Apply the SRS-F-017 defaults on create so seed rows and UI-created
	// sequences start from one consistent, audit-safe state.
	if creating {
		if _, ok := working["active"]; !ok {
			working["active"] = true
		}
		if _, ok := working["prefix"]; !ok {
			working["prefix"] = ""
		}
		if _, ok := working["suffix"]; !ok {
			working["suffix"] = ""
		}
		if _, ok := working["padding"]; !ok {
			working["padding"] = int64(0)
		}
		if _, ok := working["increment"]; !ok {
			working["increment"] = int64(1)
		}
		if _, ok := working["number_next"]; !ok {
			working["number_next"] = int64(1)
		}
		// no_gap is the default on purpose: this platform issues financial
		// references, so the audit-grade mode must be the one you get without
		// thinking, and standard must be an explicit opt-in.
		if _, ok := working["implementation"]; !ok {
			working["implementation"] = SysSequenceImplementationNoGap
		}
	}

	// Walk each trimmed string field in order so validation and side effects stay scoped to the current element.
	for _, key := range []string{"name", "code", "implementation"} {
		if raw, ok := stringFromAny(working[key]); ok {
			working[key] = strings.TrimSpace(raw)
		}
	}

	if err := validateSysSequenceValues(working, creating); err != nil {
		return nil, err
	}
	return working, nil
}

// validateSysSequenceValues validates one normalized payload against the
// SRS-F-017 field rules.
func validateSysSequenceValues(values orm.RecordMap, creating bool) error {
	// Walk each required string field in order so validation and side effects stay scoped to the current element.
	for _, key := range []string{"name", "code"} {
		raw, ok := values[key]
		if !ok {
			if creating {
				log.Errorf("sys.sequence %s is required", key)
				return fmt.Errorf("sys.sequence %s is required", key)
			}
			continue
		}
		if strings.TrimSpace(stringValue(raw)) == "" {
			log.Errorf("sys.sequence %s must not be empty", key)
			return fmt.Errorf("sys.sequence %s must not be empty", key)
		}
	}

	if raw, ok := values["implementation"]; ok {
		mode := strings.TrimSpace(stringValue(raw))
		// The mode set is closed: an unknown mode would silently fall back to
		// no-gap in the claim path and mislead operators about the guarantees.
		if mode != SysSequenceImplementationNoGap && mode != SysSequenceImplementationStandard {
			log.Errorf("sys.sequence implementation must be %q or %q", SysSequenceImplementationNoGap, SysSequenceImplementationStandard)
			return fmt.Errorf("sys.sequence implementation must be %q or %q", SysSequenceImplementationNoGap, SysSequenceImplementationStandard)
		}
		values["implementation"] = mode
	}

	if raw, ok := values["padding"]; ok {
		padding, parsed := int64FromAny(raw)
		if !parsed || padding < 0 {
			log.Errorf("sys.sequence padding must be zero or a positive integer")
			return fmt.Errorf("sys.sequence padding must be zero or a positive integer")
		}
		values["padding"] = padding
	}

	if raw, ok := values["increment"]; ok {
		increment, parsed := int64FromAny(raw)
		// A non-positive increment would break the monotonically-increasing
		// contract every consumer of financial references depends on.
		if !parsed || increment < 1 {
			log.Errorf("sys.sequence increment must be a positive integer")
			return fmt.Errorf("sys.sequence increment must be a positive integer")
		}
		values["increment"] = increment
	}

	if raw, ok := values["number_next"]; ok {
		numberNext, parsed := int64FromAny(raw)
		if !parsed || numberNext < 1 {
			log.Errorf("sys.sequence number_next must be a positive integer")
			return fmt.Errorf("sys.sequence number_next must be a positive integer")
		}
		values["number_next"] = numberNext
	}
	return nil
}

// sequenceCodeFromArgs accepts the code positionally (`args[0]`, the
// calling convention) or as `code` / `sequence_code` kwargs so both RPC styles
// resolve to one lookup key.
func sequenceCodeFromArgs(args []any, kwargs map[string]any) string {
	if len(args) > 0 {
		if code, ok := stringFromAny(args[0]); ok {
			return strings.TrimSpace(code)
		}
	}
	// Walk each accepted kwarg key in order so validation and side effects stay scoped to the current element.
	for _, key := range []string{"code", "sequence_code"} {
		if raw, ok := kwargs[key]; ok {
			if code, okStr := stringFromAny(raw); okStr {
				return strings.TrimSpace(code)
			}
		}
	}
	return ""
}
