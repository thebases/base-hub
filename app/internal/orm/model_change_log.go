package orm

// File overview:
// - Responsibility: implements the metadata-driven ORM/runtime kernel for model change log, where storage, runtime dispatch, and security rules stay centralized.
// - Key symbols: `RegisterModelChangeLogger`, `LogModelChange`, `ModelChangeLogEntry`, `modelChangeLoggerFunc`.
// - Pattern: Leans on metadata-driven registration so storage mapping, runtime methods, and behavior hooks stay centralized instead of being scattered across handlers.
// - Pattern: Uses Bun-facing model or repository patterns so schema mapping stays explicit and transaction-aware inside the backend data layer.

import (
	"context"
	"sync"

	"github.com/uptrace/bun"
)

type ModelChangeLogEntry struct {
	ChangeType    string
	Action        string
	TargetModel   string
	TargetRecord  string
	SubjectModel  string
	SubjectName   string
	Source        string
	UserID        int64
	ChangedFields []string
	BeforeData    RecordMap
	AfterData     RecordMap
}

type modelChangeLoggerFunc func(context.Context, bun.IDB, ModelChangeLogEntry) error

var modelChangeLogger struct {
	mu sync.RWMutex
	fn modelChangeLoggerFunc
}

// RegisterModelChangeLogger installs the process-wide metadata audit logger.
func RegisterModelChangeLogger(fn modelChangeLoggerFunc) {
	modelChangeLogger.mu.Lock()
	defer modelChangeLogger.mu.Unlock()
	modelChangeLogger.fn = fn
}

// LogModelChange emits one structured metadata change event when a logger is
// installed. A missing logger is treated as a no-op so bootstrap callers can
// stay simple in tests that do not register the audit model.
func LogModelChange(ctx context.Context, db bun.IDB, entry ModelChangeLogEntry) error {
	modelChangeLogger.mu.RLock()
	fn := modelChangeLogger.fn
	modelChangeLogger.mu.RUnlock()
	if fn == nil {
		return nil
	}
	return fn(ctx, db, cloneModelChangeLogEntry(entry))
}

// cloneModelChangeLogEntry clones the model change log entry handled by this package.
func cloneModelChangeLogEntry(entry ModelChangeLogEntry) ModelChangeLogEntry {
	cloned := entry
	if len(entry.ChangedFields) > 0 {
		cloned.ChangedFields = append([]string(nil), entry.ChangedFields...)
	}
	if entry.BeforeData != nil {
		cloned.BeforeData = cloneChangeLogRecordMap(entry.BeforeData)
	}
	if entry.AfterData != nil {
		cloned.AfterData = cloneChangeLogRecordMap(entry.AfterData)
	}
	return cloned
}

// cloneChangeLogRecordMap clones the change log record map handled by this package.
func cloneChangeLogRecordMap(values RecordMap) RecordMap {
	if values == nil {
		return nil
	}
	cloned := make(RecordMap, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}
