package models

// File overview:
// - Responsibility: pure formatters for sequence numbers (date interpolation, padding) with no database access.
// - Key symbols: `sequenceNow`, `formatSequenceValue`, `interpolateSequenceDates`, `hasRecordMapKey`.

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
)

// sequenceNow resolves the timestamp used for date placeholders from the
// environment timezone (SRS-F-017 formatting rule 2), falling back to UTC so
// references stay deterministic when no user timezone is bound.
func sequenceNow(ctx context.Context) time.Time {
	now := time.Now().UTC()
	env := orm.EnvironmentFromContext(ctx)
	if env == nil {
		return now
	}
	raw, ok := env.ContextValue("tz")
	if !ok {
		return now
	}
	name, ok := stringFromAny(raw)
	if !ok || strings.TrimSpace(name) == "" {
		return now
	}
	location, err := time.LoadLocation(strings.TrimSpace(name))
	if err != nil {
		// An invalid stored timezone must not block number issuance; UTC keeps
		// the reference deterministic and the error visible in logs.
		log.Errorf("sys.sequence: invalid environment timezone %q, using UTC", name)
		return now
	}
	return now.In(location)
}

// formatSequenceValue renders `{prefix}{zero-padded number}{suffix}` with the
// SRS-F-017 date placeholders resolved against the environment-local time.
func formatSequenceValue(prefix, suffix string, padding, number int64, now time.Time) string {
	rendered := strconv.FormatInt(number, 10)
	if padding > 0 {
		rendered = fmt.Sprintf("%0*d", int(padding), number)
	}
	return interpolateSequenceDates(prefix, now) + rendered + interpolateSequenceDates(suffix, now)
}

// interpolateSequenceDates resolves the supported `%(...)s` placeholders. The
// placeholder set intentionally mirrors a common subset so migrated
// prefix definitions keep rendering identically.
func interpolateSequenceDates(pattern string, now time.Time) string {
	if !strings.Contains(pattern, "%(") {
		return pattern
	}
	replacer := strings.NewReplacer(
		"%(year)s", now.Format("2006"),
		"%(y)s", now.Format("06"),
		"%(month)s", now.Format("01"),
		"%(day)s", now.Format("02"),
		"%(h24)s", now.Format("15"),
		"%(min)s", now.Format("04"),
		"%(sec)s", now.Format("05"),
	)
	return replacer.Replace(pattern)
}

// hasRecordMapKey reports raw key presence so write handlers can distinguish
// "field not touched" from "field explicitly set to empty/NULL".
func hasRecordMapKey(values orm.RecordMap, key string) bool {
	_, ok := values[key]
	return ok
}
