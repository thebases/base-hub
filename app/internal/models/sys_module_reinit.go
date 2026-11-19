package models

// File overview:
// - Responsibility: completion tracking for sys.module.module action_reinit so a client whose
//   connection dropped mid-reinit can learn the outcome (bus event + status poll).
// - Key symbols: `reinitTracker`, `publishModuleReinitEvent`, `reinitStatusSysModule`.

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
)

const (
	sysModuleReinitSucceededEvent = "sys.module.reinit_succeeded"
	sysModuleReinitFailedEvent    = "sys.module.reinit_failed"
	reinitTrackerTTL              = time.Hour
)

type reinitEntry struct {
	status  string // running | succeeded | failed
	errText string
	at      time.Time
}

// reinitTracker is process-local on purpose: a restart loses entries, and the
// status RPC then answers "unknown" so the client falls back to its reload path.
var reinitTracker = struct {
	mu      sync.Mutex
	entries map[string]reinitEntry
}{entries: map[string]reinitEntry{}}

func setReinitStatus(requestID, status, errText string) {
	if requestID == "" {
		return
	}
	reinitTracker.mu.Lock()
	defer reinitTracker.mu.Unlock()
	now := time.Now()
	for id, e := range reinitTracker.entries {
		if now.Sub(e.at) > reinitTrackerTTL {
			delete(reinitTracker.entries, id)
		}
	}
	reinitTracker.entries[requestID] = reinitEntry{status: status, errText: errText, at: now}
}

// publishModuleReinitEvent records the outcome and pushes it on the global and
// per-user service-bus channels (same shape as base.database notifications).
func publishModuleReinitEvent(ctx context.Context, requestID string, names []string, reinitErr error) {
	event, status, errText := sysModuleReinitSucceededEvent, "succeeded", ""
	if reinitErr != nil {
		event, status, errText = sysModuleReinitFailedEvent, "failed", reinitErr.Error()
	}
	setReinitStatus(requestID, status, errText)

	payload := map[string]any{
		"model":      SysModuleModelName,
		"request_id": requestID,
		"modules":    names,
		"state":      status,
	}
	if errText != "" {
		payload["error"] = errText
	}
	channels := []string{"servicebus:global"}
	if env := orm.EnvironmentFromContext(ctx); env != nil && env.UserID() > 0 {
		channels = append(channels, fmt.Sprintf("servicebus:user:%d", env.UserID()))
	}
	// base.service.bus is in modules/base-bus; looked up by name (see publishUIMenuUpdated).
	if _, err := orm.ExecuteRuntimeMethod(ctx, "base.service.bus", "publish", nil, []any{channels, event, payload}, nil); err != nil {
		log.Warnf("sys.module.module: publish %s: %v", event, err)
	}
}

// reinitStatusSysModule answers {status: running|succeeded|failed|unknown, error}.
func reinitStatusSysModule(_ context.Context, _ *orm.ModelMetadata, _ []string, _ []any, kwargs map[string]any) (any, error) {
	requestID, _ := kwargs["request_id"].(string)
	requestID = strings.TrimSpace(requestID)
	reinitTracker.mu.Lock()
	e, ok := reinitTracker.entries[requestID]
	reinitTracker.mu.Unlock()
	if !ok {
		return map[string]any{"status": "unknown"}, nil
	}
	return map[string]any{"status": e.status, "error": e.errText}, nil
}
