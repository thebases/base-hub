package orm

import (
	"strings"
	"sync"
)

// Import match fields: a model's natural key for master-data import.
//
// Seed rows are tracked by external id. When the row an external id points at was deleted and
// recreated by normal use (e.g. a workflow canvas save recreates every step), the external id goes
// stale and a reimport would create a duplicate, which a uniqueness rule then refuses. A model that
// registers match fields lets the importer adopt the existing row whose values on those fields
// equal the seed row's, write it, and re-point the external id at it.
var importMatchFields = struct {
	mu     sync.RWMutex
	fields map[string][]string
}{fields: map[string][]string{}}

// RegisterImportMatchFields declares the fields that identify one row of model for master-data
// import (all must be set on the seed row). A later registration replaces the earlier one.
func RegisterImportMatchFields(model string, fields ...string) {
	model = strings.TrimSpace(model)
	if model == "" || len(fields) == 0 {
		return
	}
	cleaned := make([]string, 0, len(fields))
	for _, field := range fields {
		if field = strings.TrimSpace(field); field != "" {
			cleaned = append(cleaned, field)
		}
	}
	importMatchFields.mu.Lock()
	defer importMatchFields.mu.Unlock()
	importMatchFields.fields[model] = cleaned
}

// ImportMatchFields returns the match fields registered for model, or nil.
func ImportMatchFields(model string) []string {
	importMatchFields.mu.RLock()
	defer importMatchFields.mu.RUnlock()
	fields := importMatchFields.fields[strings.TrimSpace(model)]
	if len(fields) == 0 {
		return nil
	}
	return append([]string(nil), fields...)
}
