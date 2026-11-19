package service

// File overview:
// - Responsibility: registry of Go-implemented automation handlers, mirroring internal/services/cron's
//   RegisterHandler/UnregisterHandler shape but scoped to automation.action's go_handler action_type.
//   This lets other modules (workflow, credential, ...) hand their compiled cron/automation logic to
//   base-automation's single dispatch bridge instead of each registering its own sys.cron row, without
//   base-automation importing those modules back (they call RegisterGoHandler at their own InitRuntime).
// - Key symbols: `GoHandlerFunc`, `RegisterGoHandler`, `UnregisterGoHandler`, `lookupGoHandler`.

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// GoHandlerFunc is one registered automation.action go_handler implementation.
// payload is the action's configured Values merged with any caller-supplied
// override (see mergeActionPayload in action_executor.go).
type GoHandlerFunc func(ctx context.Context, payload map[string]any) error

var (
	goHandlersMu sync.RWMutex
	goHandlers   = map[string]GoHandlerFunc{}
)

// RegisterGoHandler binds a handler key to its implementation. Duplicate keys
// are rejected so two modules cannot silently fight over one automation.action.
func RegisterGoHandler(key string, fn GoHandlerFunc) error {
	key = strings.TrimSpace(key)
	if key == "" {
		return fmt.Errorf("automation: go handler key is required")
	}
	if fn == nil {
		return fmt.Errorf("automation: go handler %q requires a function", key)
	}
	goHandlersMu.Lock()
	defer goHandlersMu.Unlock()
	if _, exists := goHandlers[key]; exists {
		return fmt.Errorf("automation: go handler %q is already registered", key)
	}
	goHandlers[key] = fn
	return nil
}

// UnregisterGoHandler removes a handler registration so module shutdown/init
// cycles (tests, restarts) stay symmetric with RegisterGoHandler's duplicate guard.
func UnregisterGoHandler(key string) {
	goHandlersMu.Lock()
	defer goHandlersMu.Unlock()
	delete(goHandlers, strings.TrimSpace(key))
}

func lookupGoHandler(key string) (GoHandlerFunc, bool) {
	goHandlersMu.RLock()
	defer goHandlersMu.RUnlock()
	fn, ok := goHandlers[strings.TrimSpace(key)]
	return fn, ok
}
