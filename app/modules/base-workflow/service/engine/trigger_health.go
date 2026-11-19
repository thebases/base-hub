package engine

// Trigger health (plan T99, WF-U-9 / WF-X-12): per-trigger liveness kept in memory, keyed like the
// registry (`<logical db>|<trigger id>`), and served by base.workflow.trigger.health.
//
// Invariants:
//   - Health is per process; every node of a multi-instance deployment reports its own view.
//   - Delivery handlers (mqtt worker, kafka consumer, tcp connection, model workers) update it
//     through this store only. It has its own lock-free/small-mutex state and never takes the
//     registry's r.mu, so a handler can never block on (or deadlock with) a register/unregister.
//   - ErrRunRetryScheduled means the run was accepted (the retry owner takes over): not a failure.
//   - Counters reset when a trigger is re-created (config change); last_error survives
//     unregistration so an operator can still see why a trigger stopped.

import (
	"sync"
	"sync/atomic"
	"time"

	"git.thebase.vn/base_hub/modules/base-workflow/models"
)

// triggerHealthErrorMax bounds the stored error text (provider errors can embed whole payloads).
const triggerHealthErrorMax = 500

type triggerHealthEntry struct {
	registered   atomic.Bool
	registeredAt atomic.Int64
	lastEventAt  atomic.Int64
	lastErrorAt  atomic.Int64
	events       atomic.Int64
	failures     atomic.Int64

	mu        sync.Mutex // guards lastError only
	lastError string
}

// triggerHealthStore maps registry key -> *triggerHealthEntry.
type triggerHealthStore struct{ entries sync.Map }

var triggerHealth = &triggerHealthStore{}

func (s *triggerHealthStore) entry(key string) *triggerHealthEntry {
	if existing, ok := s.entries.Load(key); ok {
		return existing.(*triggerHealthEntry)
	}
	created, _ := s.entries.LoadOrStore(key, &triggerHealthEntry{})
	return created.(*triggerHealthEntry)
}

func (s *triggerHealthStore) markRegistered(key string) {
	e := s.entry(key)
	e.registeredAt.Store(time.Now().Unix())
	e.registered.Store(true)
}

func (s *triggerHealthStore) markUnregistered(key string) {
	s.entry(key).registered.Store(false)
}

// recordError stores a subscribe/listen/delivery error without touching the counters.
func (s *triggerHealthStore) recordError(key string, err error) {
	if err == nil {
		return
	}
	msg := err.Error()
	if len(msg) > triggerHealthErrorMax {
		msg = msg[:triggerHealthErrorMax]
	}
	e := s.entry(key)
	e.mu.Lock()
	e.lastError = msg
	e.mu.Unlock()
	e.lastErrorAt.Store(time.Now().Unix())
}

// clearError forgets the stored error once the cause is gone (a subscribe that finally succeeded).
func (s *triggerHealthStore) clearError(key string) {
	e := s.entry(key)
	e.mu.Lock()
	e.lastError = ""
	e.mu.Unlock()
	e.lastErrorAt.Store(0)
}

// clearBlockedReason forgets a "workflow cannot run" error once the workflow can run again; any
// other stored error (a past delivery failure) is kept.
func (s *triggerHealthStore) clearBlockedReason(key string) {
	v, ok := s.entries.Load(key)
	if !ok {
		return
	}
	e := v.(*triggerHealthEntry)
	e.mu.Lock()
	defer e.mu.Unlock()
	switch e.lastError {
	case triggerBlockedNotFound, triggerBlockedInactive, triggerBlockedTemplate, triggerBlockedNotPublished:
		e.lastError = ""
		e.lastErrorAt.Store(0)
	}
}

// deliveryStart counts one event handed to the trigger.
func (s *triggerHealthStore) deliveryStart(key string) {
	e := s.entry(key)
	e.events.Add(1)
	e.lastEventAt.Store(time.Now().Unix())
}

// deliveryEnd records the outcome: nil and a scheduled retry are accepted deliveries.
func (s *triggerHealthStore) deliveryEnd(key string, err error) {
	if err == nil || isRunRetryScheduled(err) {
		return
	}
	s.entry(key).failures.Add(1)
	s.recordError(key, err)
}

func (s *triggerHealthStore) snapshot(key string) models.WorkflowTriggerHealth {
	v, ok := s.entries.Load(key)
	if !ok {
		return models.WorkflowTriggerHealth{}
	}
	e := v.(*triggerHealthEntry)
	e.mu.Lock()
	lastError := e.lastError
	e.mu.Unlock()
	return models.WorkflowTriggerHealth{
		Registered:   e.registered.Load(),
		RegisteredAt: e.registeredAt.Load(),
		LastEventAt:  e.lastEventAt.Load(),
		LastError:    lastError,
		LastErrorAt:  e.lastErrorAt.Load(),
		Events:       e.events.Load(),
		Failures:     e.failures.Load(),
	}
}

// triggerHealthForDB returns the health of ids in one logical database. Only that database's keys
// are read, so a tenant can never see another tenant's trigger with the same id.
func triggerHealthForDB(dbName string, ids []int64) map[int64]models.WorkflowTriggerHealth {
	out := make(map[int64]models.WorkflowTriggerHealth, len(ids))
	for _, id := range ids {
		out[id] = triggerHealth.snapshot(triggerKey(dbName, id))
	}
	return out
}

func init() { models.RegisterWorkflowTriggerHealthProvider(triggerHealthForDB) }
